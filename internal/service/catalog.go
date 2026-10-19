package service

import (
	"context"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

type Catalog struct {
	store Store
	scope FundScope
}

func (c *Catalog) WithScope(scope FundScope) *Catalog { c.scope = scope; return c }

func NewCatalog(store Store) *Catalog {
	return &Catalog{store: store}
}

func WellFromInput(in domain.WellInput) (domain.Well, error) {
	if in.NGDU == nil || in.Department == nil {
		return domain.Well{}, domain.NewError(domain.Invalid, "Обязательные поля: НГДУ и цех")
	}
	if in.WellNumber != nil && len([]rune(*in.WellNumber)) > 50 {
		return domain.Well{}, domain.NewError(domain.Invalid, "well_number длиннее 50 символов")
	}
	fund := "operational"
	if in.FundType != nil {
		fund = *in.FundType
	}
	if fund != "operational" && fund != "presentation" {
		return domain.Well{}, domain.NewError(domain.Invalid, "Недопустимый тип фонда")
	}
	active := true
	if in.Active.Null {
		return domain.Well{}, domain.NewError(domain.Invalid, "active не может быть null")
	}
	if in.Active.Present {
		active = in.Active.Value
	}
	var id int64
	if in.ID != nil {
		if *in.ID <= 0 {
			return domain.Well{}, domain.NewError(domain.Invalid, "ID Pulse должен быть положительным")
		}
		id = *in.ID
	}
	if in.Responsible != nil {
		value := strings.TrimSpace(*in.Responsible)
		if utf8.RuneCountInString(value) > 120 {
			return domain.Well{}, domain.NewError(domain.Invalid, "Ответственный: максимум 120 символов")
		}
		if value == "" {
			in.Responsible = nil
		} else {
			in.Responsible = &value
		}
	}
	return domain.Well{Responsible: in.Responsible, FundType: fund, ID: id, UnitekID: in.UnitekID, ArmitsID: in.ArmitsID, NGDU: *in.NGDU, Department: *in.Department, WellNumber: in.WellNumber, Active: active}, nil
}

func (c *Catalog) Ping(ctx context.Context) error {
	return c.store.Ping(ctx)
}

func (c *Catalog) Wells(ctx context.Context, active bool) ([]domain.Well, error) {
	var rows []domain.Well
	var err error
	if c.scope != nil {
		_, rows, err = c.scope.Members(ctx)
		if active {
			out := []domain.Well{}
			for _, w := range rows {
				if w.Active {
					out = append(out, w)
				}
			}
			rows = out
		}
	} else {
		rows, err = c.store.Wells(ctx, active)
	}
	if err != nil {
		return nil, err
	}
	out := make([]domain.Well, 0, len(rows))
	for _, w := range rows {
		if domain.CanReadWell(ctx, w) {
			out = append(out, w)
		}
	}
	return out, nil
}

func (c *Catalog) Well(ctx context.Context, id int64) (domain.Well, error) {
	if c.scope != nil {
		return c.scope.RequireWell(ctx, id)
	}
	w, err := c.store.Well(ctx, id)
	if err == nil && !domain.CanReadWell(ctx, w) {
		return domain.Well{}, domain.NewError(domain.Missing, "Скважина не найдена")
	}
	return w, err
}

func (c *Catalog) CreateWell(ctx context.Context, in domain.WellInput) (domain.Well, error) {
	if c.scope != nil {
		return domain.Well{}, domain.NewError(domain.Forbidden, "Состав когорты меняется только созданием новой версии")
	}
	if in.ID != nil {
		return domain.Well{}, domain.NewError(domain.Invalid, "ID Pulse назначается автоматически при создании")
	}
	w, err := WellFromInput(in)
	if err != nil {
		return w, err
	}
	return c.store.CreateWell(ctx, w)
}

func (c *Catalog) PatchWell(ctx context.Context, id int64, p domain.WellPatch) (domain.Well, error) {
	if p.Revision.Present && (p.Revision.Null || p.Revision.Value < 1) {
		return domain.Well{}, domain.NewError(domain.Invalid, "Некорректная версия карточки")
	}
	if p.Responsible.Present && !p.Responsible.Null {
		p.Responsible.Value = strings.TrimSpace(p.Responsible.Value)
		if utf8.RuneCountInString(p.Responsible.Value) > 120 {
			return domain.Well{}, domain.NewError(domain.Invalid, "Ответственный: максимум 120 символов")
		}
		if p.Responsible.Value == "" {
			p.Responsible.Null = true
		}
	}
	if p.NGDU.Null || p.Department.Null || p.Active.Null {
		return domain.Well{}, domain.NewError(domain.Invalid, "Обязательные поля не могут быть null")
	}
	if p.WellNumber.Present && !p.WellNumber.Null && len([]rune(p.WellNumber.Value)) > 50 {
		return domain.Well{}, domain.NewError(domain.Invalid, "well_number длиннее 50 символов")
	}
	if err := scopedWell(ctx, c.scope, id); err != nil {
		return domain.Well{}, err
	}
	if c.scope != nil && (p.UnitekID.Present || p.ArmitsID.Present) {
		return domain.Well{}, domain.NewError(domain.Forbidden, "Привязки версии когорты неизменяемы")
	}
	return c.store.PatchWell(ctx, id, p)
}

func (c *Catalog) DeleteWell(ctx context.Context, id int64) error {
	if err := scopedWell(ctx, c.scope, id); err != nil {
		return err
	}
	return c.store.DeleteWell(ctx, id)
}

func (c *Catalog) ImportWells(ctx context.Context, input []domain.WellInput) (domain.ImportResult, error) {
	if c.scope != nil {
		return domain.ImportResult{}, domain.NewError(domain.Forbidden, "Состав когорты неизменяем")
	}
	rows := make([]domain.Well, 0, len(input))
	for _, in := range input {
		w, err := WellFromInput(in)
		if err != nil {
			return domain.ImportResult{}, err
		}
		rows = append(rows, w)
	}
	return c.store.ImportWells(ctx, rows)
}

func (c *Catalog) References(ctx context.Context, kind domain.ReferenceKind) ([]domain.Reference, error) {
	rows, err := c.store.References(ctx, kind)
	p, ok := domain.CurrentPrincipal(ctx)
	if err != nil || (c.scope == nil && (!ok || p.Role == "admin")) {
		return rows, err
	}
	wells, err := c.Wells(ctx, false)
	if err != nil {
		return nil, err
	}
	ids := map[int64]bool{}
	for _, w := range wells {
		if kind == domain.NGDUReference {
			ids[w.NGDU] = true
		} else {
			ids[w.Department] = true
		}
	}
	out := make([]domain.Reference, 0)
	for _, r := range rows {
		if ids[r.ID] {
			out = append(out, r)
		}
	}
	return out, nil
}

func referenceName(name *string) error {
	if name == nil || len([]rune(*name)) > 255 {
		return domain.NewError(domain.Invalid, "Некорректный name")
	}
	return nil
}

func (c *Catalog) CreateReference(ctx context.Context, kind domain.ReferenceKind, in domain.ReferenceInput) (domain.Reference, error) {
	if c.scope != nil {
		return domain.Reference{}, domain.NewError(domain.Forbidden, "Изменение общих справочников недоступно в пользовательском фонде")
	}
	if in.ID == nil {
		return domain.Reference{}, domain.NewError(domain.Invalid, "Не указан id")
	}
	if err := referenceName(in.Name); err != nil {
		return domain.Reference{}, err
	}
	return c.store.CreateReference(ctx, kind, *in.ID, *in.Name)
}

func (c *Catalog) UpdateReference(ctx context.Context, kind domain.ReferenceKind, id int64, name *string) (domain.Reference, error) {
	if c.scope != nil {
		return domain.Reference{}, domain.NewError(domain.Forbidden, "Изменение общих справочников недоступно в пользовательском фонде")
	}
	if err := referenceName(name); err != nil {
		return domain.Reference{}, err
	}
	return c.store.UpdateReference(ctx, kind, id, *name)
}

func (c *Catalog) DeleteReference(ctx context.Context, kind domain.ReferenceKind, id int64) error {
	if c.scope != nil {
		return domain.NewError(domain.Forbidden, "Изменение общих справочников недоступно в пользовательском фонде")
	}
	return c.store.DeleteReference(ctx, kind, id)
}

func (c *Catalog) Incidents(ctx context.Context, id int64) ([]domain.Incident, error) {
	if err := scopedWell(ctx, c.scope, id); err != nil {
		return nil, err
	}
	return c.store.Incidents(ctx, id)
}

func (c *Catalog) CreateIncident(ctx context.Context, id int64, in domain.IncidentInput) (domain.Incident, error) {
	if err := scopedWell(ctx, c.scope, id); err != nil {
		return domain.Incident{}, err
	}
	if in.FailureType == nil || len([]rune(*in.FailureType)) > 100 || in.FailureDate == nil {
		return domain.Incident{}, domain.NewError(domain.Invalid, "Некорректные данные отказа")
	}
	if _, err := time.Parse("2006-01-02", *in.FailureDate); err != nil {
		return domain.Incident{}, domain.NewError(domain.Invalid, "Некорректная дата отказа")
	}
	days := int64(60)
	if in.DaysBefore != nil {
		days = *in.DaysBefore
	}
	if days < 1 || days > 365 {
		return domain.Incident{}, domain.NewError(domain.Invalid, "days_before должен быть в диапазоне 1..365")
	}
	return c.store.CreateIncident(ctx, domain.Incident{WellID: id, FailureType: *in.FailureType, FailureDate: *in.FailureDate, DaysBefore: days})
}

func (c *Catalog) DeleteIncident(ctx context.Context, id int64) error {
	if c.scope != nil {
		resolver, ok := c.store.(interface {
			IncidentWell(context.Context, int64) (int64, error)
		})
		if !ok {
			return domain.NewError(domain.Unavailable, "Не подключён справочник отказов")
		}
		well, err := resolver.IncidentWell(ctx, id)
		if err != nil {
			return err
		}
		if err = scopedWell(ctx, c.scope, well); err != nil {
			return err
		}
	}
	return c.store.DeleteIncident(ctx, id)
}

func (c *Catalog) IncidentStats(ctx context.Context) (domain.IncidentStats, error) {
	if c.scope != nil {
		return c.FilteredIncidentStats(ctx, domain.WellFilter{Archive: "all"})
	}
	return c.store.IncidentStats(ctx)
}

func (c *Catalog) Predictions(ctx context.Context, id int64, count int) ([]domain.Prediction, error) {
	if err := scopedWell(ctx, c.scope, id); err != nil {
		return nil, err
	}
	return c.store.Predictions(ctx, id, count)
}

func (c *Catalog) Complications(ctx context.Context, id int64) ([]domain.Complication, error) {
	if err := scopedWell(ctx, c.scope, id); err != nil {
		return nil, err
	}
	return c.store.Complications(ctx, id)
}

func Classify(rows []domain.Complication) *string {
	categories := map[string]bool{}
	for _, c := range rows {
		if c.Category != nil && *c.Category != "" {
			categories[*c.Category] = true
		}
	}
	if len(categories) > 1 {
		v := "комбинированный"
		return &v
	}
	for key := range categories {
		return &key
	}
	return nil
}

func (c *Catalog) Classification(ctx context.Context, id int64) (domain.Classification, error) {
	if err := scopedWell(ctx, c.scope, id); err != nil {
		return domain.Classification{}, err
	}
	rows, err := c.store.Complications(ctx, id)
	return domain.Classification{WellID: id, Classification: Classify(rows)}, err
}

func (c *Catalog) Runs(ctx context.Context, filter domain.RunFilter) ([]domain.RunEvent, error) {
	rows, err := c.store.Runs(ctx, filter)
	return c.visibleRunEvents(ctx, rows, err)
}

func (c *Catalog) RunEvents(ctx context.Context, dag, run string) ([]domain.RunEvent, error) {
	rows, err := c.store.RunEvents(ctx, dag, run)
	return c.visibleRunEvents(ctx, rows, err)
}

func (c *Catalog) Audit(ctx context.Context, count int) ([]domain.AuditEntry, error) {
	if c.scope != nil {
		return []domain.AuditEntry{}, nil
	}
	return c.store.Audit(ctx, count)
}

func (c *Catalog) RecordAudit(ctx context.Context, actor, method, path string, status int) error {
	return c.store.RecordAudit(ctx, actor, method, path, status)
}

// PulseTelemetry проверяет ID Pulse и переводит их во внутренний ключ хранения.
func (c *Catalog) PulseTelemetry(ctx context.Context, points []domain.Measurement) ([]domain.Measurement, error) {
	checked := map[int64]bool{}
	out := make([]domain.Measurement, len(points))
	for i, p := range points {
		id, err := strconv.ParseInt(p.WellID, 10, 64)
		if err != nil || id <= 0 {
			return nil, domain.NewError(domain.Invalid, "В файле нужен числовой ID Pulse из реестра скважин")
		}
		if !checked[id] {
			if _, err = c.Well(ctx, id); err != nil {
				return nil, err
			}
			checked[id] = true
		}
		p.WellID = PulseTelemetryID(id)
		out[i] = p
	}
	return out, nil
}

// WellsForFund сохраняет совместимость старого API: пустой фильтр возвращает все фонды.
func (c *Catalog) WellsForFund(ctx context.Context, active bool, fund string) ([]domain.Well, error) {
	if fund != "" && fund != "operational" && fund != "presentation" {
		return nil, domain.NewError(domain.Invalid, "Недопустимый фильтр фонда")
	}
	rows, err := c.Wells(ctx, active)
	if err != nil || fund == "" {
		return rows, err
	}
	out := make([]domain.Well, 0, len(rows))
	for _, row := range rows {
		kind := row.FundType
		if kind == "" {
			kind = "operational"
		}
		if kind == fund {
			out = append(out, row)
		}
	}
	return out, nil
}

func (c *Catalog) visibleRunEvents(ctx context.Context, rows []domain.RunEvent, err error) ([]domain.RunEvent, error) {
	if err != nil || c.scope == nil {
		return rows, err
	}
	out := []domain.RunEvent{}
	for _, row := range rows {
		if row.WellID != nil {
			if _, e := c.scope.RequireWell(ctx, *row.WellID); e == nil {
				out = append(out, row)
			}
		}
	}
	return out, nil
}
