package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"time"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// Accept exactly the lowercase canonical UUID syntax; versions/variants and the
// nil UUID retain their prior acceptance semantics.
var passportCommitKey = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type PassportOptions struct {
	Scope         FundScope
	AppScope      string
	CohortVersion string
	Location      *time.Location
	Now           func() time.Time
}
type EquipmentPassports struct {
	store EquipmentPassportStore
	codec PassportWorkbookCodec
	opts  PassportOptions
}

func NewEquipmentPassports(store EquipmentPassportStore, codec PassportWorkbookCodec, opts PassportOptions) (*EquipmentPassports, error) {
	if store == nil || codec == nil || (opts.AppScope != "research" && opts.AppScope != "cohort") || opts.AppScope == "cohort" && (opts.Scope == nil || opts.CohortVersion == "") {
		return nil, fmt.Errorf("equipment passports require explicit store/codec/policy/scope")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Location == nil {
		var err error
		opts.Location, err = time.LoadLocation("Europe/Moscow")
		if err != nil {
			return nil, err
		}
	}
	return &EquipmentPassports{store: store, codec: codec, opts: opts}, nil
}
func (s *EquipmentPassports) policy() domain.PassportPolicy {
	return domain.PassportPolicy{AppScope: s.opts.AppScope, CohortVersion: s.opts.CohortVersion}
}
func (s *EquipmentPassports) today() domain.PassportDate {
	return domain.PassportDate(s.opts.Now().In(s.opts.Location).Format("2006-01-02"))
}
func passportServiceAuthority(ctx context.Context, permission domain.Permission) error {
	p, ok := domain.CurrentPrincipal(ctx)
	if !ok || !p.Authenticated || p.AccountID == nil || *p.AccountID <= 0 || p.Mechanism != "session" || p.Authorization == nil {
		return domain.NewError(domain.Unauthorized, "Требуется персональный вход")
	}
	if !domain.RBACEnforced(ctx) {
		return domain.NewError(domain.Forbidden, "equipment_passports_require_enforce")
	}
	return p.Authorization.Require(permission)
}
func passportServiceError(err error) error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if errors.As(err, &de) {
		return err
	}
	return &domain.Error{Kind: domain.Unavailable, Message: "equipment_passport_unavailable", Cause: err}
}
func (s *EquipmentPassports) Read(ctx context.Context, id domain.PassportID) (domain.PassportView, error) {
	if e := passportServiceAuthority(ctx, "equipment_passports.read"); e != nil {
		return domain.PassportView{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	v, e := s.store.ReadPassport(ctx, s.policy(), id)
	return v, passportServiceError(e)
}
func (s *EquipmentPassports) HistoryTable(ctx context.Context, id domain.PassportID, raw string, size int, cursor string) (domain.TablePage[domain.PassportEvent], error) {
	if e := passportServiceAuthority(ctx, "equipment_passports.history.read"); e != nil {
		return domain.TablePage[domain.PassportEvent]{}, e
	}
	if size == 0 {
		size = 25
	}
	if size > 50 {
		return domain.TablePage[domain.PassportEvent]{}, tableInvalid()
	}
	if raw == "" {
		raw = `{"version":1,"filters":[],"sort":[]}`
	}
	req, e := parseTableRequest(ctx, "equipmenthistory", int64(id), raw, size, cursor, s.opts.AppScope, s.opts.CohortVersion)
	if e != nil {
		return domain.TablePage[domain.PassportEvent]{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, e := s.store.PassportHistoryTable(ctx, s.policy(), id, req)
	return tablePage(req, rows, passportServiceError(e))
}
func (s *EquipmentPassports) Prepare(ctx context.Context, in domain.PassportChangeSet, provenance domain.PassportProvenance) (domain.PassportPreview, error) {
	if provenance.Source != domain.PassportSourceManual {
		return domain.PassportPreview{}, domain.NewError(domain.Invalid, "manual_source_required")
	}
	if e := passportServiceAuthority(ctx, "equipment_passports.update"); e != nil {
		return domain.PassportPreview{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	v, e := s.store.PreparePassport(ctx, s.policy(), in, domain.PassportProvenance{Source: domain.PassportSourceManual}, s.today())
	return v, passportServiceError(e)
}
func (s *EquipmentPassports) Import(ctx context.Context, r io.Reader) (domain.PassportPreview, error) {
	if e := passportServiceAuthority(ctx, "equipment_passports.import"); e != nil {
		return domain.PassportPreview{}, e
	}
	ctx, release, e := passportWorkContext(ctx)
	if e != nil {
		return domain.PassportPreview{}, e
	}
	defer release()
	workbook, e := s.codec.Decode(ctx, r)
	if e != nil {
		return domain.PassportPreview{}, passportServiceError(e)
	}
	for _, row := range workbook.ChangeSet.Rows {
		if !slices.Contains([]domain.PassportAction{"register", "update", "replace"}, row.Action) {
			return domain.PassportPreview{}, domain.NewError(domain.Invalid, "excel_action_not_allowed")
		}
	}
	version := domain.PassportSchemaVersion
	source := domain.PassportProvenance{Source: domain.PassportSourceExcel, SourceFileSHA256: &workbook.FileSHA256, TemplateVersion: &version, Rows: slices.Clone(workbook.Rows)}
	v, e := s.store.PreparePassport(ctx, s.policy(), workbook.ChangeSet, source, s.today())
	return v, passportServiceError(e)
}
func (s *EquipmentPassports) Preview(ctx context.Context, id domain.PassportID) (domain.PassportPreview, error) {
	if e := passportServiceAuthority(ctx, "equipment_passports.read"); e != nil {
		return domain.PassportPreview{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	v, e := s.store.ReadPassportPreview(ctx, s.policy(), id)
	return v, passportServiceError(e)
}
func (s *EquipmentPassports) CancelPreview(ctx context.Context, id domain.PassportID) error {
	if e := passportServiceAuthority(ctx, "equipment_passports.read"); e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return passportServiceError(s.store.CancelPassportPreview(ctx, s.policy(), id))
}
func (s *EquipmentPassports) Commit(ctx context.Context, in domain.PassportCommitInput) (domain.PassportReceipt, error) {
	if e := passportServiceAuthority(ctx, "equipment_passports.read"); e != nil {
		return domain.PassportReceipt{}, e
	}
	if !passportCommitKey.MatchString(in.IdempotencyKey) || in.PreviewID <= 0 {
		return domain.PassportReceipt{}, domain.NewError(domain.Invalid, "invalid_commit_identity")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	v, e := s.store.CommitPassport(ctx, s.policy(), in, s.today())
	return v, passportServiceError(e)
}
func (s *EquipmentPassports) Receipt(ctx context.Context, id domain.PassportID) (domain.PassportReceipt, error) {
	if e := passportServiceAuthority(ctx, "equipment_passports.read"); e != nil {
		return domain.PassportReceipt{}, e
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	v, e := s.store.ReadPassportReceipt(ctx, s.policy(), id)
	return v, passportServiceError(e)
}
func (s *EquipmentPassports) Template(ctx context.Context, ids []domain.PassportID, w io.Writer) error {
	return s.write(ctx, domain.PassportExportRequest{WellIDs: ids, Mode: "current_components"}, domain.PassportPurposeTemplate, w)
}
func (s *EquipmentPassports) Export(ctx context.Context, in domain.PassportExportRequest, w io.Writer) error {
	purpose := domain.PassportPurposeExportCurrent
	if in.Mode == "history" {
		purpose = domain.PassportPurposeExportHistory
	}
	return s.write(ctx, in, purpose, w)
}
func (s *EquipmentPassports) write(ctx context.Context, in domain.PassportExportRequest, purpose domain.PassportSnapshotPurpose, w io.Writer) error {
	permission := domain.Permission("equipment_passports.export")
	if purpose == domain.PassportPurposeTemplate {
		permission = "equipment_passports.import"
	}
	if e := passportServiceAuthority(ctx, permission); e != nil {
		return e
	}
	if purpose == domain.PassportPurposeExportHistory {
		if e := passportServiceAuthority(ctx, "equipment_passports.history.read"); e != nil {
			return e
		}
	}
	if len(in.WellIDs) == 0 || len(in.WellIDs) > 100 || in.Mode != "history" && in.Mode != "current_components" {
		return domain.NewError(domain.Invalid, "invalid_export")
	}
	if purpose == domain.PassportPurposeExportHistory {
		raw, e := json.Marshal(in.Query)
		if e != nil {
			return e
		}
		if in.Query.Version == 0 {
			raw = []byte(`{"version":1,"filters":[],"sort":[]}`)
		}
		req, e := parseTableRequest(ctx, "equipmenthistory", 0, string(raw), 50, "")
		if e != nil {
			return e
		}
		in.Query = req.Query
	}
	ctx, release, e := passportWorkContext(ctx)
	if e != nil {
		return e
	}
	defer release()
	snapshot, e := s.store.LoadPassportExport(ctx, s.policy(), in, purpose)
	if e != nil {
		return passportServiceError(e)
	}
	return passportServiceError(s.codec.Encode(ctx, w, snapshot))
}
