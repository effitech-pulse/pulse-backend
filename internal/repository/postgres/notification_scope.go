package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type notificationReadFrame struct {
	tx       pgx.Tx
	scope    domain.NotificationScope
	boundary domain.NotificationBoundary
	active   bool
	families []domain.NotificationFamily
}

func notificationHash(v any) (string, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return "", e
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// notificationReadTx serializes current account changes and freezes cohort membership.
func (r *Repository) notificationReadTx(ctx context.Context, s domain.NotificationScope) (notificationReadFrame, error) {
	f := notificationReadFrame{scope: s, families: []domain.NotificationFamily{}}
	p := s.Principal
	if !p.Authenticated || p.AccountID == nil || *p.AccountID <= 0 || p.Authorization == nil || p.Authorization.AccountID != *p.AccountID || p.Authorization.Require() != nil {
		return f, domain.NewError(domain.Unauthorized, "Требуется вход.")
	}
	if s.Mode != "research" && s.Mode != "cohort" {
		return f, domain.NewError(domain.Unavailable, "notification scope unavailable")
	}
	tx, e := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if e != nil {
		return f, e
	}
	f.tx = tx
	fail := func(e error) (notificationReadFrame, error) { _ = tx.Rollback(ctx); return notificationReadFrame{}, e }
	if _, e = tx.Exec(ctx, `SET LOCAL statement_timeout='3s'`); e != nil {
		return fail(e)
	}
	a, e := authorizationTx(ctx, tx, *p.AccountID)
	if e != nil {
		if isMissing(e) {
			e = domain.NewError(domain.Unauthorized, "Требуется вход.")
		}
		return fail(e)
	}
	if !a.Active || a.AccountID != *p.AccountID || a.Epoch != p.Authorization.Epoch {
		return fail(domain.NewError(domain.Unauthorized, "Требуется вход."))
	}
	f.scope.Principal.Authorization = &a
	f.boundary.AccountID = a.AccountID
	f.boundary.Epoch = a.Epoch
	f.boundary.GraphRevision = a.GraphRevision
	perms := slices.Clone(a.Permissions)
	slices.Sort(perms)
	legacy := a.LegacyScope
	legacy.Funds = slices.Clone(legacy.Funds)
	slices.Sort(legacy.Funds)
	if legacy.NGDUs != nil {
		ids := slices.Clone(*legacy.NGDUs)
		slices.Sort(ids)
		legacy.NGDUs = &ids
	}
	if legacy.Departments != nil {
		ids := slices.Clone(*legacy.Departments)
		slices.Sort(ids)
		legacy.Departments = &ids
	}
	f.boundary.AuthorizationSHA256, e = notificationHash(struct {
		Permissions  []domain.Permission
		Organization *domain.OrganizationScope
		Legacy       domain.AccessScope
	}{perms, a.Organization, legacy})
	if e != nil {
		return fail(e)
	}
	if s.Mode == "cohort" {
		c := s.Cohort
		if c == nil || c.Version == "" || c.ManifestSHA256 == "" || len(c.WellIDs) != 100 {
			return fail(fmt.Errorf("invalid notification cohort fence"))
		}
		query, args, e := build(sqlBuilder.Select("c.manifest_sha256", "m.well_id").From("app_cohort c").Join("app_cohort_member m ON m.version=c.version").Where(sq.Eq{"c.version": c.Version}).OrderBy("m.well_id").Suffix("FOR SHARE OF c,m"))
		if e != nil {
			return fail(e)
		}
		rows, e := tx.Query(ctx, query, args...)
		if e != nil {
			return fail(e)
		}
		ids := make([]int64, 0, 100)
		valid := true
		for rows.Next() {
			var id int64
			var manifest string
			if e = rows.Scan(&manifest, &id); e != nil {
				break
			}
			valid = valid && manifest == c.ManifestSHA256 && id > 0
			ids = append(ids, id)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return fail(e)
		}
		expected := slices.Clone(c.WellIDs)
		slices.Sort(expected)
		if !valid || len(ids) != 100 || !slices.Equal(ids, expected) {
			return fail(fmt.Errorf("notification cohort changed"))
		}
		for i := 1; i < len(ids); i++ {
			if ids[i] == ids[i-1] {
				return fail(fmt.Errorf("duplicate cohort member"))
			}
		}
		f.boundary.CohortSHA256, e = notificationHash(struct {
			Mode, Version, Manifest string
			IDs                     []int64
		}{s.Mode, c.Version, c.ManifestSHA256, ids})
	} else {
		f.boundary.CohortSHA256, e = notificationHash(struct{ Mode string }{s.Mode})
	}
	if e != nil {
		return fail(e)
	}
	var activation *string
	if e = tx.QueryRow(ctx, `SELECT activation_id FROM notification_activation WHERE id FOR SHARE`).Scan(&activation); e != nil {
		return fail(e)
	}
	if activation != nil {
		f.active = true
		f.boundary.ActivationID = *activation
	}
	if a.Require("alerts.read") == nil {
		f.families = append(f.families, domain.NotificationFamilyAlerts)
	}
	if a.Require("integrations.read") == nil && a.FullApplicationScope() {
		f.families = append(f.families, domain.NotificationFamilyCollection)
	}
	return f, nil
}
func (f notificationReadFrame) wellPredicate() sq.Sqlizer {
	where := sq.And{visibleWellPredicate(*f.scope.Principal.Authorization, "w")}
	if f.scope.Mode == "cohort" {
		where = append(where, sq.Eq{"w.id": f.scope.Cohort.WellIDs})
	}
	return where
}
func (f notificationReadFrame) predicate() sq.Sqlizer {
	branches := sq.Or{}
	if slices.Contains(f.families, domain.NotificationFamilyAlerts) {
		q := sqlBuilder.Select("1").From("ml_alert a").Join("ml_run r ON r.id=a.run_id").Join("well w ON w.id=a.app_well_id").Where("(a.id,a.run_id,a.app_well_id)=(f.alert_id,f.run_id,f.well_id)").Where(sq.Eq{"r.status": "done"}).Where(f.wellPredicate())
		branches = append(branches, sq.And{sq.Eq{"f.kind": "alert.created.v1"}, sq.Expr("EXISTS (?)", q)})
	}
	if slices.Contains(f.families, domain.NotificationFamilyCollection) {
		branches = append(branches, sq.And{sq.Eq{"f.kind": "collection.failed.v1"}, sq.Expr(`EXISTS(SELECT 1 FROM telemetry_collection_log l JOIN telemetry_source s ON s.id=l.source_id WHERE (l.id,l.source_id)=(f.collection_log_id,f.source_id) AND l.outcome='failed' AND l.attempt_generation IS NOT NULL)`)})
	}
	return branches
}
func notificationMissing() error {
	return domain.NewError(domain.Missing, "Уведомление недоступно.")
}
func (f notificationReadFrame) requireFeed() error {
	if !f.active {
		return domain.NewError(domain.Unavailable, "Уведомления временно недоступны.")
	}
	if len(f.families) == 0 {
		return domain.NewError(domain.Forbidden, "Недостаточно прав.")
	}
	return nil
}

// commit verifies the authority frame again while the actor/cohort SHARE locks remain held.
func (f notificationReadFrame) commit(ctx context.Context) error {
	a, e := authorization(ctx, f.tx, f.boundary.AccountID)
	if e != nil {
		return e
	}
	if !a.Active || a.Epoch != f.boundary.Epoch || a.GraphRevision != f.boundary.GraphRevision {
		return domain.NewError(domain.Unavailable, "notification authority changed")
	}
	return f.tx.Commit(ctx)
}
