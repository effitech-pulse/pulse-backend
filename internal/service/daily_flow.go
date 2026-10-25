package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

const DailyFlowJSONLimit = 128 * 1024
const DailyFlowXLSXLimit = 256 * 1024

type DailyFlowProvider interface {
	Profile() domain.DailyFlowProfile
	Read(context.Context, int64, string, string) ([]domain.DailyFlowSourceRow, error)
}
type DailyFlowWorkbookWriter interface {
	WriteDailyFlow(context.Context, domain.DailyFlowView) ([]byte, error)
}
type DailyFlow struct {
	catalog  *Catalog
	scope    FundScope
	provider DailyFlowProvider
	writer   DailyFlowWorkbookWriter
	mode     string
	exports  chan struct{}
}

func NewDailyFlow(c *Catalog, scope FundScope, p DailyFlowProvider, w DailyFlowWorkbookWriter, mode string) *DailyFlow {
	return &DailyFlow{catalog: c, scope: scope, provider: p, writer: w, mode: mode, exports: make(chan struct{}, 2)}
}
func dailyUnavailable() error { return domain.NewError(domain.Unavailable, "daily_flow_unavailable") }
func dailyChanged() error     { return domain.NewError(domain.Duplicate, "daily_flow_view_changed") }

// ValidateDailyFlowCohort связывает весь профиль с текущим read-only фондом.
func ValidateDailyFlowCohort(ctx context.Context, scope FundScope, p domain.DailyFlowProfile) error {
	if scope == nil {
		return dailyUnavailable()
	}
	c, wells, err := scope.Members(ctx)
	if err != nil {
		return dailyUnavailable()
	}
	if c.Version != p.CohortVersion || c.ManifestSHA256 != p.CohortManifestSHA256 || len(wells) != 100 || len(p.Members) != 100 {
		return dailyUnavailable()
	}
	members := map[int64]domain.DailyFlowMember{}
	for _, m := range p.Members {
		if _, ok := members[m.WellID]; ok {
			return dailyUnavailable()
		}
		members[m.WellID] = m
	}
	seen := map[int64]bool{}
	for _, w := range wells {
		m, ok := members[w.ID]
		if !ok || seen[w.ID] || !dailyBinding(w, m) {
			return dailyUnavailable()
		}
		seen[w.ID] = true
	}
	if ctx.Err() != nil {
		return dailyUnavailable()
	}
	return nil
}
func dailyBinding(w domain.Well, m domain.DailyFlowMember) bool {
	return w.UnitekID != nil && w.ArmitsID != nil && *w.UnitekID == m.UnitekID && *w.ArmitsID == m.ArmitsID
}
func (s *DailyFlow) Read(ctx context.Context, id int64, q domain.DailyFlowQuery) (domain.DailyFlowResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.read(ctx, id, q)
}
func (s *DailyFlow) read(ctx context.Context, id int64, q domain.DailyFlowQuery) (domain.DailyFlowResult, error) {
	fail := func(e error) (domain.DailyFlowResult, error) { return domain.DailyFlowResult{}, e }
	if s.catalog == nil {
		return fail(dailyUnavailable())
	}
	w, err := s.catalog.RequireWell(ctx, "telemetry.read", id)
	if err != nil {
		if ctx.Err() != nil {
			return fail(dailyUnavailable())
		}
		return fail(err)
	}
	if s.mode == "off" || s.mode == "" {
		return domain.DailyFlowResult{Enabled: false}, nil
	}
	if s.mode != "synthetic" || s.provider == nil {
		return fail(dailyUnavailable())
	}
	profile := s.provider.Profile()
	current, currentErr := s.catalog.store.Well(ctx, id)
	if currentErr != nil {
		return fail(dailyUnavailable())
	}
	var member domain.DailyFlowMember
	found := false
	for _, m := range profile.Members {
		if m.WellID == id {
			member = m
			found = true
			break
		}
	}
	if !found || !dailyBinding(w, member) || !dailyBinding(current, member) {
		return fail(dailyUnavailable())
	}
	if err = ValidateDailyFlowCohort(ctx, s.scope, profile); err != nil {
		return fail(err)
	}
	start, end, err := dailyInterval(q, profile)
	if err != nil {
		return fail(err)
	}
	if q.DatasetVersion != "" && q.DatasetVersion != profile.DatasetVersion {
		return fail(dailyChanged())
	}
	rows, err := s.provider.Read(ctx, id, start, end)
	if err != nil {
		return fail(dailyUnavailable())
	}
	view, err := dailyView(ctx, id, start, end, profile, member.ScenarioCode, rows)
	if err != nil {
		return fail(err)
	}
	if q.ViewID != "" && q.ViewID != view.ViewID {
		return fail(dailyChanged())
	}
	raw, err := json.Marshal(view)
	if err != nil || len(raw)+1 > DailyFlowJSONLimit || ctx.Err() != nil {
		return fail(dailyUnavailable())
	}
	return domain.DailyFlowResult{Enabled: true, View: &view}, nil
}
func dailyInterval(q domain.DailyFlowQuery, p domain.DailyFlowProfile) (string, string, error) {
	invalid := domain.NewError(domain.Invalid, "daily_flow_invalid_query")
	if (q.StartDate == "") != (q.EndDate == "") {
		return "", "", invalid
	}
	if q.StartDate == "" {
		end, e := time.Parse("2006-01-02", p.EndDate)
		if e != nil {
			return "", "", dailyUnavailable()
		}
		q.StartDate = end.AddDate(0, 0, -29).Format("2006-01-02")
		q.EndDate = p.EndDate
	}
	a, e := time.Parse("2006-01-02", q.StartDate)
	b, f := time.Parse("2006-01-02", q.EndDate)
	if e != nil || f != nil || a.Format("2006-01-02") != q.StartDate || b.Format("2006-01-02") != q.EndDate || b.Before(a) || b.Sub(a) > 89*24*time.Hour || q.StartDate < p.StartDate || q.EndDate > p.EndDate {
		return "", "", invalid
	}
	return q.StartDate, q.EndDate, nil
}
func dailyNumber(v *int64) *float64 {
	if v == nil {
		return nil
	}
	x := float64(*v) / 100
	return &x
}
func dailyView(ctx context.Context, id int64, start, end string, p domain.DailyFlowProfile, scenario string, input []domain.DailyFlowSourceRow) (domain.DailyFlowView, error) {
	view := domain.DailyFlowView{Enabled: true, SchemaVersion: "daily-flow-v1", WellID: strconv.FormatInt(id, 10), StartDate: start, EndDate: end, CalendarTimezone: "Europe/Moscow", Quantity: "liquid_rate", Unit: "m3/day", DatasetVersion: p.DatasetVersion, Source: domain.DailyFlowSource{Kind: "test_generated", Synthetic: true, Label: "Тестовые данные", ExpectedDefinition: "synthetic_reference", ScenarioCode: scenario, RecipeVersion: p.RecipeVersion, ManifestSHA256: p.ManifestSHA256, DatasetSHA256: p.DatasetSHA256}, Available: domain.DailyFlowRange{StartDate: p.StartDate, EndDate: p.EndDate}, Rows: []domain.DailyFlowRow{}}
	if len(input) > 90 {
		return view, dailyUnavailable()
	}
	byDate := map[string]domain.DailyFlowSourceRow{}
	for _, r := range input {
		if _, ok := byDate[r.Date]; ok || r.Date < start || r.Date > end {
			return view, dailyUnavailable()
		}
		date, e := time.Parse("2006-01-02", r.Date)
		if e != nil || date.Format("2006-01-02") != r.Date {
			return view, dailyUnavailable()
		}
		if r.RecordState != "present" && r.RecordState != "missing_day" {
			return view, dailyUnavailable()
		}
		if r.RecordState == "missing_day" && (r.ActualHundredths != nil || r.ExpectedHundredths != nil) {
			return view, dailyUnavailable()
		}
		for _, v := range []*int64{r.ActualHundredths, r.ExpectedHundredths} {
			if v != nil && (*v < 0 || *v > 30000) {
				return view, dailyUnavailable()
			}
		}
		byDate[r.Date] = r
	}
	a, _ := time.Parse("2006-01-02", start)
	canonicalRows := make([]domain.DailyFlowSourceRow, 0, 90)
	for date := a.Format("2006-01-02"); date <= end; {
		if ctx.Err() != nil {
			return view, dailyUnavailable()
		}
		r, ok := byDate[date]
		if !ok {
			r = domain.DailyFlowSourceRow{Date: date, RecordState: "missing_day"}
		}
		canonicalRows = append(canonicalRows, r)
		row := domain.DailyFlowRow{Date: date, Actual: dailyNumber(r.ActualHundredths), Expected: dailyNumber(r.ExpectedHundredths), RecordState: r.RecordState, ComparisonStatus: "missing_both"}
		c := &view.Coverage
		c.CalendarDays++
		if r.RecordState == "present" {
			c.RecordedDays++
		}
		if row.Actual != nil {
			c.ActualDays++
		}
		if row.Expected != nil {
			c.ExpectedDays++
		}
		switch {
		case row.Actual != nil && row.Expected != nil:
			c.PairedDays++
			delta := *r.ActualHundredths - *r.ExpectedHundredths
			row.Delta = dailyNumber(&delta)
			row.ComparisonStatus = "zero_expected"
			if *r.ExpectedHundredths > 0 {
				c.ComparablePercentDays++
				row.ComparisonStatus = "paired"
				num := delta * 10000
				den := *r.ExpectedHundredths
				sign := int64(1)
				if num < 0 {
					sign = -1
					num = -num
				}
				percent := sign * ((num + den/2) / den)
				row.DeltaPercent = dailyNumber(&percent)
			}
		case row.Actual == nil && row.Expected != nil:
			row.ComparisonStatus = "missing_actual"
		case row.Actual != nil && row.Expected == nil:
			row.ComparisonStatus = "missing_expected"
		}
		view.Rows = append(view.Rows, row)
		a = a.AddDate(0, 0, 1)
		date = a.Format("2006-01-02")
	}
	canonical := struct {
		Canonicalization string                      `json:"canonicalization"`
		Formula          string                      `json:"formula"`
		View             domain.DailyFlowView        `json:"view"`
		Rows             []domain.DailyFlowSourceRow `json:"source_rows"`
	}{"daily-flow-canonical-v1", "actual-minus-expected-v1", view, canonicalRows}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return view, dailyUnavailable()
	}
	sum := sha256.Sum256(raw)
	view.ViewID = hex.EncodeToString(sum[:])
	return view, nil
}
func (s *DailyFlow) Export(ctx context.Context, id int64, q domain.DailyFlowQuery) (domain.DailyFlowExport, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Admission includes authorization and encoding; no waiting queue.
	select {
	case s.exports <- struct{}{}:
		defer func() { <-s.exports }()
	default:
		return domain.DailyFlowExport{}, domain.NewError(domain.Limited, "daily_flow_busy")
	}
	result, err := s.read(ctx, id, q)
	if err != nil {
		return domain.DailyFlowExport{}, err
	}
	if !result.Enabled {
		return domain.DailyFlowExport{}, domain.NewError(domain.Unavailable, "daily_flow_disabled")
	}
	if q.StartDate == "" || q.EndDate == "" || q.DatasetVersion == "" || q.ViewID == "" {
		return domain.DailyFlowExport{}, domain.NewError(domain.Invalid, "daily_flow_invalid_query")
	}
	if s.writer == nil {
		return domain.DailyFlowExport{}, dailyUnavailable()
	}
	raw, err := s.writer.WriteDailyFlow(ctx, *result.View)
	if err != nil || ctx.Err() != nil || len(raw) > DailyFlowXLSXLimit {
		return domain.DailyFlowExport{}, dailyUnavailable()
	}
	return domain.DailyFlowExport{Filename: fmt.Sprintf("pulse-%d-daily-flow-%s-%s-TEST.xlsx", id, q.StartDate, q.EndDate), Bytes: raw}, nil
}
