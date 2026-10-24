package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func passportLoadBasis(ctx context.Context, tx pgx.Tx, w domain.Well, lock bool) (domain.PassportBasis, error) {
	b := domain.PassportBasis{Well: w, Installations: []domain.PassportInstallation{}}
	q := sqlBuilder.Select("id::text AS id,revision::text AS revision").From("equipment_passport").Where(sq.Eq{"well_id": w.ID})
	if lock {
		q = q.Suffix("FOR UPDATE")
	}
	head, e := one[struct {
		ID       domain.PassportID       `json:"id"`
		Revision domain.PassportRevision `json:"revision"`
	}](ctx, tx, q)
	if e != nil {
		var de *domain.Error
		if errors.As(e, &de) && de.Kind == domain.Missing {
			return b, nil
		}
		return b, e
	}
	b.PassportID = &head.ID
	b.Revision = head.Revision
	if lock {
		if _, e = execute(ctx, tx, sqlBuilder.Select("id").From("equipment_passport_installation").Where(sq.Eq{"passport_id": int64(head.ID)}).OrderBy("id").Suffix("FOR UPDATE")); e != nil {
			return b, e
		}
	}
	attrs := `jsonb_build_object('model',c.model,'manufacturer',c.manufacturer,'serial_number',c.serial_number,'pump_nominal_flow_m3_day',c.pump_nominal_flow_m3_day::text,'pump_nominal_head_m',c.pump_nominal_head_m::text,'pump_stage_count',c.pump_stage_count,'motor_rated_power_kw',c.motor_rated_power_kw::text,'motor_rated_voltage_v',c.motor_rated_voltage_v::text,'motor_rated_current_a',c.motor_rated_current_a::text,'motor_nominal_frequency_hz',c.motor_nominal_frequency_hz::text,'control_rated_power_kw',c.control_rated_power_kw::text,'control_rated_voltage_v',c.control_rated_voltage_v::text,'control_rated_current_a',c.control_rated_current_a::text,'control_output_frequency_min_hz',c.control_output_frequency_min_hz::text,'control_output_frequency_max_hz',c.control_output_frequency_max_hz::text) AS attributes`
	b.Installations, e = list[domain.PassportInstallation](ctx, tx, sqlBuilder.Select("i.id::text AS id,i.component_id::text AS component_id,i.slot,i.status,i.installed_on,i.removed_on,i.predecessor_id::text AS predecessor_id").Column(attrs).From("equipment_passport_installation i").Join("equipment_passport_component c ON c.id=i.component_id").Where(sq.Eq{"i.passport_id": int64(head.ID)}).OrderBy("i.id"))
	return b, e
}
func passportView(ctx context.Context, tx pgx.Tx, w domain.Well) (domain.PassportView, error) {
	b, e := passportLoadBasis(ctx, tx, w, false)
	v := domain.PassportView{WellID: domain.PassportID(w.ID), WellRevision: domain.PassportRevision(w.Revision), PassportID: b.PassportID, Revision: b.Revision, Slots: []domain.PassportInstallation{}, Provenance: []domain.PassportSlotProvenance{}}
	if e != nil {
		return v, e
	}
	for _, installation := range b.Installations {
		if installation.Status != domain.PassportStatusCurrent {
			continue
		}
		v.Slots = append(v.Slots, installation)
		// Match exact after installation identity; unrelated predecessor edits do not alter LastChange.
		row, e := one[struct {
			Payload domain.PassportEvent `json:"payload"`
		}](ctx, tx, sqlBuilder.Select("e.payload").From("equipment_passport_event e").Where(sq.Eq{"e.well_id": w.ID}).Where(sq.Expr("EXISTS(SELECT 1 FROM jsonb_array_elements(e.diff->'after') item WHERE item->>'id'=?)", strconv.FormatInt(int64(*installation.ID), 10))).OrderBy("e.recorded_at DESC", "e.id DESC").Limit(1))
		if e != nil {
			return v, e
		}
		event := row.Payload
		v.Provenance = append(v.Provenance, domain.PassportSlotProvenance{Slot: installation.Slot, LastChange: domain.PassportChangeProvenance{Source: event.Source, Actor: event.Actor, RecordedAt: event.RecordedAt, SourceReference: event.SourceReference, ReceiptID: event.ReceiptID, SourceFileSHA256: event.SourceFileSHA256, TemplateVersion: event.TemplateVersion, SourceRow: event.SourceRow}})
	}
	return v, nil
}
func (r *Repository) ReadPassport(ctx context.Context, policy domain.PassportPolicy, id domain.PassportID) (domain.PassportView, error) {
	var zero domain.PassportView
	tx, e := passportTx(ctx, r, true)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	a, _, e := passportAuthority(ctx, tx, "equipment_passports.read")
	if e != nil {
		return zero, e
	}
	wells, e := passportWells(ctx, tx, policy, a, []domain.PassportID{id}, false)
	if e != nil {
		return zero, e
	}
	v, e := passportView(ctx, tx, wells[0])
	if e != nil {
		return zero, e
	}
	return v, tx.Commit(ctx)
}
func passportNumber(v *domain.PassportDecimal) any {
	if v == nil {
		return nil
	}
	return sq.Expr("?::numeric", string(*v))
}
func passportAttributeValues(a domain.PassportAttributes) map[string]any {
	return map[string]any{
		"model":                           a.Model,
		"manufacturer":                    a.Manufacturer,
		"serial_number":                   a.SerialNumber,
		"pump_nominal_flow_m3_day":        passportNumber(a.PumpNominalFlowM3Day),
		"pump_nominal_head_m":             passportNumber(a.PumpNominalHeadM),
		"pump_stage_count":                a.PumpStageCount,
		"motor_rated_power_kw":            passportNumber(a.MotorRatedPowerKW),
		"motor_rated_voltage_v":           passportNumber(a.MotorRatedVoltageV),
		"motor_rated_current_a":           passportNumber(a.MotorRatedCurrentA),
		"motor_nominal_frequency_hz":      passportNumber(a.MotorNominalFrequencyHz),
		"control_rated_power_kw":          passportNumber(a.ControlRatedPowerKW),
		"control_rated_voltage_v":         passportNumber(a.ControlRatedVoltageV),
		"control_rated_current_a":         passportNumber(a.ControlRatedCurrentA),
		"control_output_frequency_min_hz": passportNumber(a.ControlOutputFrequencyMinHz),
		"control_output_frequency_max_hz": passportNumber(a.ControlOutputFrequencyMaxHz),
	}
}
func passportPersistDiff(ctx context.Context, tx pgx.Tx, head domain.PassportID, d domain.PassportDiff) (domain.PassportDiff, error) {
	for i, after := range d.After {
		attributes := passportAttributeValues(after.Attributes)
		if after.ID == nil {
			attributes["passport_id"] = int64(head)
			attributes["slot"] = string(after.Slot)
			component, e := mutate[struct {
				ID domain.PassportID `json:"id"`
			}](ctx, tx, sqlBuilder.Insert("equipment_passport_component").SetMap(attributes).Suffix("RETURNING id::text AS id"))
			if e != nil {
				return d, e
			}
			after.ComponentID = &component.ID
			created, e := mutate[struct {
				ID domain.PassportID `json:"id"`
			}](ctx, tx, sqlBuilder.Insert("equipment_passport_installation").SetMap(map[string]any{"passport_id": int64(head), "component_id": int64(component.ID), "slot": string(after.Slot), "status": string(after.Status), "installed_on": after.InstalledOn, "removed_on": after.RemovedOn, "predecessor_id": after.PredecessorID}).Suffix("RETURNING id::text AS id"))
			if e != nil {
				return d, e
			}
			after.ID = &created.ID
		} else {
			if after.ComponentID == nil {
				return d, fmt.Errorf("persisted passport component is missing")
			}
			tag, e := execute(ctx, tx, sqlBuilder.Update("equipment_passport_component").SetMap(attributes).Where(sq.Eq{"id": int64(*after.ComponentID), "passport_id": int64(head)}))
			if e != nil {
				return d, e
			}
			if tag.RowsAffected() != 1 {
				return d, passportError(domain.Duplicate, "installation_mismatch")
			}
			tag, e = execute(ctx, tx, sqlBuilder.Update("equipment_passport_installation").SetMap(map[string]any{"status": string(after.Status), "installed_on": after.InstalledOn, "removed_on": after.RemovedOn}).Where(sq.Eq{"id": int64(*after.ID), "passport_id": int64(head)}))
			if e != nil {
				return d, e
			}
			if tag.RowsAffected() != 1 {
				return d, passportError(domain.Duplicate, "installation_mismatch")
			}
		}
		d.After[i] = after
	}
	return d, nil
}
func passportInsertEvent(ctx context.Context, tx pgx.Tx, head domain.PassportID, event domain.PassportEvent) error {
	id, e := one[struct {
		ID domain.PassportID `json:"id"`
	}](ctx, tx, sqlBuilder.Select("nextval(pg_get_serial_sequence('equipment_passport_event','id'))::text AS id"))
	if e != nil {
		return e
	}
	event.ID = id.ID
	raw, e := json.Marshal(event)
	if e != nil {
		return e
	}
	diff, e := json.Marshal(event.Diff)
	if e != nil {
		return e
	}
	_, e = execute(ctx, tx, sqlBuilder.Insert("equipment_passport_event").SetMap(map[string]any{"id": int64(event.ID), "passport_id": int64(head), "well_id": int64(event.WellID), "revision": int64(event.Revision), "receipt_id": int64(event.ReceiptID), "row_key": event.RowKey, "action": string(event.Action), "slot": string(event.Slot), "recorded_at": event.RecordedAt, "actor_id": int64(event.Actor.AccountID), "actor_name": event.Actor.Name, "source": string(event.Source), "reason": event.Reason, "diff": diff, "payload": raw}))
	return e
}

type passportReceiptRecord struct {
	TargetWellIDs []domain.PassportID    `json:"target_well_ids"`
	PreviewID     domain.PassportID      `json:"preview_id"`
	Digest        string                 `json:"digest"`
	AppScope      string                 `json:"app_scope"`
	CohortVersion string                 `json:"cohort_version"`
	Source        domain.PassportSource  `json:"source"`
	Receipt       domain.PassportReceipt `json:"payload"`
}

func passportReceiptQuery() sq.SelectBuilder {
	return sqlBuilder.Select("preview_id::text AS preview_id,digest,app_scope,cohort_version,source,payload", "to_jsonb(ARRAY(SELECT v::text FROM unnest(target_well_ids) v)) AS target_well_ids").From("equipment_passport_receipt")
}
func passportReceiptByKey(ctx context.Context, tx pgx.Tx, actor int64, key string) (passportReceiptRecord, error) {
	return one[passportReceiptRecord](ctx, tx, passportReceiptQuery().Where(sq.Eq{"owner_id": actor, "request_id": key}))
}
func passportReceiptAccess(ctx context.Context, tx pgx.Tx, policy domain.PassportPolicy, a domain.AuthorizationSnapshot, h passportReceiptRecord) error {
	if e := a.Require(passportPermission(h.Source)); e != nil {
		return e
	}
	if policy.AppScope != h.AppScope || policy.CohortVersion != h.CohortVersion {
		return passportError(domain.Missing, "receipt_unavailable")
	}
	ids := h.TargetWellIDs
	_, e := passportWells(ctx, tx, policy, a, ids, false)
	return e
}
func (r *Repository) ReadPassportReceipt(ctx context.Context, policy domain.PassportPolicy, id domain.PassportID) (domain.PassportReceipt, error) {
	var zero domain.PassportReceipt
	tx, e := passportTx(ctx, r, true)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	a, _, e := passportAuthority(ctx, tx, "equipment_passports.read")
	if e != nil {
		return zero, e
	}
	h, e := one[passportReceiptRecord](ctx, tx, passportReceiptQuery().Where(sq.Eq{"id": int64(id), "owner_id": a.AccountID}))
	if e != nil {
		return zero, e
	}
	if e = passportReceiptAccess(ctx, tx, policy, a, h); e != nil {
		return zero, e
	}
	return h.Receipt, tx.Commit(ctx)
}
