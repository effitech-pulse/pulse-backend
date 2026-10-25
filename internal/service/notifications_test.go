package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type notificationFake struct {
	records  domain.NotificationRecords
	query    domain.NotificationQuery
	err      error
	sourceID string
	deadline time.Duration
	count    domain.NotificationCount
	families []domain.NotificationFamily
	reason   string
}

func (f *notificationFake) NotificationCapabilities(context.Context, domain.NotificationScope) (domain.NotificationCapabilities, error) {
	return domain.NotificationCapabilities{Families: f.families, Reason: f.reason}, f.err
}
func (f *notificationFake) ReadNotificationPage(ctx context.Context, q domain.NotificationQuery) (domain.NotificationRecords, error) {
	f.query = q
	if d, ok := ctx.Deadline(); ok {
		f.deadline = time.Until(d)
	}
	if q.Before != nil && q.Before.Boundary != f.records.Boundary {
		return domain.NotificationRecords{}, domain.NewError(domain.Invalid, "private mismatch")
	}
	return f.records, f.err
}
func (f *notificationFake) CountUnreadNotifications(context.Context, domain.NotificationScope) (domain.NotificationCount, error) {
	return f.count, f.err
}
func (f *notificationFake) MarkNotificationRead(_ context.Context, _ domain.NotificationScope, id int64) (domain.NotificationRead, error) {
	return domain.NotificationRead{ID: id, ReadAt: time.Date(2026, 10, 25, 12, 0, 0, 123456000, time.UTC)}, f.err
}
func (f *notificationFake) ReadNotificationAlert(_ context.Context, _ domain.NotificationScope, l domain.NotificationAlertLocator) (domain.NotificationAlertContext, error) {
	return domain.NotificationAlertContext{Target: domain.NotificationAlertTarget{Well: domain.NotificationWell{ID: l.WellID}, RunID: l.RunID, AlertID: l.AlertID}}, f.err
}
func (f *notificationFake) ReadNotificationSource(_ context.Context, _ domain.NotificationScope, id string) (domain.NotificationSourceOverview, error) {
	f.sourceID = id
	return domain.NotificationSourceOverview{ID: id}, f.err
}
func notificationContext() context.Context {
	id := int64(41)
	return domain.WithPrincipal(context.Background(), domain.Principal{AccountID: &id, Authenticated: true, Mechanism: "session", Authorization: &domain.AuthorizationSnapshot{AccountID: id, Active: true, Epoch: 1, GraphRevision: 2, Permissions: []domain.Permission{"wells.read", "risks.read", "alerts.read"}}})
}
func notificationFixture() domain.NotificationRecord {
	return domain.NotificationRecord{Key: domain.NotificationKey{ID: 9007199254740993, RecordedAt: time.Date(2026, 10, 25, 12, 0, 0, 123456000, time.UTC)}, Kind: domain.NotificationKindAlertCreated, Code: domain.NotificationCodeAlertCreated, Alert: &domain.NotificationAlertTarget{Well: domain.NotificationWell{ID: 9007199254740993}, RunID: 9007199254740995, AlertID: 9007199254740997}}
}
func notificationService(t *testing.T, f *notificationFake, now func() time.Time) *Notifications {
	t.Helper()
	s, e := NewNotifications(f, nil, NotificationsOptions{AuthMode: "local", Mode: "research", Prefix: "/api/v1", UIEnabled: true, Now: now})
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestNotificationCursorBoundariesAndBudget(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	r := notificationFixture()
	f := &notificationFake{records: domain.NotificationRecords{Items: []domain.NotificationRecord{r, r}, Boundary: domain.NotificationBoundary{AccountID: 41, Epoch: 1, GraphRevision: 2, ActivationID: "active"}}}
	s := notificationService(t, f, func() time.Time { return now })
	ctx := notificationContext()
	page, e := s.List(ctx, "unread", 1, "")
	if e != nil || page.NextCursor == nil {
		t.Fatal(page, e)
	}
	token := *page.NextCursor
	if _, e = s.List(ctx, "unread", 1, token); e != nil {
		t.Fatal(e)
	}
	if f.query.Before.Key.ID != 9007199254740993 || f.deadline <= 0 || f.deadline > 5*time.Second {
		t.Fatal(f.query, f.deadline)
	}
	checks := []struct {
		name  string
		ctx   context.Context
		state domain.NotificationState
		limit int
		token string
	}{{"tamper", ctx, "unread", 1, token + "x"}, {"oversize", ctx, "unread", 1, strings.Repeat("a", 4097)}, {"state", ctx, "all", 1, token}, {"limit", ctx, "unread", 2, token}}
	for _, change := range []string{"account", "epoch", "graph", "funds"} {
		p, _ := domain.CurrentPrincipal(notificationContext())
		a := *p.Authorization
		p.Authorization = &a
		switch change {
		case "account":
			id := int64(42)
			p.AccountID = &id
			a.AccountID = id
		case "epoch":
			a.Epoch++
		case "graph":
			a.GraphRevision++
		case "funds":
			p.Scope.Funds = []string{}
		}
		checks = append(checks, struct {
			name  string
			ctx   context.Context
			state domain.NotificationState
			limit int
			token string
		}{change, domain.WithPrincipal(context.Background(), p), "unread", 1, token})
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			_, e := s.List(check.ctx, check.state, check.limit, check.token)
			var d *domain.Error
			if !errors.As(e, &d) || d.Kind != domain.Invalid || d.Message != "Некорректный запрос." {
				t.Fatal(e)
			}
		})
	}
	f.records.Boundary.ActivationID = "changed"
	if _, e = s.List(ctx, "unread", 1, token); e == nil {
		t.Fatal("fresh boundary accepted")
	}
	f.records.Boundary.ActivationID = "active"
	other := notificationService(t, f, func() time.Time { return now })
	if _, e = other.List(ctx, "unread", 1, token); e == nil {
		t.Fatal("restart cursor accepted")
	}
	now = now.Add(30 * time.Minute)
	if _, e = s.List(ctx, "unread", 1, token); e == nil {
		t.Fatal("expired cursor accepted")
	}
}
func TestNotificationCapabilitiesAndSafeErrors(t *testing.T) {
	f := &notificationFake{}
	s := notificationService(t, f, nil)
	v, e := s.Capabilities(notificationContext())
	if e != nil || !v.Enabled || v.Reason != "no_families" || v.Families == nil {
		t.Fatal(v, e)
	}
	s.enabled = false
	v, e = s.Capabilities(notificationContext())
	if e != nil || v.Reason != "feature_off" {
		t.Fatal(v, e)
	}
	if _, e = s.List(notificationContext(), "", 25, ""); e == nil {
		t.Fatal("off list")
	}
	if _, e = s.Source(notificationContext(), "legacy%2F/a?+"); e != nil || f.sourceID != "legacy%2F/a?+" {
		t.Fatal(e)
	}
	for _, ctx := range []context.Context{context.Background(), domain.WithPrincipal(context.Background(), domain.TrustedLegacyPrincipal("test"))} {
		if _, e = s.Capabilities(ctx); e == nil {
			t.Fatal("nonpersonal accepted")
		}
	}
	s.enabled = true
	f.err = errors.New("secret-canary")
	if _, e = s.UnreadCount(notificationContext()); e == nil || strings.Contains(e.Error(), "canary") {
		t.Fatal(e)
	}
	f.err = domain.NewError(domain.Missing, "secret-canary")
	if _, e = s.MarkRead(notificationContext(), "nf1_1"); e == nil || e.Error() != "Уведомление недоступно." {
		t.Fatal(e)
	}
}
func TestNotificationCapabilityRejectsUnknownFamily(t *testing.T) {
	f := &notificationFake{families: []domain.NotificationFamily{"secret-canary"}}
	s := notificationService(t, f, nil)
	if _, e := s.Capabilities(notificationContext()); e == nil {
		t.Fatal("unknown family escaped allowlist")
	}
}

func TestNotificationInactiveCaptureCapability(t *testing.T) {
	f := &notificationFake{reason: "feature_off"}
	s := notificationService(t, f, nil)
	v, e := s.Capabilities(notificationContext())
	if e != nil || v.Reason != "feature_off" || v.Enabled {
		t.Fatal(v, e)
	}
}

type notificationScopeFixture struct {
	FundScope
	version string
	ids     []int64
}

func (f *notificationScopeFixture) Members(context.Context) (domain.Cohort, []domain.Well, error) {
	w := make([]domain.Well, 100)
	for i := range w {
		w[i].ID = f.ids[i]
	}
	return domain.Cohort{Version: f.version, ManifestSHA256: "manifest"}, w, nil
}
func TestNotificationCursorBindsCohort(t *testing.T) {
	ids := make([]int64, 100)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	scope := &notificationScopeFixture{version: "v1", ids: ids}
	r := notificationFixture()
	f := &notificationFake{records: domain.NotificationRecords{Items: []domain.NotificationRecord{r, r}}}
	s, e := NewNotifications(f, scope, NotificationsOptions{AuthMode: "local", Mode: "cohort", Prefix: "/api/v1", UIEnabled: true})
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.List(notificationContext(), "unread", 1, "")
	if e != nil || p.NextCursor == nil {
		t.Fatal(p, e)
	}
	scope.version = "v2"
	if _, e = s.List(notificationContext(), "unread", 1, *p.NextCursor); e == nil {
		t.Fatal("changed cohort version accepted")
	}
	scope.version = "v1"
	scope.ids[99] = 101
	if _, e = s.List(notificationContext(), "unread", 1, *p.NextCursor); e == nil {
		t.Fatal("changed membership accepted")
	}
}

func TestNotificationUIOffHidesAvailableFamilies(t *testing.T) {
	f := &notificationFake{families: []domain.NotificationFamily{domain.NotificationFamilyAlerts, domain.NotificationFamilyCollection}, reason: "enabled"}
	s := notificationService(t, f, nil)
	s.enabled = false
	v, e := s.Capabilities(notificationContext())
	if e != nil || v.Enabled || v.Reason != "feature_off" || v.Families == nil || len(v.Families) != 0 {
		t.Fatalf("UI-off capability: %+v, %v", v, e)
	}
}
