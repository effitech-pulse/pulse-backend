package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

const EventTimeLayout = "2006-01-02T15:04:05.000000Z"

func eventInvalid() error {
	return domain.NewError(domain.Invalid, "Некорректный запрос.")
}
func eventUnavailable(err error) error {
	return &domain.Error{Kind: domain.Unavailable, Message: "История временно недоступна.", Cause: err}
}
func eventMissing() error {
	return domain.NewError(domain.Missing, "Событие недоступно.")
}
func eventCursorError() error {
	return &domain.EventReadError{Cause: domain.NewError(domain.Invalid, "История изменилась. Обновите список."), Detail: "cursor_restart_required"}
}
func eventDecimal(s string) bool {
	n, e := strconv.ParseInt(s, 10, 64)
	return e == nil && n > 0 && strconv.FormatInt(n, 10) == s
}
func eventJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte{'\n'}), nil
}
func eventLocatorTuple(l domain.EventLocator) ([]string, error) {
	if l.Target == "" || !utf8.ValidString(l.Target) {
		return nil, eventInvalid()
	}
	rev := strconv.FormatInt(l.Revision, 10)
	switch l.Source {
	case "mc":
		if l.Revision == 0 {
			return []string{"manual_check", l.Target}, nil
		}
	case "review.check":
		if l.Revision > 0 {
			return []string{"review_event", "check", l.Target, rev}, nil
		}
	case "review.alert":
		if l.Revision > 0 && eventDecimal(l.Target) {
			return []string{"review_event", "alert", l.Target, rev}, nil
		}
	case "well":
		if l.Revision > 0 && eventDecimal(l.Target) {
			return []string{"well_event", l.Target, rev}, nil
		}
	}
	return nil, eventInvalid()
}
func EncodeEventID(l domain.EventLocator) (string, error) {
	a, err := eventLocatorTuple(l)
	if err != nil {
		return "", err
	}
	b, err := eventJSON(a)
	if err != nil {
		return "", err
	}
	s := "e1." + base64.RawURLEncoding.EncodeToString(b)
	if len(s) > 2048 {
		return "", eventInvalid()
	}
	return s, nil
}
func DecodeEventID(s string) (domain.EventLocator, error) {
	var l domain.EventLocator
	if len(s) > 2048 || !strings.HasPrefix(s, "e1.") {
		return l, eventInvalid()
	}
	b, e := base64.RawURLEncoding.Strict().DecodeString(s[3:])
	if e != nil {
		return l, eventInvalid()
	}
	var a []string
	if json.Unmarshal(b, &a) != nil {
		return l, eventInvalid()
	}
	switch {
	case len(a) == 2 && a[0] == "manual_check":
		l = domain.EventLocator{Source: "mc", Target: a[1]}
	case len(a) == 4 && a[0] == "review_event" && (a[1] == "check" || a[1] == "alert"):
		if !eventDecimal(a[3]) {
			return l, eventInvalid()
		}
		l.Source = "review." + a[1]
		l.Target = a[2]
		l.Revision, _ = strconv.ParseInt(a[3], 10, 64)
	case len(a) == 3 && a[0] == "well_event":
		if !eventDecimal(a[2]) {
			return l, eventInvalid()
		}
		l.Source = "well"
		l.Target = a[1]
		l.Revision, _ = strconv.ParseInt(a[2], 10, 64)
	default:
		return l, eventInvalid()
	}
	canonical, err := EncodeEventID(l)
	if err != nil || canonical != s {
		return l, eventInvalid()
	}
	return l, nil
}
func eventHash(v any) string {
	b, _ := eventJSON(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// EventURLFits считает уже escaped pathname+search, без origin и fragment.
func EventURLFits(target string) bool { return len([]byte(target)) <= 4096 }
func (s *Events) eventEmission(e domain.EventSummary) error {
	seg := url.PathEscape(e.EventID)
	well := strconv.FormatInt(e.Well.ID, 10)
	tab := "history"
	if e.Target.Source == "mc" || e.Target.Source == "review.check" {
		tab = "checks"
	}
	for _, u := range []string{s.prefix + "/events/" + seg, "/events/" + seg, "/wells/" + well + "?" + url.Values{"tab": {tab}, "event": {e.EventID}}.Encode()} {
		if !EventURLFits(u) {
			return eventUnavailable(fmt.Errorf("event URL budget"))
		}
	}
	return nil
}
func eventFilterValues(f domain.EventFilter) url.Values {
	v := url.Values{"limit": {strconv.Itoa(f.Limit)}, "types": {strings.Join(f.Types, ",")}, "archive": {f.Archive}}
	if f.WellID != nil {
		v.Set("well_id", strconv.FormatInt(*f.WellID, 10))
	}
	if f.From != nil {
		v.Set("from", f.From.UTC().Format(EventTimeLayout))
	}
	if f.To != nil {
		v.Set("to", f.To.UTC().Format(EventTimeLayout))
	}
	return v
}

type eventCursorKey struct {
	OccurredAt string `json:"occurred_at"`
	SourceRank int16  `json:"source_rank"`
	TargetKey  string `json:"target_key"`
	Revision   string `json:"revision"`
}
type eventCursor struct {
	Version   int            `json:"v"`
	Expires   string         `json:"expires_at"`
	Last      eventCursorKey `json:"lastK"`
	QueryHash string         `json:"query_hash"`
	ScopeHash string         `json:"scope_hash"`
}

func (s *Events) encodeCursor(k domain.EventKey, f domain.EventFilter, scope domain.EventReadScope) (string, error) {
	k.OccurredAt = k.OccurredAt.UTC()
	b, e := eventJSON(eventCursor{1, s.now().UTC().Add(30 * time.Minute).Format(EventTimeLayout), eventCursorKey{k.OccurredAt.Format(EventTimeLayout), k.SourceRank, k.TargetKey, strconv.FormatInt(k.Revision, 10)}, eventHash(f), eventHash(scope)})
	if e != nil || len(b) > 2048 {
		return "", eventUnavailable(e)
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, e = io.ReadFull(s.random, nonce); e != nil {
		return "", eventUnavailable(e)
	}
	token := base64.RawURLEncoding.EncodeToString(s.aead.Seal(nonce, nonce, b, []byte("pulse.events.cursor.v1")))
	v := eventFilterValues(f)
	v.Set("cursor", token)
	if len(token) > 2048 || !EventURLFits(s.prefix+"/events?"+v.Encode()) {
		return "", eventUnavailable(fmt.Errorf("cursor URL budget"))
	}
	return token, nil
}
func (s *Events) decodeCursor(token string, f domain.EventFilter, scope domain.EventReadScope) (*domain.EventKey, error) {
	bad := eventCursorError
	if len(token) > 2048 {
		return nil, bad()
	}
	b, e := base64.RawURLEncoding.Strict().DecodeString(token)
	if e != nil || base64.RawURLEncoding.EncodeToString(b) != token || len(b) < s.aead.NonceSize()+s.aead.Overhead() {
		return nil, bad()
	}
	n := s.aead.NonceSize()
	plain, e := s.aead.Open(nil, b[:n], b[n:], []byte("pulse.events.cursor.v1"))
	if e != nil || len(plain) > 2048 {
		return nil, bad()
	}
	var c eventCursor
	d := json.NewDecoder(bytes.NewReader(plain))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil {
		return nil, bad()
	}
	canonical, _ := eventJSON(c)
	if !bytes.Equal(plain, canonical) {
		return nil, bad()
	}
	expiry, e := time.Parse(EventTimeLayout, c.Expires)
	if e != nil || expiry.Format(EventTimeLayout) != c.Expires || !expiry.After(s.now()) || c.Version != 1 || c.QueryHash != eventHash(f) || c.ScopeHash != eventHash(scope) {
		return nil, bad()
	}
	at, parseErr := time.Parse(EventTimeLayout, c.Last.OccurredAt)
	rev, revErr := strconv.ParseInt(c.Last.Revision, 10, 64)
	if parseErr != nil || at.Format(EventTimeLayout) != c.Last.OccurredAt || revErr != nil || strconv.FormatInt(rev, 10) != c.Last.Revision {
		return nil, bad()
	}
	k := domain.EventKey{OccurredAt: at, SourceRank: c.Last.SourceRank, TargetKey: c.Last.TargetKey, Revision: rev}
	if k.OccurredAt.IsZero() || k.OccurredAt.Nanosecond()%1000 != 0 || k.SourceRank < 1 || k.SourceRank > 4 {
		return nil, bad()
	}
	source := []string{"", "mc", "review.check", "review.alert", "well"}[k.SourceRank]
	if _, e = eventLocatorTuple(domain.EventLocator{Source: source, Target: k.TargetKey, Revision: k.Revision}); e != nil {
		return nil, bad()
	}
	return &k, nil
}
