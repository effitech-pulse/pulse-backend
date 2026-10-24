package postgres

import (
	"context"
	"fmt"
	"strconv"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/jackc/pgx/v5"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func passportHistoryRows(ctx context.Context, tx pgx.Tx, ids []int64, req domain.TableRequest) (domain.TableRows[domain.PassportEvent], error) {
	rows, e := tableRows[struct {
		Payload domain.PassportEvent `json:"payload"`
	}](ctx, tx, sqlBuilder.Select("e.payload").From("equipment_passport_event e").Where(sq.Eq{"e.well_id": ids}), "equipmenthistory", req)
	out := domain.TableRows[domain.PassportEvent]{Items: []domain.PassportEvent{}, Keys: rows.Keys, HasMore: rows.HasMore}
	for _, row := range rows.Items {
		out.Items = append(out.Items, row.Payload)
	}
	return out, e
}
func (r *Repository) PassportHistoryTable(ctx context.Context, policy domain.PassportPolicy, id domain.PassportID, req domain.TableRequest) (domain.TableRows[domain.PassportEvent], error) {
	var zero domain.TableRows[domain.PassportEvent]
	if req.PageSize < 1 || req.PageSize > 50 {
		return zero, passportError(domain.Invalid, "invalid_table_query")
	}
	tx, e := passportTx(ctx, r, true)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	a, _, e := passportAuthority(ctx, tx, "equipment_passports.history.read")
	if e != nil {
		return zero, e
	}
	if _, e = passportWells(ctx, tx, policy, a, []domain.PassportID{id}, false); e != nil {
		return zero, e
	}
	out, e := passportHistoryRows(ctx, tx, []int64{int64(id)}, req)
	if e != nil {
		return zero, e
	}
	return out, tx.Commit(ctx)
}
func (r *Repository) LoadPassportExport(ctx context.Context, policy domain.PassportPolicy, in domain.PassportExportRequest, purpose domain.PassportSnapshotPurpose) (domain.PassportExport, error) {
	out := domain.PassportExport{Mode: in.Mode, Template: purpose == domain.PassportPurposeTemplate, Current: []domain.PassportView{}, History: []domain.PassportEvent{}, Provenance: []domain.PassportRowProvenance{}}
	permission := domain.Permission("equipment_passports.export")
	switch purpose {
	case domain.PassportPurposeTemplate:
		permission = "equipment_passports.import"
		if in.Mode != "current_components" {
			return out, passportError(domain.Invalid, "invalid_export")
		}
	case domain.PassportPurposeExportCurrent:
		if in.Mode != "current_components" {
			return out, passportError(domain.Invalid, "invalid_export")
		}
	case domain.PassportPurposeExportHistory:
		if in.Mode != "history" {
			return out, passportError(domain.Invalid, "invalid_export")
		}
	default:
		return out, passportError(domain.Invalid, "invalid_export")
	}
	tx, e := passportTx(ctx, r, true)
	if e != nil {
		return out, e
	}
	defer tx.Rollback(ctx)
	permissions := []domain.Permission{permission}
	if purpose == domain.PassportPurposeExportHistory {
		permissions = append(permissions, "equipment_passports.history.read")
	}
	a, _, e := passportAuthority(ctx, tx, permissions...)
	if e != nil {
		return out, e
	}
	wells, e := passportWells(ctx, tx, policy, a, in.WellIDs, false)
	if e != nil {
		return out, e
	}
	var stamp time.Time
	if e = tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&stamp); e != nil {
		return out, e
	}
	out.ExportedAt = stamp
	if purpose == domain.PassportPurposeExportHistory {
		ids := make([]int64, 0, len(wells))
		for _, w := range wells {
			ids = append(ids, w.ID)
		}
		rows, e := passportHistoryRows(ctx, tx, ids, domain.TableRequest{Query: in.Query, PageSize: 1000})
		if e != nil {
			return out, e
		}
		if rows.HasMore {
			return out, passportError(domain.Invalid, "file_limit_exceeded")
		}
		out.History = rows.Items
	} else {
		for _, w := range wells {
			view, e := passportView(ctx, tx, w)
			if e != nil {
				return out, e
			}
			out.Current = append(out.Current, view)
			out.EmptySlotCount += 3 - len(view.Slots)
			for _, provenance := range view.Provenance {
				v := provenance.LastChange
				out.Provenance = append(out.Provenance, domain.PassportRowProvenance{RowKey: fmt.Sprintf("%s_%s", strconv.FormatInt(w.ID, 10), provenance.Slot), Source: v.Source, ActorID: v.Actor.AccountID, RecordedAt: v.RecordedAt, ReceiptID: v.ReceiptID, SourceReference: v.SourceReference})
			}
		}
	}
	return out, tx.Commit(ctx)
}
