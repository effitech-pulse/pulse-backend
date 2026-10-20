package service

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type FileInputStore interface {
	AcceptFileInput(context.Context, domain.PreparedFileInput) (domain.FileInputSummary, error)
	StoredFileInput(context.Context, string) (domain.PreparedFileInput, error)
	CommitStoredResult(context.Context, domain.StoredFileResult) (domain.StoredFileResult, error)
	FileInputRequest(context.Context, int64, string) (domain.FileInputSummary, error)
	FileInputSummary(context.Context, string) (domain.FileInputSummary, error)
	FileInputSummaries(context.Context, int64, string) ([]domain.FileInputSummary, error)
	StoredFileResult(context.Context, string) (domain.StoredFileResult, error)
}
type FileInputAccounts interface {
	Account(context.Context, int64) (domain.Account, error)
}
type FileInputReader interface {
	ParserSources() []domain.FileParserPart
	Read(context.Context, io.Reader, domain.FileParseSpec, domain.FileInputLimits) (domain.FileInputRows, error)
}
type PinnedTelemetryScorer interface {
	UploadPinned(context.Context, domain.ExecutionPin, domain.PinnedInputIdentity, io.Reader) (domain.UploadResult, error)
}
type FileChecks struct {
	store          FileInputStore
	accounts       FileInputAccounts
	scope          FundScope
	reader         FileInputReader
	scorer         PinnedTelemetryScorer
	schema         domain.TelemetrySchemaSnapshot
	pin            domain.ExecutionPin
	limits         domain.FileInputLimits
	implementation domain.FileParserImplementation
	configSHA      string
	normalize      chan struct{}
	execute        chan struct{}
}

func fileInvalid(s string) error { return domain.NewError(domain.Invalid, s) }
func fileMissing(err error) bool {
	var de *domain.Error
	return errors.As(err, &de) && de.Kind == domain.Missing
}
func NewFileChecks(store FileInputStore, accounts FileInputAccounts, scope FundScope, reader FileInputReader, scorer PinnedTelemetryScorer, schema domain.TelemetrySchemaSnapshot, pin domain.ExecutionPin, limits domain.FileInputLimits) (*FileChecks, error) {
	info, ok := fileParserActualBuildInfo()
	if !ok {
		return nil, domain.NewError(domain.Unavailable, "parser_build_identity_unavailable")
	}
	return NewFileChecksWithBuildInfo(store, accounts, scope, reader, scorer, schema, pin, limits, info)
}

// NewFileChecksWithBuildInfo — доверенный server dependency seam для явных fixtures.
// HTTP/env/client не выбирают BuildInfo; обычный constructor всегда читает binary metadata.
func NewFileChecksWithBuildInfo(store FileInputStore, accounts FileInputAccounts, scope FundScope, reader FileInputReader, scorer PinnedTelemetryScorer, schema domain.TelemetrySchemaSnapshot, pin domain.ExecutionPin, limits domain.FileInputLimits, info *debug.BuildInfo) (*FileChecks, error) {
	if store == nil || accounts == nil || scope == nil || reader == nil || scorer == nil || !limits.Valid() || len(schema.Channels) == 0 || len(schema.Bytes) == 0 || domain.FileSHA256(schema.Bytes) != schema.SHA256 {
		return nil, fileInvalid("invalid_file_checks_configuration")
	}
	if e := pin.Validate(); e != nil {
		return nil, e
	}
	// Копии изолируют snapshot от изменения caller после constructor.
	raw, err := json.Marshal(schema.Channels)
	if err != nil {
		return nil, err
	}
	var channels []domain.TelemetryChannel
	if err = json.Unmarshal(raw, &channels); err != nil {
		return nil, err
	}
	schema.Channels = channels
	schema.Bytes = append([]byte(nil), schema.Bytes...)
	schema.DecoderSources = append([]domain.FileParserPart(nil), schema.DecoderSources...)
	implementation, err := fileParserImplementation(reader.ParserSources(), schema.DecoderSources, info)
	if err != nil {
		return nil, err
	}
	configSHA := domain.FileSHA256(raw)
	raw, err = json.Marshal(pin)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(raw, &pin)
	return &FileChecks{store: store, accounts: accounts, scope: scope, reader: reader, scorer: scorer, schema: schema, pin: pin, limits: limits, implementation: implementation, configSHA: configSHA, normalize: make(chan struct{}, 1), execute: make(chan struct{}, 1)}, nil
}
func (s *FileChecks) actor(ctx context.Context) (context.Context, domain.Account, error) {
	p, ok := domain.CurrentPrincipal(ctx)
	if !ok || !p.Authenticated || p.Mechanism != "session" || p.AccountID == nil || *p.AccountID <= 0 {
		return ctx, domain.Account{}, domain.NewError(domain.Unauthorized, "session_required")
	}
	a, err := s.accounts.Account(ctx, *p.AccountID)
	if err != nil {
		return ctx, a, err
	}
	if a.ID != *p.AccountID || !a.Active || (a.Role != "technologist" && a.Role != "admin") {
		return ctx, a, domain.NewError(domain.Forbidden, "file_check_forbidden")
	}
	return domain.WithPrincipal(ctx, domain.AccountPrincipal(a)), a, nil
}
func (s *FileChecks) authorize(ctx context.Context, wellID int64) (context.Context, domain.Account, domain.Well, error) {
	ctx, a, err := s.actor(ctx)
	if err != nil {
		return ctx, a, domain.Well{}, err
	}
	well, err := s.scope.RequireWell(ctx, wellID)
	if err != nil {
		return ctx, a, well, err
	}
	if well.ID != wellID || !well.Active || well.ArchivedAt != nil || !domain.AccountPrincipal(a).Allows(well) {
		return ctx, a, well, domain.NewError(domain.Missing, "well_unavailable")
	}
	return ctx, a, well, nil
}
func normalizeFileSpec(v domain.FileParseSpec, l domain.FileInputLimits) (domain.FileParseSpec, []byte, error) {
	if v.Version != "file_input_v1" || (v.Format != "csv" && v.Format != "xlsx") || v.HeaderRow < 1 || v.HeaderRow > l.HeaderRow || v.TimeColumn == "" || len(v.Mappings) < 1 || len(v.Mappings) > l.Mappings {
		return v, nil, fileInvalid("invalid_parse_spec")
	}
	if v.Format == "csv" {
		if len(v.Delimiter) != 1 || !strings.ContainsAny(v.Delimiter, ",;\t") || v.Sheet != "" {
			return v, nil, fileInvalid("invalid_csv_options")
		}
	} else if v.Sheet == "" || v.Delimiter != "" {
		return v, nil, fileInvalid("invalid_xlsx_options")
	}
	switch v.TimestampLayout {
	case "rfc3339", "2006-01-02 15:04:05", "2006-01-02", "02.01.2006 15:04:05", "excel_serial":
	default:
		return v, nil, fileInvalid("unsupported_timestamp_layout")
	}
	if v.TimestampLayout == "excel_serial" && v.Format != "xlsx" {
		return v, nil, fileInvalid("unsupported_timestamp_layout")
	}
	if v.Timezone == "" {
		return v, nil, fileInvalid("explicit_timezone_required")
	}
	if _, e := time.LoadLocation(v.Timezone); e != nil {
		return v, nil, fileInvalid("invalid_timezone")
	}
	if v.WellColumn != nil {
		column := *v.WellColumn
		v.WellColumn = &column
		if column == "" || (v.WellIdentity != "pulse" && v.WellIdentity != "unitek" && v.WellIdentity != "armits") {
			return v, nil, fileInvalid("invalid_well_identity")
		}
	} else if v.WellIdentity != "" {
		return v, nil, fileInvalid("invalid_well_identity")
	}
	v.SchemaSHA256 = ""
	v.Mappings = append([]domain.Mapping(nil), v.Mappings...)
	sort.Slice(v.Mappings, func(i, j int) bool { return v.Mappings[i].Field < v.Mappings[j].Field })
	fields, targets := map[string]bool{}, map[string]bool{}
	for _, m := range v.Mappings {
		if m.Field == "" || m.Parameter == "" || m.Field == v.TimeColumn || (v.WellColumn != nil && m.Field == *v.WellColumn) || fields[m.Field] || targets[m.Parameter] || math.IsNaN(m.Scale) || math.IsInf(m.Scale, 0) || math.IsNaN(m.Offset) || math.IsInf(m.Offset, 0) {
			return v, nil, fileInvalid("invalid_mapping")
		}
		fields[m.Field] = true
		targets[m.Parameter] = true
	}
	raw, e := json.Marshal(v)
	if e != nil {
		return v, nil, e
	}
	if len(raw) > l.SpecBytes {
		return v, nil, fileInvalid("spec_limit")
	}
	return v, raw, nil
}
func (s *FileChecks) resolveSpec(v domain.FileParseSpec) (domain.FileParseSpec, error) {
	lookup := map[string]domain.TelemetryChannel{}
	for _, c := range s.schema.Channels {
		for _, name := range append([]string{c.Canonical}, c.Aliases...) {
			if old, ok := lookup[name]; ok && old.Canonical != c.Canonical {
				return v, fileInvalid("ambiguous_schema")
			}
			lookup[name] = c
		}
	}
	for _, name := range []string{"qg", "qg_tex", "qn"} {
		lookup[name] = domain.TelemetryChannel{Canonical: name, Scale: 1}
	}
	seen := map[string]bool{}
	for i, m := range v.Mappings {
		c, ok := lookup[m.Parameter]
		if !ok || m.Parameter == "dat_zam" {
			return v, fileInvalid("unknown_mapping_target")
		}
		if seen[c.Canonical] {
			return v, fileInvalid("duplicate_mapping_target")
		}
		seen[c.Canonical] = true
		if (m.Unit != "" && m.Unit != c.Unit) || (m.Scale != 0 && m.Scale != c.Scale) || m.Offset != c.Offset {
			return v, fileInvalid("mapping_schema_mismatch")
		}
		for _, mark := range strings.FieldsFunc(m.Missing, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
			n, e := strconv.ParseFloat(mark, 64)
			if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
				return v, fileInvalid("invalid_missing_marker")
			}
			if math.Abs(n) <= 1e-6 && (c.Canonical == "F_out" || c.Canonical == "Ia" || c.Canonical == "Ib" || c.Canonical == "Ic" || c.Canonical == "qg" || c.Canonical == "qg_tex" || c.Canonical == "qn" || c.Canonical == "Pump_discharge_flow" || c.Canonical == "Current_motor_max" || strings.HasPrefix(c.Canonical, "Flowrate for ")) {
				return v, fileInvalid("valid_zero_is_not_missing")
			}
		}
		m.Parameter = c.Canonical
		m.Unit = c.Unit
		m.Scale = c.Scale
		m.Offset = c.Offset
		m.IsSet = c.IsSet || strings.HasPrefix(c.Canonical, "set_")
		m.Missing = strings.Trim(c.Missing+";"+m.Missing, ";")
		v.Mappings[i] = m
	}
	v.SchemaSHA256 = s.schema.SHA256
	return v, nil
}
func (s *FileChecks) PrepareFileInput(ctx context.Context, wellID int64, key string, spec domain.FileParseSpec, raw io.Reader) (domain.PreparedFileInput, error) {
	ctx, a, well, err := s.authorize(ctx, wellID)
	if err != nil {
		return domain.PreparedFileInput{}, err
	}
	if !checkRequestID.MatchString(key) || raw == nil {
		return domain.PreparedFileInput{}, fileInvalid("invalid_request_key")
	}
	select {
	case s.normalize <- struct{}{}:
		defer func() { <-s.normalize }()
	default:
		return domain.PreparedFileInput{}, domain.NewError(domain.Limited, "normalization_busy")
	}
	ctx, cancel := context.WithTimeout(ctx, s.limits.Timeout)
	defer cancel()
	// Namespace lookup precedes reader/schema/default resolution and never selects payload.
	previous, lookupErr := s.store.FileInputRequest(ctx, a.ID, key)
	if lookupErr != nil && !fileMissing(lookupErr) {
		return domain.PreparedFileInput{}, lookupErr
	}
	spec, clientJSON, err := normalizeFileSpec(spec, s.limits)
	if err != nil {
		return domain.PreparedFileInput{}, err
	}
	content, err := io.ReadAll(io.LimitReader(raw, s.limits.RawBytes+1))
	if err != nil {
		return domain.PreparedFileInput{}, err
	}
	if err = ctx.Err(); err != nil {
		return domain.PreparedFileInput{}, err
	}
	if len(content) == 0 || int64(len(content)) > s.limits.RawBytes {
		return domain.PreparedFileInput{}, fileInvalid("raw_limit")
	}
	commitmentJSON, _ := json.Marshal(struct {
		Version     string
		Actor, Well int64
		Raw         string
		Spec        json.RawMessage
	}{"request_commitment_v1", a.ID, wellID, domain.FileSHA256(content), clientJSON})
	commitment := domain.FileSHA256(commitmentJSON)
	if lookupErr == nil {
		if previous.RequestCommitmentSHA256 != commitment || previous.ActorAccountID != a.ID || previous.PulseWellID != wellID {
			return domain.PreparedFileInput{}, domain.NewError(domain.Duplicate, "request_key_conflict")
		}
		return domain.PreparedFileInput{FileInputSummary: previous}, nil
	}
	spec, err = s.resolveSpec(spec)
	if err != nil {
		return domain.PreparedFileInput{}, err
	}
	captured := time.Now().UTC()
	rows, err := s.reader.Read(ctx, bytes.NewReader(content), spec, s.limits)
	if err != nil {
		return domain.PreparedFileInput{}, err
	}
	canonical := &fileBoundedWriter{max: s.limits.CanonicalBytes}
	rowCount, ignored, err := s.normalizeRows(ctx, well, rows, spec, captured, canonical)
	if err != nil {
		return domain.PreparedFileInput{}, err
	}
	implementation := s.implementation
	implementation.Sources = append([]domain.FileParserPart(nil), s.implementation.Sources...)
	implementation.Modules = append([]domain.FileParserModule(nil), s.implementation.Modules...)
	recipe := domain.FileParseRecipe{Version: "file_parser_v2", Limits: s.limits, Date1904: rows.Date1904, Implementation: &implementation, SchemaSHA256: s.schema.SHA256, ResolvedConfigSHA256: s.configSHA}
	recipeJSON, _ := json.Marshal(recipe)
	token := make([]byte, 16)
	if _, err = rand.Read(token); err != nil {
		return domain.PreparedFileInput{}, err
	}
	v := domain.PreparedFileInput{FileInputSummary: domain.FileInputSummary{ID: hex.EncodeToString(token), PulseWellID: wellID, ActorAccountID: a.ID, Actor: domain.FileActorSnapshot{Name: a.Name, Role: a.Role, Revision: a.Revision}, RequestKey: key, SourceKind: "uploaded_file", CreatedAt: captured, CapturedAt: captured, RawSHA256: domain.FileSHA256(content), CanonicalSHA256: domain.FileSHA256(canonical.Bytes()), RequestCommitmentSHA256: commitment, ParseSpec: spec, ParseRecipeSHA256: domain.FileSHA256(recipeJSON), ParseRecipe: recipe, ExecutionPin: s.pin.Clone(), RawBytes: int64(len(content)), CanonicalBytes: int64(canonical.Len()), Rows: rowCount, Columns: len(rows.Columns), IgnoredColumns: ignored}, Raw: content, Canonical: canonical.Bytes(), Limits: s.limits}
	v.IdentityJSON, err = domain.FileInputIdentityJSON(v.FileInputSummary)
	if err != nil {
		return v, err
	}
	v.FingerprintSHA256 = domain.FileSHA256(v.IdentityJSON)
	return v, domain.ValidateFileInput(v)
}

type fileBoundedWriter struct {
	bytes.Buffer
	max int64
}

func (b *fileBoundedWriter) Write(v []byte) (int, error) {
	if int64(b.Len()+len(v)) > b.max {
		return 0, fileInvalid("canonical_limit")
	}
	return b.Buffer.Write(v)
}
func (s *FileChecks) normalizeRows(ctx context.Context, well domain.Well, rows domain.FileInputRows, spec domain.FileParseSpec, asOf time.Time, output io.Writer) (int, []string, error) {
	columns := map[string]int{}
	for i, c := range rows.Columns {
		if _, ok := columns[c]; ok {
			return 0, nil, fileInvalid("duplicate_header")
		}
		columns[c] = i
	}
	ti, ok := columns[spec.TimeColumn]
	if !ok {
		return 0, nil, fileInvalid("time_column_missing")
	}
	wi := -1
	if spec.WellColumn != nil {
		wi, ok = columns[*spec.WellColumn]
		if !ok {
			return 0, nil, fileInvalid("well_column_missing")
		}
	}
	used := map[string]bool{spec.TimeColumn: true}
	if spec.WellColumn != nil {
		used[*spec.WellColumn] = true
	}
	type mappedColumn struct {
		index   int
		mapping domain.Mapping
	}
	mapped := make([]mappedColumn, 0, len(spec.Mappings))
	sensors := make([]mappedColumn, 0, len(spec.Mappings))
	for _, m := range spec.Mappings {
		index, exists := columns[m.Field]
		if !exists {
			return 0, nil, fileInvalid("mapped_column_missing")
		}
		used[m.Field] = true
		column := mappedColumn{index: index, mapping: m}
		mapped = append(mapped, column)
		if !m.IsSet {
			sensors = append(sensors, column)
		}
	}
	// Валидируются ID всех строк до времени/числовых значений.
	for _, row := range rows.Rows {
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}
		if len(row) != len(rows.Columns) {
			return 0, nil, fileInvalid("inconsistent_columns")
		}
		if wi >= 0 {
			v := row[wi]
			id, e := strconv.ParseInt(v, 10, 64)
			if e != nil || id <= 0 || strconv.FormatInt(id, 10) != v {
				return 0, nil, fileInvalid("invalid_well_id")
			}
			expected := well.ID
			switch spec.WellIdentity {
			case "unitek":
				if well.UnitekID == nil {
					return 0, nil, fileInvalid("well_identity_missing")
				}
				expected = *well.UnitekID
			case "armits":
				if well.ArmitsID == nil {
					return 0, nil, fileInvalid("well_identity_missing")
				}
				expected = *well.ArmitsID
			}
			if id != expected {
				return 0, nil, fileInvalid("multiple_wells_not_supported")
			}
		}
	}
	loc, _ := time.LoadLocation(spec.Timezone)
	// Retain only O(rows) time/index metadata alongside the bounded reader rows.
	// Do not materialize all-row generic records or Measurement maps/pointers.
	type indexedRow struct {
		timestamp time.Time
		index     int
	}
	indexed := make([]indexedRow, 0, len(rows.Rows))
	for index, row := range rows.Rows {
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}
		ts, err := parseFileTimestamp(row[ti], spec.TimestampLayout, loc, rows.Date1904)
		if err != nil {
			return 0, nil, err
		}
		for _, column := range mapped {
			v := row[column.index]
			if v != "" {
				n, ok := number(v)
				if !ok {
					return 0, nil, fileInvalid("non_numeric_measurement")
				}
				if _, present := cleanTelemetryScalar(n, column.mapping); !present && !fileIsMarker(n, column.mapping) {
					return 0, nil, fileInvalid("non_finite_measurement")
				}
			}
		}
		if ts.IsZero() || ts.After(asOf.Add(time.Hour)) || len(sensors) == 0 {
			return 0, nil, fileInvalid("invalid_telemetry_row")
		}
		indexed = append(indexed, indexedRow{timestamp: ts.UTC(), index: index})
	}
	if len(indexed) == 0 {
		return 0, nil, fileInvalid("invalid_telemetry_row")
	}
	sort.Slice(indexed, func(i, j int) bool { return indexed[i].timestamp.Before(indexed[j].timestamp) })
	for i := 1; i < len(indexed); i++ {
		if indexed[i].timestamp.Equal(indexed[i-1].timestamp) {
			return 0, nil, fileInvalid("duplicate_timestamp")
		}
	}
	if indexed[len(indexed)-1].timestamp.Sub(indexed[0].timestamp) > 366*24*time.Hour {
		return 0, nil, fileInvalid("timestamp_span_limit")
	}
	sort.Slice(sensors, func(i, j int) bool { return sensors[i].mapping.Parameter < sensors[j].mapping.Parameter })
	// A single reusable CSV row replaces the whole Measurement value graph.
	csvRow := make([]string, len(sensors)+1)
	csvRow[0] = "timestamp"
	for i, column := range sensors {
		csvRow[i+1] = column.mapping.Parameter
	}
	writer := csv.NewWriter(output)
	if err := writer.Write(csvRow); err != nil {
		return 0, nil, err
	}
	for _, ref := range indexed {
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}
		csvRow[0] = ref.timestamp.Format(time.RFC3339Nano)
		row := rows.Rows[ref.index]
		for i, column := range sensors {
			csvRow[i+1] = ""
			if n, ok := number(row[column.index]); ok {
				if n, present := cleanTelemetryScalar(n, column.mapping); present {
					csvRow[i+1] = strconv.FormatFloat(n, 'g', -1, 64)
				}
			}
		}
		if err := writer.Write(csvRow); err != nil {
			return 0, nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return 0, nil, err
	}
	ignored := []string{}
	for _, c := range rows.Columns {
		if !used[c] {
			ignored = append(ignored, c)
		}
	}
	return len(indexed), ignored, nil
}
func fileIsMarker(n float64, m domain.Mapping) bool {
	for _, v := range strings.FieldsFunc(m.Missing, func(r rune) bool { return r == ';' || r == ',' || r == ' ' }) {
		x, e := strconv.ParseFloat(v, 64)
		if e == nil && math.Abs(x-n) <= 1e-6 {
			return true
		}
	}
	return false
}
func parseFileTimestamp(v, layout string, loc *time.Location, date1904 bool) (time.Time, error) {
	if layout == "rfc3339" {
		ts, e := time.Parse(time.RFC3339Nano, v)
		if e != nil {
			return time.Time{}, fileInvalid("invalid_timestamp")
		}
		return ts.UTC(), nil
	}
	if layout == "excel_serial" {
		serial, e := strconv.ParseFloat(v, 64)
		if e != nil || math.IsNaN(serial) || math.IsInf(serial, 0) || (!date1904 && serial >= 60 && serial < 61) {
			return time.Time{}, fileInvalid("invalid_excel_date")
		}
		if serial < 0 || serial > 2950000 {
			return time.Time{}, fileInvalid("invalid_excel_date")
		}
		base := time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC)
		if date1904 {
			base = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
		} else if serial >= 61 {
			serial--
		}
		days := math.Floor(serial)
		ts := base.AddDate(0, 0, int(days)).Add(time.Duration(math.Round((serial - days) * 86400 * 1e9)))
		v = ts.Format("2006-01-02 15:04:05.999999999")
		layout = "2006-01-02 15:04:05.999999999"
	}
	ts, e := time.ParseInLocation(layout, v, loc)
	if e != nil || ts.Format(layout) != v {
		return time.Time{}, fileInvalid("invalid_local_timestamp")
	}
	// Проверяем все offset рядом с переходом DST: 0 или >1 решений запрещены.
	offsets := map[int]bool{}
	for d := -48; d <= 48; d++ {
		_, off := ts.Add(time.Duration(d) * time.Hour).In(loc).Zone()
		offsets[off] = true
	}
	wall, e := time.Parse(layout, v)
	if e != nil {
		return time.Time{}, fileInvalid("invalid_timestamp")
	}
	matches := 0
	for off := range offsets {
		candidate := wall.Add(-time.Duration(off) * time.Second)
		if candidate.In(loc).Format("2006-01-02 15:04:05.999999999") == wall.Format("2006-01-02 15:04:05.999999999") {
			matches++
		}
	}
	if matches != 1 {
		return time.Time{}, fileInvalid("ambiguous_local_timestamp")
	}
	return ts.UTC(), nil
}
func (s *FileChecks) owned(ctx context.Context, id string) (context.Context, domain.FileInputSummary, error) {
	ctx, a, e := s.actor(ctx)
	if e != nil {
		return ctx, domain.FileInputSummary{}, e
	}
	v, e := s.store.FileInputSummary(ctx, id)
	if e != nil {
		return ctx, v, e
	}
	if v.ActorAccountID != a.ID {
		return ctx, domain.FileInputSummary{}, domain.NewError(domain.Missing, "input_not_found")
	}
	ctx, _, _, e = s.authorize(ctx, v.PulseWellID)
	return ctx, v, e
}
func (s *FileChecks) FileInputSummary(ctx context.Context, id string) (domain.FileInputSummary, error) {
	_, v, e := s.owned(ctx, id)
	return v, e
}
func (s *FileChecks) FileInputSummaries(ctx context.Context, well int64, before string) ([]domain.FileInputSummary, error) {
	ctx, a, _, e := s.authorize(ctx, well)
	if e != nil {
		return nil, e
	}
	rows, e := s.store.FileInputSummaries(ctx, well, before)
	if e != nil {
		return nil, e
	}
	out := []domain.FileInputSummary{}
	for _, v := range rows {
		if v.ActorAccountID == a.ID {
			out = append(out, v)
		}
	}
	return out, nil
}
func (s *FileChecks) StoredFileResult(ctx context.Context, id string) (domain.StoredFileResult, error) {
	ctx, _, e := s.owned(ctx, id)
	if e != nil {
		return domain.StoredFileResult{}, e
	}
	return s.store.StoredFileResult(ctx, id)
}
func (s *FileChecks) AcceptFileInput(ctx context.Context, v domain.PreparedFileInput) (domain.FileInputSummary, error) {
	ctx, a, _, e := s.authorize(ctx, v.PulseWellID)
	if e != nil {
		return domain.FileInputSummary{}, e
	}
	if v.ActorAccountID != a.ID {
		return domain.FileInputSummary{}, domain.NewError(domain.Forbidden, "actor_mismatch")
	}
	previous, e := s.store.FileInputRequest(ctx, a.ID, v.RequestKey)
	if e == nil {
		if previous.FingerprintSHA256 != v.FingerprintSHA256 || previous.RequestCommitmentSHA256 != v.RequestCommitmentSHA256 {
			return domain.FileInputSummary{}, domain.NewError(domain.Duplicate, "request_key_conflict")
		}
		return previous, nil
	}
	if !fileMissing(e) {
		return domain.FileInputSummary{}, e
	}
	if e = domain.ValidateFileInput(v); e != nil {
		return domain.FileInputSummary{}, e
	}
	expected, _ := json.Marshal(s.pin)
	actual, _ := json.Marshal(v.ExecutionPin)
	expectedImplementation, _ := json.Marshal(s.implementation)
	actualImplementation, _ := json.Marshal(v.ParseRecipe.Implementation)
	if !bytes.Equal(expected, actual) || v.ParseSpec.SchemaSHA256 != s.schema.SHA256 || !bytes.Equal(expectedImplementation, actualImplementation) || v.ParseRecipe.ResolvedConfigSHA256 != s.configSHA || v.ParseRecipe.Limits != s.limits {
		return domain.FileInputSummary{}, fileInvalid("prepared_configuration_mismatch")
	}
	return s.store.AcceptFileInput(ctx, v)
}
func (s *FileChecks) ComputeStoredInput(ctx context.Context, id string) (domain.StoredFileResult, error) {
	ctx, summary, e := s.owned(ctx, id)
	if e != nil {
		return domain.StoredFileResult{}, e
	}
	if result, e := s.store.StoredFileResult(ctx, id); e == nil {
		return result, nil
	} else if !fileMissing(e) {
		return domain.StoredFileResult{}, e
	}
	select {
	case s.execute <- struct{}{}:
		defer func() { <-s.execute }()
	default:
		return domain.StoredFileResult{}, domain.NewError(domain.Limited, "execution_busy")
	}
	v, e := s.store.StoredFileInput(ctx, id)
	if e != nil {
		return domain.StoredFileResult{}, e
	}
	if v.FingerprintSHA256 != summary.FingerprintSHA256 {
		return domain.StoredFileResult{}, domain.NewError(domain.Unavailable, "input_corrupt")
	}
	if e = domain.ValidateFileInput(v); e != nil {
		return domain.StoredFileResult{}, e
	}
	ctx, _, _, e = s.authorize(ctx, v.PulseWellID)
	if e != nil {
		return domain.StoredFileResult{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	result, e := s.scorer.UploadPinned(ctx, v.ExecutionPin, domain.PinnedInputIdentity{InputSnapshotID: v.ID, CanonicalSHA256: v.CanonicalSHA256, FingerprintSHA256: v.FingerprintSHA256, CanonicalBytes: v.CanonicalBytes, SourceKind: v.SourceKind}, bytes.NewReader(v.Canonical))
	if e != nil {
		return domain.StoredFileResult{}, e
	}
	raw, e := json.Marshal(result)
	if e != nil {
		return domain.StoredFileResult{}, e
	}
	if len(raw) > s.limits.ResultBytes {
		return domain.StoredFileResult{}, domain.NewError(domain.Limited, "result_too_large")
	}
	out := domain.StoredFileResult{InputID: id, FingerprintSHA256: v.FingerprintSHA256, ExecutionPin: v.ExecutionPin, ComputedAt: time.Now().UTC(), Result: result}
	return out, domain.ValidateFileResult(summary, out)
}
func (s *FileChecks) CommitStoredResult(ctx context.Context, result domain.StoredFileResult) (domain.StoredFileResult, error) {
	ctx, v, e := s.owned(ctx, result.InputID)
	if e != nil {
		return domain.StoredFileResult{}, e
	}
	if v.ParseRecipe.Version != "file_parser_v2" {
		return domain.StoredFileResult{}, domain.NewError(domain.Unavailable, "parser_identity_unavailable")
	}
	a, _ := json.Marshal(v.ExecutionPin)
	b, _ := json.Marshal(result.ExecutionPin)
	if result.FingerprintSHA256 != v.FingerprintSHA256 || !bytes.Equal(a, b) || result.ComputedAt.IsZero() {
		return domain.StoredFileResult{}, domain.NewError(domain.Duplicate, "result_identity_conflict")
	}
	if e = domain.ValidateFileResult(v, result); e != nil {
		return domain.StoredFileResult{}, e
	}
	raw, e := json.Marshal(result.Result)
	if e != nil {
		return domain.StoredFileResult{}, e
	}
	if len(raw) > s.limits.ResultBytes {
		return domain.StoredFileResult{}, domain.NewError(domain.Limited, "result_too_large")
	}
	return s.store.CommitStoredResult(ctx, result)
}

//go:embed file_inputs.go
var fileParserServiceSource []byte

//go:embed integrations.go
var fileParserIntegrationsSource []byte
var fileParserServiceParts = []domain.FileParserPart{{Name: "service/file_inputs.go", SHA256: domain.FileSHA256(fileParserServiceSource)}, {Name: "service/integrations.go", SHA256: domain.FileSHA256(fileParserIntegrationsSource)}}
var fileParserBuildOnce sync.Once
var fileParserBuildMetadata *debug.BuildInfo
var fileParserBuildAvailable bool

func fileParserActualBuildInfo() (*debug.BuildInfo, bool) {
	fileParserBuildOnce.Do(func() { fileParserBuildMetadata, fileParserBuildAvailable = debug.ReadBuildInfo() })
	return fileParserBuildMetadata, fileParserBuildAvailable
}
func fileParserImplementation(reader, decoder []domain.FileParserPart, info *debug.BuildInfo) (domain.FileParserImplementation, error) {
	unavailable := domain.NewError(domain.Unavailable, "parser_build_identity_unavailable")
	if info == nil || info.GoVersion != runtime.Version() {
		return domain.FileParserImplementation{}, unavailable
	}
	// Сохраняем только выбранную actual closure; local replacement не становится публичным path.
	selected := map[string]domain.FileParserModule{}
	required := map[string]bool{}
	for _, path := range domain.FileParserModulePaths() {
		required[path] = true
	}
	for _, module := range info.Deps {
		if module == nil || !required[module.Path] {
			continue
		}
		if _, ok := selected[module.Path]; ok {
			return domain.FileParserImplementation{}, unavailable
		}
		actual := module
		replacement := ""
		if module.Replace != nil {
			actual = module.Replace
			replacement = actual.Path
			if actual.Replace != nil {
				return domain.FileParserImplementation{}, unavailable
			}
		}
		selected[module.Path] = domain.FileParserModule{Path: module.Path, Version: actual.Version, Sum: actual.Sum, ReplacementPath: replacement}
	}
	parts := domain.FileParserDomainSources()
	parts = append(parts, fileParserServiceParts...)
	parts = append(parts, reader...)
	parts = append(parts, decoder...)
	sort.Slice(parts, func(i, j int) bool { return parts[i].Name < parts[j].Name })
	out := domain.FileParserImplementation{Version: "file_parser_impl_v1", Sources: parts, GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	for _, path := range domain.FileParserModulePaths() {
		module, ok := selected[path]
		if !ok {
			return out, unavailable
		}
		out.Modules = append(out.Modules, module)
	}
	if e := out.Validate(); e != nil {
		return out, unavailable
	}
	return out, nil
}
