package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type Notifications struct {
	store                  NotificationStore
	scope                  FundScope
	authMode, mode, prefix string
	enabled                bool
	now                    func() time.Time
	random                 io.Reader
	aead                   cipher.AEAD
}

func NewNotifications(store NotificationStore, scope FundScope, o NotificationsOptions) (*Notifications, error) {
	if store == nil || (o.AuthMode != "local" && o.AuthMode != "legacy") || (o.Mode != "research" && o.Mode != "cohort") || (o.Mode == "cohort" && scope == nil) || !strings.HasPrefix(o.Prefix, "/") || strings.HasSuffix(o.Prefix, "/") {
		return nil, fmt.Errorf("notifications require explicit store/auth/mode/prefix/scope")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Random == nil {
		o.Random = rand.Reader
	}
	key := make([]byte, 32)
	if _, e := io.ReadFull(o.Random, key); e != nil {
		return nil, fmt.Errorf("notifications entropy: %w", e)
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	a, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	return &Notifications{store: store, scope: scope, authMode: o.AuthMode, mode: o.Mode, prefix: o.Prefix, enabled: o.UIEnabled, now: o.Now, random: o.Random, aead: a}, nil
}
func notificationStoreError(err error) error {
	var e *domain.Error
	if errors.As(err, &e) {
		switch e.Kind {
		case domain.Invalid:
			return notificationInvalid()
		case domain.Unauthorized:
			return domain.NewError(domain.Unauthorized, "Требуется вход.")
		case domain.Forbidden:
			return domain.NewError(domain.Forbidden, "Недостаточно прав.")
		case domain.Missing:
			return domain.NewError(domain.Missing, "Уведомление недоступно.")
		}
	}
	return notificationUnavailable()
}
func (s *Notifications) readScope(ctx context.Context) (domain.NotificationScope, error) {
	p, ok := domain.CurrentPrincipal(ctx)
	out := domain.NotificationScope{Principal: p, Mode: s.mode}
	if s.authMode != "local" || !ok || !p.Authenticated || p.Mechanism != "session" || p.AccountID == nil || *p.AccountID <= 0 || p.Authorization == nil || p.Authorization.AccountID != *p.AccountID {
		return out, domain.NewError(domain.Unauthorized, "Требуется вход.")
	}
	if p.Authorization.Require() != nil {
		return out, domain.NewError(domain.Forbidden, "Недостаточно прав.")
	}
	// Cursor scope ignores presentation metadata and canonicalizes authority sets.
	out.Principal.Name = ""
	out.Principal.Role = ""
	a := *p.Authorization
	a.Sources = nil
	a.Permissions = slices.Clone(a.Permissions)
	slices.Sort(a.Permissions)
	out.Principal.Authorization = &a
	out.Principal.Scope = notificationCanonicalScope(p.Scope)
	a.LegacyScope = notificationCanonicalScope(a.LegacyScope)
	if s.mode == "cohort" {
		c, w, e := s.scope.Members(ctx)
		if e != nil {
			return out, notificationStoreError(e)
		}
		f := &domain.NotificationCohortFence{Version: c.Version, ManifestSHA256: c.ManifestSHA256, WellIDs: make([]int64, 0, len(w))}
		seen := map[int64]bool{}
		for _, v := range w {
			if v.ID <= 0 || seen[v.ID] {
				return out, notificationUnavailable()
			}
			seen[v.ID] = true
			f.WellIDs = append(f.WellIDs, v.ID)
		}
		slices.Sort(f.WellIDs)
		if len(w) != 100 || f.Version == "" || f.ManifestSHA256 == "" {
			return out, notificationUnavailable()
		}
		out.Cohort = f
	}
	return out, nil
}
func notificationCanonicalScope(v domain.AccessScope) domain.AccessScope {
	v.Funds = slices.Clone(v.Funds)
	slices.Sort(v.Funds)
	if v.NGDUs != nil {
		a := slices.Clone(*v.NGDUs)
		slices.Sort(a)
		v.NGDUs = &a
	}
	if v.Departments != nil {
		a := slices.Clone(*v.Departments)
		slices.Sort(a)
		v.Departments = &a
	}
	return v
}
func (s *Notifications) capabilities(ctx context.Context, scope domain.NotificationScope) (domain.NotificationCapabilities, error) {
	v, e := s.store.NotificationCapabilities(ctx, scope)
	if e != nil {
		return v, notificationStoreError(e)
	}
	seen := map[domain.NotificationFamily]bool{}
	for _, family := range v.Families {
		if (family != domain.NotificationFamilyAlerts && family != domain.NotificationFamilyCollection) || seen[family] {
			return domain.NotificationCapabilities{}, notificationUnavailable()
		}
		seen[family] = true
	}
	if v.Families == nil {
		v.Families = []domain.NotificationFamily{}
	}
	if !s.enabled || v.Reason == "feature_off" {
		v.Enabled = false
		v.Reason = "feature_off"
		v.Families = []domain.NotificationFamily{}
	} else if len(v.Families) == 0 {
		v.Enabled = true
		v.Reason = "no_families"
	} else {
		v.Enabled = true
		v.Reason = "enabled"
	}
	return v, nil
}
func (s *Notifications) Capabilities(ctx context.Context) (domain.NotificationCapabilities, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	scope, e := s.readScope(ctx)
	if e != nil {
		return domain.NotificationCapabilities{}, e
	}
	return s.capabilities(ctx, scope)
}
func (s *Notifications) dataScope(ctx context.Context) (domain.NotificationScope, error) {
	scope, e := s.readScope(ctx)
	if e != nil {
		return scope, e
	}
	if !s.enabled {
		return scope, notificationUnavailable()
	}
	return scope, nil
}
func (s *Notifications) List(ctx context.Context, state domain.NotificationState, limit int, cursor string) (domain.NotificationPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out := domain.NotificationPage{Items: []domain.NotificationRecord{}}
	scope, e := s.dataScope(ctx)
	if e != nil {
		return out, e
	}
	if state == "" {
		state = domain.NotificationStateUnread
	}
	if limit == 0 {
		limit = 25
	}
	if (state != domain.NotificationStateUnread && state != domain.NotificationStateAll) || limit < 1 || limit > 100 {
		return out, notificationInvalid()
	}
	q := domain.NotificationQuery{Scope: scope, State: state, Limit: limit}
	if cursor != "" {
		q.Before, e = s.decodeCursor(cursor, state, limit, scope)
		if e != nil {
			return out, e
		}
	}
	records, e := s.store.ReadNotificationPage(ctx, q)
	if e != nil {
		return out, notificationStoreError(e)
	}
	if len(records.Items) > limit+1 {
		return out, notificationUnavailable()
	}
	more := len(records.Items) > limit
	if more {
		records.Items = records.Items[:limit]
	}
	for _, r := range records.Items {
		if r.Validate() != nil || (r.Collection != nil && !s.sourceURLFits(r.Collection.SourceID)) {
			return out, notificationUnavailable()
		}
	}
	out.Items = records.Items
	if out.Items == nil {
		out.Items = []domain.NotificationRecord{}
	}
	if more {
		token, e := s.encodeCursor(out.Items[len(out.Items)-1].Key, records.Boundary, state, limit, scope)
		if e != nil {
			return domain.NotificationPage{}, e
		}
		out.NextCursor = &token
	}
	return out, nil
}
func (s *Notifications) UnreadCount(ctx context.Context) (domain.NotificationCount, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	scope, e := s.dataScope(ctx)
	if e != nil {
		return domain.NotificationCount{}, e
	}
	v, e := s.store.CountUnreadNotifications(ctx, scope)
	if e != nil {
		return domain.NotificationCount{}, notificationStoreError(e)
	}
	if v.Value < 0 || v.Value > 99 || (v.Capped && v.Value != 99) {
		return domain.NotificationCount{}, notificationUnavailable()
	}
	return v, nil
}
func (s *Notifications) MarkRead(ctx context.Context, id string) (domain.NotificationRead, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	scope, e := s.dataScope(ctx)
	if e != nil {
		return domain.NotificationRead{}, e
	}
	n, e := DecodeNotificationID(id)
	if e != nil {
		return domain.NotificationRead{}, e
	}
	v, e := s.store.MarkNotificationRead(ctx, scope, n)
	if e != nil {
		return v, notificationStoreError(e)
	}
	if v.ID != n || v.ReadAt.IsZero() {
		return domain.NotificationRead{}, notificationUnavailable()
	}
	return v, nil
}
func (s *Notifications) Alert(ctx context.Context, target domain.NotificationAlertLocator) (domain.NotificationAlertContext, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	scope, e := s.readScope(ctx)
	if e != nil {
		return domain.NotificationAlertContext{}, e
	}
	if target.Validate() != nil {
		return domain.NotificationAlertContext{}, notificationInvalid()
	}
	v, e := s.store.ReadNotificationAlert(ctx, scope, target)
	if e != nil {
		return v, notificationStoreError(e)
	}
	if v.Target.Well.ID != target.WellID || v.Target.RunID != target.RunID || v.Target.AlertID != target.AlertID {
		return domain.NotificationAlertContext{}, notificationUnavailable()
	}
	return v, nil
}
func (s *Notifications) Source(ctx context.Context, id string) (domain.NotificationSourceOverview, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	scope, e := s.readScope(ctx)
	if e != nil {
		return domain.NotificationSourceOverview{}, e
	}
	if id == "" || !utf8.ValidString(id) {
		return domain.NotificationSourceOverview{}, notificationInvalid()
	}
	v, e := s.store.ReadNotificationSource(ctx, scope, id)
	if e != nil {
		return v, notificationStoreError(e)
	}
	if v.ID != id || !s.sourceURLFits(id) {
		return domain.NotificationSourceOverview{}, notificationUnavailable()
	}
	return v, nil
}

func (s *Notifications) sourceURLFits(id string) bool {
	encoded := url.QueryEscape(id)
	return len(s.prefix+"/notification-targets/source?source_id="+encoded) <= 4096 && len("/integrations/source-overview?source_id="+encoded) <= 4096
}
