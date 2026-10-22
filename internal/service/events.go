package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type EventStore interface {
	ReadEventPage(context.Context, domain.EventReadQuery) ([]domain.EventRecord, error)
	ReadEvent(context.Context, domain.EventReadScope, domain.EventLocator) (domain.EventRecord, error)
}
type EventsOptions struct {
	Mode   string
	Prefix string
	Now    func() time.Time
	Random io.Reader
}
type Events struct {
	store        EventStore
	scope        FundScope
	mode, prefix string
	now          func() time.Time
	random       io.Reader
	aead         cipher.AEAD
}

var EventTypes = []string{"manual_check.saved.v1", "review.check.recorded.v1", "review.alert.recorded.v1", "well.metadata_changed.v1", "well.archived.v1", "well.restored.v1"}

func NewEvents(store EventStore, scope FundScope, o EventsOptions) (*Events, error) {
	if store == nil || (o.Mode != "research" && o.Mode != "cohort") || (o.Mode == "cohort" && scope == nil) || o.Prefix == "" {
		return nil, fmt.Errorf("events require explicit store/mode/prefix/scope")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Random == nil {
		o.Random = rand.Reader
	}
	key := make([]byte, 32)
	if _, e := io.ReadFull(o.Random, key); e != nil {
		return nil, fmt.Errorf("events entropy: %w", e)
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	a, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	return &Events{store: store, scope: scope, mode: o.Mode, prefix: o.Prefix, now: o.Now, random: o.Random, aead: a}, nil
}
func (s *Events) readScope(ctx context.Context) (domain.EventReadScope, error) {
	p, ok := domain.CurrentPrincipal(ctx)
	if !ok || !((p.Mechanism == "legacy" && !p.Authenticated && p.AccountID == nil) || (p.Mechanism == "session" && p.Authenticated && p.AccountID != nil && *p.AccountID > 0)) {
		return domain.EventReadScope{}, domain.NewError(domain.Unauthorized, "Требуется вход.")
	}
	if !slices.Contains([]string{"admin", "technologist", "viewer"}, p.Role) {
		return domain.EventReadScope{}, domain.NewError(domain.Forbidden, "Недостаточно прав.")
	}
	out := domain.EventReadScope{Principal: p, Mode: s.mode}
	// Canonicalize scope sets while preserving nil versus explicitly empty slices.
	out.Principal.Scope.Funds = slices.Clone(p.Scope.Funds)
	sort.Strings(out.Principal.Scope.Funds)
	for _, pair := range []struct {
		src *[]int64
		dst **[]int64
	}{{p.Scope.NGDUs, &out.Principal.Scope.NGDUs}, {p.Scope.Departments, &out.Principal.Scope.Departments}} {
		if pair.src != nil {
			v := slices.Clone(*pair.src)
			slices.Sort(v)
			*pair.dst = &v
		}
	}
	if s.mode == "cohort" {
		c, w, e := s.scope.Members(ctx)
		if e != nil {
			return out, eventUnavailable(e)
		}
		f := &domain.EventCohortFence{Version: c.Version, Manifest: c.ManifestSHA256, IDs: make([]int64, 0, len(w))}
		seen := map[int64]bool{}
		for _, v := range w {
			if v.ID <= 0 || seen[v.ID] {
				return out, eventUnavailable(fmt.Errorf("cohort IDs"))
			}
			seen[v.ID] = true
			f.IDs = append(f.IDs, v.ID)
		}
		slices.Sort(f.IDs)
		if len(w) != 100 || f.Version == "" || f.Manifest == "" {
			return out, eventUnavailable(fmt.Errorf("incomplete cohort"))
		}
		out.Cohort = f
	}
	return out, nil
}
func normalizeEventFilter(f domain.EventFilter) (domain.EventFilter, error) {
	if f.Limit == 0 {
		f.Limit = 25
	}
	if f.Limit < 1 || f.Limit > 100 {
		return f, eventInvalid()
	}
	if f.Archive == "" {
		f.Archive = "all"
	}
	if !slices.Contains([]string{"all", "current", "archived"}, f.Archive) {
		return f, eventInvalid()
	}
	if f.Types == nil {
		f.Types = slices.Clone(EventTypes)
	} else {
		f.Types = slices.Clone(f.Types)
	}
	seen := map[string]bool{}
	if len(f.Types) == 0 || len(f.Types) > 6 {
		return f, eventInvalid()
	}
	for _, v := range f.Types {
		if !slices.Contains(EventTypes, v) || seen[v] {
			return f, eventInvalid()
		}
		seen[v] = true
	}
	sort.Strings(f.Types)
	if f.WellID != nil && *f.WellID <= 0 {
		return f, eventInvalid()
	}
	for _, p := range []**time.Time{&f.From, &f.To} {
		if *p != nil {
			t := (**p).UTC()
			if t.Nanosecond()%1000 != 0 {
				return f, eventInvalid()
			}
			*p = &t
		}
	}
	if f.From != nil && f.To != nil && !f.From.Before(*f.To) {
		return f, eventInvalid()
	}
	return f, nil
}
func (s *Events) List(ctx context.Context, f domain.EventFilter, cursor string) (domain.EventPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out := domain.EventPage{Items: []domain.EventSummary{}}
	scope, e := s.readScope(ctx)
	if e != nil {
		return out, e
	}
	f, e = normalizeEventFilter(f)
	if e != nil {
		return out, e
	}
	q := domain.EventReadQuery{Scope: scope, Filter: f}
	if cursor != "" {
		q.Before, e = s.decodeCursor(cursor, f, scope)
		if e != nil {
			return out, e
		}
	}
	records, e := s.store.ReadEventPage(ctx, q)
	if e != nil {
		return out, eventStoreError(e)
	}
	out.HasMore = len(records) > f.Limit
	if out.HasMore {
		records = records[:f.Limit]
	}
	for _, r := range records {
		summary, e := s.summary(r)
		if e != nil {
			return domain.EventPage{}, e
		}
		out.Items = append(out.Items, summary)
	}
	if out.HasMore {
		token, e := s.encodeCursor(records[len(records)-1].Key, f, scope)
		if e != nil {
			return domain.EventPage{}, e
		}
		out.NextCursor = &token
	}
	return out, nil
}
func (s *Events) Get(ctx context.Context, id string) (domain.EventDetail, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	scope, e := s.readScope(ctx)
	if e != nil {
		return domain.EventDetail{}, e
	}
	l, e := DecodeEventID(id)
	if e != nil {
		return domain.EventDetail{}, e
	}
	r, e := s.store.ReadEvent(ctx, scope, l)
	if e != nil {
		return domain.EventDetail{}, eventStoreError(e)
	}
	summary, e := s.summary(r)
	return domain.EventDetail{Event: summary, Record: r}, e
}
func eventStoreError(e error) error {
	var de *domain.Error
	if errors.As(e, &de) && de.Kind == domain.Missing {
		return eventMissing()
	}
	return eventUnavailable(e)
}
func eventEnum(s *string, allowed ...string) *string {
	if s == nil {
		return nil
	}
	v := "unknown"
	if slices.Contains(allowed, *s) {
		v = *s
	}
	return &v
}
func eventActor(a *domain.EventActorSnapshot) domain.EventActor {
	unknown := domain.EventActor{Kind: "unknown"}
	if a == nil || a.ID == nil || a.Authenticated == nil {
		return unknown
	}
	kind := ""
	if *a.Authenticated && strings.HasPrefix(*a.ID, "user:") && eventDecimal(strings.TrimPrefix(*a.ID, "user:")) {
		kind = "person"
	}
	if !*a.Authenticated && (*a.ID == "shared" || *a.ID == "service") {
		kind = *a.ID
	}
	if kind == "" {
		return unknown
	}
	name := a.Name
	if name != nil && *name == "" {
		name = nil
	}
	return domain.EventActor{Kind: kind, ID: a.ID, Name: name, Authenticated: a.Authenticated}
}
func (s *Events) summary(r domain.EventRecord) (domain.EventSummary, error) {
	id, e := EncodeEventID(r.Locator)
	if e != nil {
		return domain.EventSummary{}, eventUnavailable(e)
	}
	v := domain.EventSummary{EventID: id, OccurredAt: r.Key.OccurredAt.UTC(), Well: r.Well, Target: r.Locator, Actor: eventActor(r.ActorSnapshot)}
	switch r.Locator.Source {
	case "mc":
		v.EventType = EventTypes[0]
		v.Actor = eventActor(nil)
		v.Outcome.Verdict = eventEnum(r.Verdict, "alert", "watch", "norm", "insufficient")
	case "review.check":
		v.EventType = EventTypes[1]
	case "review.alert":
		v.EventType = EventTypes[2]
	case "well":
		switch r.ChangeKind {
		case "metadata":
			v.EventType = EventTypes[3]
		case "archive":
			v.EventType = EventTypes[4]
		case "restore":
			v.EventType = EventTypes[5]
		default:
			return v, eventUnavailable(fmt.Errorf("event kind"))
		}
	}
	if strings.HasPrefix(r.Locator.Source, "review.") {
		v.Outcome.ReviewStatus = eventEnum(r.ReviewStatus, "new", "in_progress", "closed")
		if r.Conclusion != nil && *r.Conclusion != "" {
			v.Outcome.Conclusion = eventEnum(r.Conclusion, "confirmed", "not_confirmed", "inconclusive")
		}
	}
	return v, s.eventEmission(v)
}
