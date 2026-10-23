package service

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/observability"
)

type TelemetryStore interface {
	Sources(context.Context) ([]domain.TelemetrySource, error)
	Source(context.Context, string) (domain.TelemetrySource, error)
	MutateSource(context.Context, domain.SourceMutation) (domain.TelemetrySource, error)
	ClaimSource(context.Context, string, bool) (*domain.TelemetrySourceLease, error)
	CommitTelemetry(context.Context, domain.TelemetryWrite) (domain.CollectionLog, error)
	FailCollection(context.Context, domain.TelemetrySourceLease, string) error
	Telemetry(context.Context, string, time.Time, time.Time) (domain.TelemetryResult, error)
	CollectionLogs(context.Context) ([]domain.CollectionLog, error)
}
type TelemetryConnector interface {
	Fetch(context.Context, domain.TelemetrySource, string) ([]map[string]any, error)
}
type SecretCodec interface {
	Seal(string) (string, error)
	Open(string) (string, error)
}
type Integrations struct {
	scope     FundScope
	store     TelemetryStore
	connector TelemetryConnector
	secrets   SecretCodec
	scorer    TelemetryScorer
	archive   TelemetryArchive
}
type TelemetryArchive interface {
	Read(context.Context, string, time.Time, time.Time) (domain.TelemetryResult, error)
	Bounds(context.Context, string) (domain.TelemetryBounds, error)
}

func (s *Integrations) WithScope(scope FundScope) *Integrations      { s.scope = scope; return s }
func (s *Integrations) WithArchive(a TelemetryArchive) *Integrations { s.archive = a; return s }
func (s *Integrations) Bounds(ctx context.Context, well string) (domain.TelemetryBounds, error) {
	if err := requireDataPermission(ctx, "telemetry.compat.read"); err != nil {
		return domain.TelemetryBounds{}, err
	}
	return s.bounds(ctx, well)
}
func (s *Integrations) bounds(ctx context.Context, well string) (domain.TelemetryBounds, error) {
	if err := s.requireTelemetry(ctx, well); err != nil {
		return domain.TelemetryBounds{}, err
	}
	if well == "" {
		return domain.TelemetryBounds{}, domain.NewError(domain.Invalid, "Укажите скважину")
	}
	local, err := s.storedBounds(ctx, well)
	if err != nil {
		return local, err
	}
	if s.archive == nil {
		return local, nil
	}
	remote, err := s.archive.Bounds(ctx, well)
	if err != nil {
		if local.Start != nil {
			return local, nil
		}
		return local, err
	}
	if remote.Start != nil && (local.Start == nil || remote.Start.Before(*local.Start)) {
		local.Start = remote.Start
	}
	if remote.End != nil && (local.End == nil || remote.End.After(*local.End)) {
		local.End = remote.End
	}
	return local, nil
}

type TelemetryScorer interface {
	Upload(context.Context, string, io.Reader) (domain.UploadResult, error)
}

func NewIntegrations(store TelemetryStore, connector TelemetryConnector, secrets SecretCodec, scorers ...TelemetryScorer) *Integrations {
	s := &Integrations{store: store, connector: connector, secrets: secrets}
	if len(scorers) > 0 {
		s.scorer = scorers[0]
	}
	return s
}

// Score передаёт выбранную историю существующему ML-контракту проверки файлов.
func (s *Integrations) Score(ctx context.Context, well string, start, end time.Time) (domain.UploadResult, error) {
	if err := requireDataPermission(ctx, "telemetry.compat.score"); err != nil {
		return domain.UploadResult{}, err
	}
	if s.scorer == nil {
		return domain.UploadResult{}, domain.NewError(domain.Unavailable, "ML-сервис не подключён")
	}
	data, err := s.Read(ctx, well, start, end)
	if err != nil {
		return domain.UploadResult{}, err
	}
	return s.scoreData(ctx, data)
}
func (s *Integrations) scoreData(ctx context.Context, data domain.TelemetryResult) (domain.UploadResult, error) {
	if s.scorer == nil {
		return domain.UploadResult{}, domain.NewError(domain.Unavailable, "ML-сервис не подключён")
	}
	content, err := telemetryCSV(data)
	if err != nil {
		return domain.UploadResult{}, err
	}
	return s.scorer.Upload(ctx, "telemetry.csv", strings.NewReader(content))
}
func telemetryCSV(data domain.TelemetryResult) (string, error) {
	var output strings.Builder
	if err := writeTelemetryCSV(&output, data); err != nil {
		return "", err
	}
	return output.String(), nil
}
func writeTelemetryCSV(output io.Writer, data domain.TelemetryResult) error {
	if len(data.Points) == 0 || data.Truncated {
		return domain.NewError(domain.Invalid, "Для скоринга нужна непустая полная выборка. Уменьшите период при достижении лимита.")
	}
	var err error
	keys := map[string]bool{}
	for _, p := range data.Points {
		for k := range p.Values {
			keys[k] = true
		}
	}
	columns := make([]string, 0, len(keys))
	for k := range keys {
		columns = append(columns, k)
	}
	sort.Strings(columns)
	writer := csv.NewWriter(output)
	if err = writer.Write(append([]string{"timestamp"}, columns...)); err != nil {
		return err
	}
	for _, p := range data.Points {
		row := []string{p.Timestamp.UTC().Format(time.RFC3339Nano)}
		for _, k := range columns {
			v := ""
			if p.Values[k] != nil {
				v = strconv.FormatFloat(*p.Values[k], 'g', -1, 64)
			}
			row = append(row, v)
		}
		if err = writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		return err
	}
	return nil
}
func (s *Integrations) List(ctx context.Context) ([]domain.TelemetrySource, error) {
	if err := requireDataPermission(ctx, "integrations.read"); err != nil {
		return nil, err
	}
	if domain.RBACEnforced(ctx) {
		if err := requireFullApplicationScope(ctx, "integrations.read"); err != nil {
			return []domain.TelemetrySource{}, nil
		}
	}
	return s.store.Sources(ctx)
}
func (s *Integrations) Logs(ctx context.Context) ([]domain.CollectionLog, error) {
	if err := requireDataPermission(ctx, "integrations.logs.read"); err != nil {
		return nil, err
	}
	if domain.RBACEnforced(ctx) {
		if err := requireFullApplicationScope(ctx, "integrations.logs.read"); err != nil {
			return []domain.CollectionLog{}, nil
		}
	}
	if s.scope != nil {
		return []domain.CollectionLog{}, nil
	}
	return s.store.CollectionLogs(ctx)
}
func (s *Integrations) Read(ctx context.Context, well string, start, end time.Time) (domain.TelemetryResult, error) {
	if err := requireDataPermission(ctx, "telemetry.compat.read"); err != nil {
		return domain.TelemetryResult{}, err
	}
	return s.read(ctx, well, start, end)
}
func (s *Integrations) read(ctx context.Context, well string, start, end time.Time) (domain.TelemetryResult, error) {
	if err := s.requireTelemetry(ctx, well); err != nil {
		return domain.TelemetryResult{}, err
	}
	if well == "" || !start.Before(end) || end.Sub(start) > 366*24*time.Hour {
		return domain.TelemetryResult{}, domain.NewError(domain.Invalid, "Укажите скважину и период до 366 дней")
	}
	local, err := s.store.Telemetry(ctx, well, start, end)
	if err != nil || s.archive == nil {
		return local, err
	}
	remote, err := s.archive.Read(ctx, well, start, end)
	if err != nil {
		return domain.TelemetryResult{}, err
	}
	// Импортированная точка имеет приоритет над архивной при точном совпадении времени.
	byTime := map[int64]domain.Measurement{}
	for _, p := range remote.Points {
		byTime[p.Timestamp.UnixNano()] = p
	}
	for _, p := range local.Points {
		byTime[p.Timestamp.UnixNano()] = p
	}
	points := make([]domain.Measurement, 0, len(byTime))
	for _, p := range byTime {
		points = append(points, p)
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Timestamp.Before(points[j].Timestamp) })
	truncated := remote.Truncated || local.Truncated || len(points) > 10000
	if len(points) > 10000 {
		points = points[:10000]
	}
	return domain.TelemetryResult{Points: points, Truncated: truncated, Resolution: remote.Resolution}, nil
}
func validateSource(in domain.TelemetrySource) error { return domain.ValidateTelemetrySource(in) }

func (s *Integrations) Save(ctx context.Context, id string, in domain.SourceInput) (domain.TelemetrySource, error) {
	permission := domain.Permission("integrations.update")
	if id == "" {
		permission = "integrations.create"
	}
	if err := requireFullApplicationScope(ctx, permission); err != nil {
		return domain.TelemetrySource{}, err
	}
	if err := validateSource(in.TelemetrySource); err != nil {
		return domain.TelemetrySource{}, err
	}
	out := in.TelemetrySource
	out.Status = "draft"
	out.LastReceived = nil
	out.LastMeasured = nil
	out.Error = ""
	out.EncryptedSecret = ""
	kind := domain.SourceUpdate
	if id != "" {
		out.ID = id
	} else {
		kind = domain.SourceCreate
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return out, err
		}
		out.ID = hex.EncodeToString(raw[:])
	}

	if in.Secret != nil {
		encrypted, err := s.secrets.Seal(*in.Secret)
		if err != nil {
			return out, err
		}
		out.EncryptedSecret = encrypted
	}
	return s.store.MutateSource(ctx, domain.SourceMutation{Kind: kind, Source: out, ReplaceSecret: in.Secret != nil})
}
func (s *Integrations) Status(ctx context.Context, id, status string) (domain.TelemetrySource, error) {
	permission := domain.Permission("integrations.status")
	if err := requireFullApplicationScope(ctx, permission); err != nil {
		return domain.TelemetrySource{}, err
	}
	if status != "running" && status != "paused" {
		return domain.TelemetrySource{}, domain.NewError(domain.Invalid, "Допустимы running и paused")
	}
	return s.store.MutateSource(ctx, domain.SourceMutation{Kind: domain.SourceStatus, Source: domain.TelemetrySource{ID: id, Status: status}})
}

func (s *Integrations) Check(ctx context.Context, in domain.SourceInput) ([]map[string]any, error) {
	permission := domain.Permission("integrations.check")
	if err := requireFullApplicationScope(ctx, permission); err != nil {
		return nil, err
	}
	if err := validateSource(in.TelemetrySource); err != nil {
		return nil, err
	}
	if in.Kind == "json_push" {
		return []map[string]any{}, nil
	}
	secret := ""
	if in.Secret != nil {
		secret = *in.Secret
	} else if in.ID != "" {
		old, err := s.store.Source(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		secret, err = s.secrets.Open(old.EncryptedSecret)
		if err != nil {
			return nil, err
		}
	}
	records, err := s.connector.Fetch(ctx, in.TelemetrySource, secret)
	if err != nil {
		return nil, err
	}
	if s.scope != nil {
		if err = s.requireSourceRecords(ctx, in.TelemetrySource, records); err != nil {
			return nil, err
		}
		points, _ := MapTelemetry(records, in.TelemetrySource)
		if _, err = s.resolveSourcePoints(ctx, in.TelemetrySource, points); err != nil {
			return nil, err
		}
		return []map[string]any{}, nil
	}
	return records, nil
}
func atPath(v any, path string) any {
	for _, p := range strings.Split(path, ".") {
		if p == "" {
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[p]
	}
	return v
}
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		n, e := x.Float64()
		return n, e == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
	case float64:
		return x, !math.IsNaN(x) && !math.IsInf(x, 0)
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case int16:
		return float64(x), true
	case float32:
		return float64(x), !math.IsNaN(float64(x)) && !math.IsInf(float64(x), 0)
	case int:
		return float64(x), true
	case string:
		if strings.TrimSpace(x) == "" {
			return 0, false
		}
		n, e := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(x), ",", "."), 64)
		return n, e == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
	default:
		return 0, false
	}
}
func cleanValue(v any, m domain.Mapping) *float64 {
	n, ok := number(v)
	if !ok {
		return nil
	}
	n, ok = cleanTelemetryScalar(n, m)
	if !ok {
		return nil
	}
	return &n
}

// cleanTelemetryScalar чистит маркеры до scale/offset без boxed values и указателей.
func cleanTelemetryScalar(n float64, m domain.Mapping) (float64, bool) {
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, false
	}
	if m.Parameter == "R_izol" && math.Abs(n-9999) <= 1e-6 {
		return 0, false
	}
	if strings.HasPrefix(m.Parameter, "energy_Input_energy_reactive_total_") && (math.Abs(n-9999) <= 1e-6 || math.Abs(n-999.9) <= 1e-6) {
		return 0, false
	}
	for marker := range strings.FieldsFuncSeq(m.Missing, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		if x, e := strconv.ParseFloat(marker, 64); e == nil && math.Abs(x-n) <= 1e-6 {
			return 0, false
		}
	}
	n = n*m.Scale + m.Offset
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, false
	}
	return n, true
}

func telemetryWellID(value any) string {
	switch v := value.(type) {
	case json.Number:
		return string(v)
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(v, 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int:
		return strconv.Itoa(v)
	}
	return ""
}

// MapTelemetry чистит маркеры до преобразования и отделяет уставки от измерений.
func MapTelemetry(records []map[string]any, source domain.TelemetrySource) ([]domain.Measurement, int) {
	return MapTelemetryAt(records, source, time.Now())
}

// MapTelemetryAt использует зафиксированное время валидации сохранённого входа.
func MapTelemetryAt(records []map[string]any, source domain.TelemetrySource, validationAsOf time.Time) ([]domain.Measurement, int) {
	points := make([]domain.Measurement, 0, len(records))
	rejected := 0
	for _, record := range records {
		wellID := telemetryWellID(atPath(record, source.WellField))
		var ts time.Time
		switch v := atPath(record, source.TimeField).(type) {
		case string:
			ts, _ = time.Parse(time.RFC3339Nano, v)
		case time.Time:
			ts = v
		}
		if wellID == "" || len(wellID) > 200 || ts.IsZero() || ts.After(validationAsOf.Add(time.Hour)) {
			rejected++
			continue
		}
		values := make(map[string]*float64)
		for _, m := range source.Mappings {
			if !m.IsSet {
				values[m.Parameter] = cleanValue(atPath(record, m.Field), m)
			}
		}
		if len(values) == 0 {
			rejected++
			continue
		}
		points = append(points, domain.Measurement{WellID: wellID, Timestamp: ts.UTC(), Values: values})
	}
	return points, rejected
}
func (s *Integrations) Ingest(ctx context.Context, id string, records []map[string]any) (domain.CollectionLog, error) {
	if err := requireDataPermission(ctx, "integrations.ingest"); err != nil {
		return domain.CollectionLog{}, err
	}
	source, err := s.store.Source(ctx, id)
	if err != nil {
		return domain.CollectionLog{}, err
	}
	if source.Kind != "json_push" || source.Status != "running" {
		return domain.CollectionLog{}, domain.NewError(domain.Invalid, "Приём доступен для запущенного JSON-источника")
	}
	if err := s.requireSourceRecords(ctx, source, records); err != nil {
		return domain.CollectionLog{}, err
	}
	points, rejected := MapTelemetry(records, source)
	points, err = s.resolveSourcePoints(ctx, source, points)
	if err != nil {
		return domain.CollectionLog{}, err
	}
	return s.store.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: id, ProfileVersion: source.ProfileVersion, Points: points, Rejected: rejected, Message: "Пакет JSON обработан"})
}
func (s *Integrations) Import(ctx context.Context, points []domain.Measurement) (domain.CollectionLog, error) {
	if err := requireDataPermission(ctx, "telemetry.import"); err != nil {
		return domain.CollectionLog{}, err
	}
	if len(points) == 0 || len(points) > 100000 {
		return domain.CollectionLog{}, domain.NewError(domain.Invalid, "Нужен пакет от 1 до 100000 записей")
	}
	ids := make([]string, 0, len(points))
	for _, p := range points {
		ids = append(ids, p.WellID)
	}
	if err := s.requireTelemetrySet(ctx, ids); err != nil {
		return domain.CollectionLog{}, err
	}
	for _, p := range points {
		if p.WellID == "" || len(p.WellID) > 200 || p.Timestamp.IsZero() || p.Timestamp.After(time.Now().Add(time.Hour)) || len(p.Values) == 0 || len(p.Values) > 100 {
			return domain.CollectionLog{}, domain.NewError(domain.Invalid, "Некорректная запись телеметрии")
		}
		for key, v := range p.Values {
			if strings.HasPrefix(key, "set_") {
				return domain.CollectionLog{}, domain.NewError(domain.Invalid, "Уставки нельзя импортировать как измерения")
			}
			if v != nil {
				n := cleanValue(*v, domain.Mapping{Parameter: key, Scale: 1})
				p.Values[key] = n
			}
		}
	}
	return s.store.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: "manual", Points: points, Message: "Файл импортирован; совпадения сохранены без перезаписи"})
}
func (s *Integrations) Collect(ctx context.Context, id string, due bool) (out domain.CollectionLog, err error) {
	permission := domain.Permission("integrations.collect")
	if err := requireFullApplicationScope(ctx, permission); err != nil {
		return domain.CollectionLog{}, err
	}
	ctx, finish := observability.Operation(ctx, "telemetry.collect")
	defer func() { finish(err) }()
	claimed, err := s.store.ClaimSource(ctx, id, due)
	if err != nil {
		return domain.CollectionLog{}, err
	}
	if claimed == nil {
		return domain.CollectionLog{}, domain.NewError(domain.Duplicate, "Источник занят, приостановлен или ещё не наступил срок сбора")
	}
	source, err := s.store.Source(ctx, id)
	if err == nil && source.ProfileVersion != claimed.ProfileVersion {
		return domain.CollectionLog{}, domain.NewError(domain.Duplicate, "stale_lease")
	}
	var records []map[string]any
	var secret string
	if err == nil {
		secret, err = s.secrets.Open(source.EncryptedSecret)
	}
	if err == nil {
		if gate, ok := s.store.(interface {
			AuthorizeTelemetrySourceLease(context.Context, domain.TelemetrySourceLease) error
		}); ok {
			err = gate.AuthorizeTelemetrySourceLease(ctx, *claimed)
		} else {
			err = domain.NewError(domain.Unavailable, "current_authority_store_required")
		}
		if err == nil {
			records, err = s.connector.Fetch(ctx, source, secret)
		}
	}
	if err != nil {
		slog.WarnContext(ctx, "telemetry collection failed", "error_code", "operation_failed")
		failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if e := s.store.FailCollection(failCtx, *claimed, "Ошибка подключения. Проверьте настройки и доступность источника."); e != nil {
			slog.ErrorContext(ctx, "record collection failure", "error_code", "operation_failed")
		}
		return domain.CollectionLog{}, domain.NewError(domain.Unavailable, "Не удалось получить данные источника")
	}
	if err = s.requireSourceRecords(ctx, source, records); err != nil {
		failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = s.store.FailCollection(failCtx, *claimed, "Пакет содержит недоступные скважины")
		return domain.CollectionLog{}, err
	}
	points, rejected := MapTelemetry(records, source)
	points, err = s.resolveSourcePoints(ctx, source, points)
	if err != nil {
		failCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if failure := s.store.FailCollection(failCtx, *claimed, "Проверьте тип ID и связи скважин со справочником Pulse"); failure != nil {
			slog.ErrorContext(ctx, "record mapping failure", "error_code", "operation_failed")
		}
		return domain.CollectionLog{}, err
	}
	out, err = s.store.CommitTelemetry(ctx, domain.TelemetryWrite{SourceID: id, ProfileVersion: source.ProfileVersion, Lease: claimed, Points: points, Rejected: rejected, Message: "Цикл сбора завершён", Release: true})
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = s.store.FailCollection(cleanup, *claimed, "Сбор не опубликован")
	}
	return out, err
}
func (s *Integrations) Schedule(ctx context.Context) {
	ctx = domain.WithSchedulerPrincipal(ctx, domain.ExecutionSystemCollect, domain.AccessScope{Funds: []string{"operational", "presentation"}})
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sources, err := s.store.Sources(ctx)
			if err != nil {
				continue
			}
			for _, source := range sources {
				if (source.Status != "running" && source.Status != "error") || source.Kind == "json_push" {
					continue
				}
				run, cancel := context.WithTimeout(ctx, 30*time.Second)
				_, _ = s.Collect(run, source.ID, true)
				cancel()
			}
		}
	}
}

// PulseTelemetryID отделяет внутреннюю адресацию от числовых ID старых источников.
func PulseTelemetryID(id int64) string { return "pulse:" + strconv.FormatInt(id, 10) }

func (s *Integrations) ReadApp(ctx context.Context, well domain.Well, start, end time.Time) (domain.TelemetryResult, error) {
	current, authorizationErr := s.currentTelemetryWell(ctx, well)
	if authorizationErr != nil {
		return domain.TelemetryResult{}, authorizationErr
	}
	well = current
	if err := scopedWell(ctx, s.scope, well.ID); err != nil {
		return domain.TelemetryResult{}, err
	}
	if !start.Before(end) || end.Sub(start) > 366*24*time.Hour {
		return domain.TelemetryResult{}, domain.NewError(domain.Invalid, "Укажите период до 366 дней")
	}
	local, err := s.store.Telemetry(ctx, PulseTelemetryID(well.ID), start, end)
	if err != nil {
		return local, err
	}
	var legacy domain.TelemetryResult
	if well.UnitekID != nil {
		legacy, err = s.Read(ctx, strconv.FormatInt(*well.UnitekID, 10), start, end)
		if err != nil {
			return legacy, err
		}
	}
	byTime := map[int64]domain.Measurement{}
	for _, p := range legacy.Points {
		byTime[p.Timestamp.UnixNano()] = p
	}
	for _, p := range local.Points {
		byTime[p.Timestamp.UnixNano()] = p
	}
	points := make([]domain.Measurement, 0, len(byTime))
	for _, p := range byTime {
		p.WellID = strconv.FormatInt(well.ID, 10)
		points = append(points, p)
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Timestamp.Before(points[j].Timestamp) })
	truncated := local.Truncated || legacy.Truncated || len(points) > 10000
	if len(points) > 10000 {
		points = points[:10000]
	}
	resolution := legacy.Resolution
	if resolution == "" {
		resolution = local.Resolution
	}
	return domain.TelemetryResult{Points: points, Truncated: truncated, Resolution: resolution}, nil
}
func (s *Integrations) BoundsApp(ctx context.Context, well domain.Well) (domain.TelemetryBounds, error) {
	current, authorizationErr := s.currentTelemetryWell(ctx, well)
	if authorizationErr != nil {
		return domain.TelemetryBounds{}, authorizationErr
	}
	well = current
	if err := scopedWell(ctx, s.scope, well.ID); err != nil {
		return domain.TelemetryBounds{}, err
	}
	local, err := s.storedBounds(ctx, PulseTelemetryID(well.ID))
	if err != nil {
		return local, err
	}
	if well.UnitekID == nil {
		return local, nil
	}
	legacy, err := s.bounds(ctx, strconv.FormatInt(*well.UnitekID, 10))
	if err != nil {
		return local, err
	}
	if legacy.Start != nil && (local.Start == nil || legacy.Start.Before(*local.Start)) {
		local.Start = legacy.Start
	}
	if legacy.End != nil && (local.End == nil || legacy.End.After(*local.End)) {
		local.End = legacy.End
	}
	return local, nil
}
func (s *Integrations) ScoreApp(ctx context.Context, well domain.Well, start, end time.Time) (domain.UploadResult, error) {
	if err := requireDataPermission(ctx, "checks.run"); err != nil {
		return domain.UploadResult{}, err
	}
	if err := requireWellValue(ctx, "telemetry.read", well); err != nil {
		return domain.UploadResult{}, err
	}
	data, err := s.ReadApp(ctx, well, start, end)
	if err != nil {
		return domain.UploadResult{}, err
	}
	return s.scoreData(ctx, data)
}

// resolveSourcePoints сохраняет новые источники по ID Pulse; старые профили читаются совместимо.
func (s *Integrations) resolveSourcePoints(ctx context.Context, source domain.TelemetrySource, points []domain.Measurement) ([]domain.Measurement, error) {
	if len(points) == 0 {
		return points, nil
	}
	if source.WellIdentity == "" {
		ids := make([]string, 0, len(points))
		for _, p := range points {
			ids = append(ids, p.WellID)
		}
		if err := s.requireTelemetrySet(ctx, ids); err != nil {
			return nil, err
		}
		return points, nil
	}
	catalog, ok := s.store.(interface {
		Wells(context.Context, bool) ([]domain.Well, error)
	})
	if !ok {
		return nil, domain.NewError(domain.Unavailable, "Справочник скважин не подключён")
	}
	var wells []domain.Well
	var err error
	if s.scope != nil {
		_, wells, err = s.scope.Members(ctx)
	} else {
		wells, err = catalog.Wells(ctx, false)
	}
	if err != nil {
		return nil, err
	}
	ids := map[string]int64{}
	for _, w := range wells {
		if !domain.CanReadWell(ctx, w) {
			continue
		}
		var external *int64
		switch source.WellIdentity {
		case "pulse":
			external = &w.ID
		case "unitek":
			external = w.UnitekID
		case "armits":
			external = w.ArmitsID
		}
		if external != nil {
			ids[strconv.FormatInt(*external, 10)] = w.ID
		}
	}
	out := make([]domain.Measurement, len(points))
	for i, p := range points {
		id, ok := ids[p.WellID]
		if !ok {
			return nil, domain.NewError(domain.Missing, "Скважина не найдена")
		}
		p.WellID = PulseTelemetryID(id)
		out[i] = p
	}
	return out, nil
}

func (s *Integrations) storedBounds(ctx context.Context, well string) (domain.TelemetryBounds, error) {
	var local domain.TelemetryBounds
	if store, ok := s.store.(interface {
		TelemetryBounds(context.Context, string) (domain.TelemetryBounds, error)
	}); ok {
		var err error
		local, err = store.TelemetryBounds(ctx, well)
		if err != nil {
			return local, err
		}
	}
	return local, nil
}

func (s *Integrations) requireTelemetry(ctx context.Context, raw string) error {
	if domain.RBACEnforced(ctx) {
		if err := requireDataPermission(ctx, "telemetry.read"); err != nil {
			return err
		}
		catalog, ok := s.store.(interface {
			Wells(context.Context, bool) ([]domain.Well, error)
		})
		if !ok {
			return domain.NewError(domain.Unavailable, "Справочник скважин не подключён")
		}
		wells, err := catalog.Wells(ctx, false)
		if err != nil {
			return err
		}
		pulse := strings.HasPrefix(raw, "pulse:")
		id, err := strconv.ParseInt(strings.TrimPrefix(raw, "pulse:"), 10, 64)
		if err != nil {
			return domain.NewError(domain.Missing, "Скважина не найдена")
		}
		for _, w := range wells {
			if (pulse && w.ID == id || !pulse && w.UnitekID != nil && *w.UnitekID == id) && domain.CanReadWell(ctx, w) {
				if s.scope != nil {
					_, err := s.scope.RequireWell(ctx, w.ID)
					return err
				}
				return nil
			}
		}
		return domain.NewError(domain.Missing, "Скважина не найдена")
	}
	if s.scope == nil {
		return nil
	}
	if strings.HasPrefix(raw, "pulse:") {
		id, err := strconv.ParseInt(strings.TrimPrefix(raw, "pulse:"), 10, 64)
		if err == nil {
			_, err = s.scope.RequireWell(ctx, id)
		}
		if err != nil {
			return domain.NewError(domain.Missing, "Скважина не найдена")
		}
		return nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return domain.NewError(domain.Missing, "Скважина не найдена")
	}
	_, err = s.scope.RequireUnitek(ctx, id)
	return err
}

// requireSourceRecords проверяет все ID до отбрасывания некорректных измерений.
func (s *Integrations) requireSourceRecords(ctx context.Context, source domain.TelemetrySource, records []map[string]any) error {
	if s.scope == nil && !domain.RBACEnforced(ctx) {
		return nil
	}
	points := make([]domain.Measurement, 0, len(records))
	for _, record := range records {
		points = append(points, domain.Measurement{WellID: telemetryWellID(atPath(record, source.WellField))})
	}
	_, err := s.resolveSourcePoints(ctx, source, points)
	return err
}
