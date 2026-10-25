package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

const NotificationTimeLayout = "2006-01-02T15:04:05.000000Z"

func notificationInvalid() error {
	return domain.NewError(domain.Invalid, "Некорректный запрос.")
}
func notificationUnavailable() error {
	return domain.NewError(domain.Unavailable, "Уведомления временно недоступны.")
}
func EncodeNotificationID(id int64) (string, error) {
	if id <= 0 {
		return "", notificationInvalid()
	}
	return "nf1_" + strconv.FormatInt(id, 10), nil
}
func DecodeNotificationID(id string) (int64, error) {
	if !strings.HasPrefix(id, "nf1_") {
		return 0, notificationInvalid()
	}
	raw := strings.TrimPrefix(id, "nf1_")
	n, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || n <= 0 || strconv.FormatInt(n, 10) != raw {
		return 0, notificationInvalid()
	}
	return n, nil
}
func notificationHash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type notificationCursor struct {
	Version   int                         `json:"v"`
	Issued    string                      `json:"issued_at"`
	Expires   string                      `json:"expires_at"`
	At        string                      `json:"recorded_at"`
	ID        string                      `json:"id"`
	Boundary  domain.NotificationBoundary `json:"boundary"`
	QueryHash string                      `json:"query_hash"`
	ScopeHash string                      `json:"scope_hash"`
}

func notificationQueryHash(state domain.NotificationState, limit int) string {
	return notificationHash([]string{"v1", string(state), strconv.Itoa(limit)})
}
func (s *Notifications) encodeCursor(key domain.NotificationKey, boundary domain.NotificationBoundary, state domain.NotificationState, limit int, scope domain.NotificationScope) (string, error) {
	now := s.now().UTC()
	c := notificationCursor{1, now.Format(NotificationTimeLayout), now.Add(30 * time.Minute).Format(NotificationTimeLayout), key.RecordedAt.UTC().Format(NotificationTimeLayout), strconv.FormatInt(key.ID, 10), boundary, notificationQueryHash(state, limit), notificationHash(scope)}
	raw, e := json.Marshal(c)
	if e != nil {
		return "", notificationUnavailable()
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, e = io.ReadFull(s.random, nonce); e != nil {
		return "", notificationUnavailable()
	}
	token := base64.RawURLEncoding.EncodeToString(s.aead.Seal(nonce, nonce, raw, []byte("pulse.notifications.cursor.v1")))
	if len(token) > 4096 {
		return "", notificationUnavailable()
	}
	return token, nil
}
func (s *Notifications) decodeCursor(token string, state domain.NotificationState, limit int, scope domain.NotificationScope) (*domain.NotificationPosition, error) {
	bad := notificationInvalid
	if len(token) > 4096 {
		return nil, bad()
	}
	b, e := base64.RawURLEncoding.Strict().DecodeString(token)
	n := s.aead.NonceSize()
	if e != nil || base64.RawURLEncoding.EncodeToString(b) != token || len(b) < n+s.aead.Overhead() {
		return nil, bad()
	}
	plain, e := s.aead.Open(nil, b[:n], b[n:], []byte("pulse.notifications.cursor.v1"))
	if e != nil {
		return nil, bad()
	}
	var c notificationCursor
	d := json.NewDecoder(bytes.NewReader(plain))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil {
		return nil, bad()
	}
	canonical, _ := json.Marshal(c)
	if !bytes.Equal(canonical, plain) {
		return nil, bad()
	}
	issued, e1 := time.Parse(NotificationTimeLayout, c.Issued)
	expiry, e2 := time.Parse(NotificationTimeLayout, c.Expires)
	at, e3 := time.Parse(NotificationTimeLayout, c.At)
	id, e4 := strconv.ParseInt(c.ID, 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || id <= 0 || strconv.FormatInt(id, 10) != c.ID || at.IsZero() || at.Format(NotificationTimeLayout) != c.At || issued.Format(NotificationTimeLayout) != c.Issued || expiry.Format(NotificationTimeLayout) != c.Expires || !expiry.Equal(issued.Add(30*time.Minute)) || issued.After(s.now()) || !expiry.After(s.now()) || c.Version != 1 || c.QueryHash != notificationQueryHash(state, limit) || c.ScopeHash != notificationHash(scope) {
		return nil, bad()
	}
	return &domain.NotificationPosition{Key: domain.NotificationKey{RecordedAt: at, ID: id}, Boundary: c.Boundary}, nil
}
