package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type CohortTelemetry interface {
	ReadApp(context.Context, domain.Well, time.Time, time.Time) (domain.TelemetryResult, error)
}
type CohortSnapshotStore interface {
	CohortSnapshot(context.Context, string, *int64) (domain.CohortSnapshot, error)
	CohortAlerts(context.Context, int64) ([]domain.CohortAlert, error)
	CohortRuns(context.Context, string, int) ([]domain.MLRun, error)
}
type CohortRunLease interface {
	PersistCohort(context.Context, int64, domain.CohortSnapshot) error
}

// ScoreCohort обходит зафиксированный фонд последовательно с общим срезом времени.
func ScoreCohort(ctx context.Context, scope FundScope, source CohortTelemetry, client MLClient, run int64, asof time.Time) (domain.CohortSnapshot, error) {
	capture, stopCapture := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	cohort, wells, err := scope.Members(capture)
	stopCapture()
	if err != nil {
		return domain.CohortSnapshot{}, err
	}
	asof = asof.UTC()
	start := asof.Add(-60 * 24 * time.Hour)
	token := make([]byte, 16)
	if _, err = rand.Read(token); err != nil {
		return domain.CohortSnapshot{}, err
	}
	id := hex.EncodeToString(token)
	out := domain.CohortSnapshot{Profile: "cohort", CohortVersion: cohort.Version, ManifestSHA256: cohort.ManifestSHA256, SnapshotID: &id, RunID: &run, AsOf: &asof, ProvenanceStatus: "partial", OutcomeCompleteness: "complete", Outcomes: make([]domain.CohortOutcome, 0, len(wells))}
	var model *cohortModelIdentity
	changed := false
	for _, w := range wells {
		o := domain.CohortOutcome{AppWellID: w.ID, Metadata: w, Status: "error", Bounds: &domain.TelemetryBounds{Start: &start, End: &asof}}
		switch {
		case w.ArchivedAt != nil:
			o.Status = "archived"
			o.ReasonCode = "archived"
		case !w.Active:
			o.Status = "inactive"
			o.ReasonCode = "inactive"
		case ctx.Err() != nil:
			o.ReasonCode = "run_timeout"
		case changed:
			o.ReasonCode = "bundle_changed"
		default:
			var identity *cohortModelIdentity
			o, identity = scoreCohortMember(ctx, o, w, source, client, start, asof)
			if identity != nil {
				if model != nil && *model != *identity {
					changed = true
					o.Status = "error"
					o.ReasonCode = "bundle_changed"
					o.Result = nil
				} else {
					model = identity
				}
			}
		}
		out.Outcomes = append(out.Outcomes, o)
	}
	if changed {
		for i := range out.Outcomes {
			if out.Outcomes[i].Status == "scored" {
				out.Outcomes[i].Status = "error"
				out.Outcomes[i].ReasonCode = "bundle_changed"
				out.Outcomes[i].Result = nil
			}
		}
	}
	out.Recount()
	return out, nil
}

type cohortModelIdentity struct{ version, rule string }

func scoreCohortMember(ctx context.Context, o domain.CohortOutcome, w domain.Well, source CohortTelemetry, client MLClient, start, end time.Time) (domain.CohortOutcome, *cohortModelIdentity) {
	o.ReasonCode = "source_failure"
	data, err := source.ReadApp(ctx, w, start, end)
	if err != nil {
		if ctx.Err() != nil {
			o.ReasonCode = "run_timeout"
		}
		return o, nil
	}
	// Ограничиваем фактический input общей границей даже для нестандартного источника.
	points := data.Points[:0]
	for _, p := range data.Points {
		if !p.Timestamp.Before(start) && !p.Timestamp.After(end) {
			points = append(points, p)
			if o.InputWatermark == nil || p.Timestamp.After(*o.InputWatermark) {
				stamp := p.Timestamp.UTC()
				o.InputWatermark = &stamp
			}
		}
	}
	data.Points = points
	o.Resolution = data.Resolution
	content, err := telemetryCSV(data)
	if err != nil {
		o.Status = "insufficient"
		o.ReasonCode = "insufficient_telemetry"
		return o, nil
	}
	digest := sha256.Sum256([]byte(content))
	o.InputSHA256 = hex.EncodeToString(digest[:])
	result, err := client.Upload(ctx, "telemetry.csv", strings.NewReader(content))
	if err != nil {
		o.ReasonCode = "ml_failure"
		switch {
		case ctx.Err() != nil:
			o.ReasonCode = "run_timeout"
		case strings.Contains(err.Error(), "unsupported_feature_recipe"):
			o.ReasonCode = "unsupported_feature_recipe"
		case strings.Contains(err.Error(), "unsupported_context"):
			o.ReasonCode = "unsupported_context"
		}
		return o, nil
	}
	if result.Provenance != nil && result.Provenance.InputSHA256 != nil && strings.TrimPrefix(*result.Provenance.InputSHA256, "sha256:") != o.InputSHA256 {
		o.ReasonCode = "input_digest_mismatch"
		return o, nil
	}
	o.Provenance = result.Provenance
	o.DecisionRule = result.DecisionRule
	o.Diagnostics = &domain.CohortDiagnostics{Verdict: result.Verdict, NWindows: result.NWindows, WindowsRunning: result.WindowsRunning, ChannelsUsed: result.ChannelsUsed, FeaturesUnavailable: result.FeaturesUnavailable}
	identity := &cohortModelIdentity{}
	if result.ModelVersion != nil {
		identity.version = *result.ModelVersion
	}
	if result.DecisionRule != nil {
		identity.rule = result.DecisionRule.RuleSHA256
	}
	switch result.Verdict {
	case "insufficient":
		o.Status = "insufficient"
		o.ReasonCode = "insufficient_running_windows"
	case "norm", "watch", "alert":
		o.Status = "scored"
		o.ReasonCode = "evaluated"
		o.Result = &result
	default:
		o.ReasonCode = "unsupported_verdict"
	}
	return o, identity
}

func (m *ML) WithCohort(scope FundScope, source CohortTelemetry, store CohortSnapshotStore) *ML {
	m.scope = scope
	m.telemetry = source
	m.snapshots = store
	return m
}
func (m *ML) IsCohort() bool { return m.scope != nil }
func (m *ML) CohortWells(ctx context.Context, run *int64) (domain.CohortSnapshot, error) {
	cohort, wells, err := m.scope.Members(ctx)
	if err != nil {
		return domain.CohortSnapshot{}, err
	}
	out, err := m.snapshots.CohortSnapshot(ctx, cohort.Version, run)
	if err != nil {
		if de, ok := err.(*domain.Error); !ok || de.Kind != domain.Missing || run != nil {
			return out, err
		}
		out = domain.CohortSnapshot{Profile: "cohort", CohortVersion: cohort.Version, ProvenanceStatus: "historical_unknown", OutcomeCompleteness: "not_evaluated", Outcomes: []domain.CohortOutcome{}}
		for _, w := range wells {
			out.Outcomes = append(out.Outcomes, domain.CohortOutcome{AppWellID: w.ID, Metadata: w, Status: "insufficient", ReasonCode: "not_evaluated"})
		}
	}
	visible := map[int64]bool{}
	for _, w := range wells {
		if domain.CanReadWell(ctx, w) {
			visible[w.ID] = true
		}
	}
	rows := make([]domain.CohortOutcome, 0, len(out.Outcomes))
	for _, o := range out.Outcomes {
		if visible[o.AppWellID] {
			rows = append(rows, o)
		}
	}
	out.Outcomes = rows
	if out.RunID != nil {
		alerts, e := m.snapshots.CohortAlerts(ctx, *out.RunID)
		if e != nil {
			return domain.CohortSnapshot{}, e
		}
		byWell := map[int64]domain.CohortAlert{}
		for _, a := range alerts {
			byWell[a.AppWellID] = a
		}
		for i := range out.Outcomes {
			if a, ok := byWell[out.Outcomes[i].AppWellID]; ok {
				out.Outcomes[i].AlertID = &a.ID
				out.Outcomes[i].AlertStatus = &a.Status
			}
		}
	}
	out.ManifestSHA256 = ""
	out.Recount()
	out.Running = false
	if len(out.Outcomes) > 0 {
		attempts, e := m.snapshots.CohortRuns(ctx, cohort.Version, 1)
		if e != nil {
			return out, e
		}
		out.Running = len(attempts) > 0 && attempts[0].Status == "running"
	}
	return out, nil
}
func (m *ML) CohortWell(ctx context.Context, id int64, run *int64) (domain.CohortOutcome, error) {
	if _, err := m.scope.RequireWell(ctx, id); err != nil {
		return domain.CohortOutcome{}, err
	}
	out, err := m.CohortWells(ctx, run)
	if err != nil {
		return domain.CohortOutcome{}, err
	}
	for _, o := range out.Outcomes {
		if o.AppWellID == id {
			o.RunID = out.RunID
			o.SnapshotID = out.SnapshotID
			o.AsOf = out.AsOf
			o.CohortVersion = out.CohortVersion
			return o, nil
		}
	}
	return domain.CohortOutcome{}, domain.NewError(domain.Missing, "Результат не найден")
}

func (m *ML) cohortRuns(ctx context.Context, count int) (domain.RunList, error) {
	cohort, members, err := m.scope.Members(ctx)
	if err != nil {
		return domain.RunList{}, err
	}
	visible := false
	for _, w := range members {
		if domain.CanReadWell(ctx, w) {
			visible = true
			break
		}
	}
	out := domain.RunList{Runs: []domain.MLRun{}}
	if !visible {
		return out, nil
	}
	rows, err := m.snapshots.CohortRuns(ctx, cohort.Version, count)
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		// Counts и diagnostic fields из ml_run никогда не раскрывают полный закрытый фонд.
		r.WellsScored = nil
		r.AlertsCount = nil
		r.DriftVerdict = nil
		r.DriftPSIMax = nil
		r.DriftTop = nil
		r.Error = nil
		r.FailureReasonCode = nil
		r.Provenance = nil
		if r.Status == "running" {
			out.Running = true
		}
		if r.Status == "error" {
			safe := "Расчёт завершился ошибкой. Повторите попытку или обратитесь к администратору."
			code := "run_failed"
			r.Error = &safe
			r.FailureReasonCode = &code
		}
		if r.SnapshotID != nil {
			snapshot, e := m.CohortWells(ctx, &r.ID)
			if e != nil {
				return out, e
			}
			scored := int64(snapshot.Coverage.ScoredCount)
			alerts := int64(0)
			for _, o := range snapshot.Outcomes {
				if o.Result != nil && (o.Result.Verdict == "alert" || o.Result.Verdict == "watch") {
					alerts++
				}
			}
			r.ProvenanceStatus = &snapshot.ProvenanceStatus
			r.WellsScored = &scored
			r.AlertsCount = &alerts
		}
		out.Runs = append(out.Runs, r)
	}
	return out, nil
}

func (m *ML) CohortByUnitek(ctx context.Context, id int64, run *int64) (domain.CohortOutcome, error) {
	w, err := m.scope.RequireUnitek(ctx, id)
	if err != nil {
		return domain.CohortOutcome{}, err
	}
	return m.CohortWell(ctx, w.ID, run)
}

func (m *ML) CohortWellsFiltered(ctx context.Context, min *float64, alertOnly bool) (domain.CohortRiskList, error) {
	s, err := m.CohortWells(ctx, nil)
	out := domain.CohortRiskList{CohortSnapshot: s, Scored: []domain.CohortOutcome{}}
	if err != nil {
		return out, err
	}
	for _, o := range s.Outcomes {
		if o.Status != "scored" || o.Result == nil {
			continue
		}
		if min != nil && o.Result.LatestRisk < *min {
			continue
		}
		if alertOnly && o.Result.Verdict != "watch" && o.Result.Verdict != "alert" {
			continue
		}
		out.Scored = append(out.Scored, o)
	}
	return out, nil
}
