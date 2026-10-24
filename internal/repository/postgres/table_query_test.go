package postgres

import (
	"encoding/json"
	"strings"
	"testing"

	sq "github.com/Masterminds/squirrel"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

// Losing a secondary key or NULL branch breaks pagination across tied values.
func TestTableSQLLexicographicNullLastAndParameters(t *testing.T) {
	req := domain.TableRequest{Query: domain.TableQuery{Version: 1, Sort: []domain.TableSort{{Field: "actor", Direction: "asc"}, {Field: "ts", Direction: "desc"}}, Filters: []domain.TableFilter{{Field: "path", Op: "contains", Value: tablePtr("%_' OR true")}}}, PageSize: 2, After: []json.RawMessage{json.RawMessage(`"Alice"`), json.RawMessage(`"2026-10-24T10:00:00Z"`), json.RawMessage(`12`)}}
	query, err := applyTableQuery(sqlBuilder.Select("id").From("action_log"), "audit", req)
	if err != nil {
		t.Fatal(err)
	}
	sql, args, err := build(query)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sql, "OR true") || !strings.Contains(sql, "COLLATE \"C\" ASC NULLS LAST, ts DESC NULLS LAST, id DESC NULLS LAST") || !strings.Contains(sql, "IS NULL") || len(args) < 4 {
		t.Fatalf("query=%s args=%v", sql, args)
	}
}
func tablePtr(v string) *string { return &v }
func TestTableSQLRejectsUnknownDatasetAndCursorShape(t *testing.T) {
	if _, err := applyTableQuery(sq.Select("id"), "nope", domain.TableRequest{}); err == nil {
		t.Fatal("accepted unknown dataset")
	}
	if _, err := applyTableQuery(sq.Select("id"), "audit", domain.TableRequest{PageSize: 25, After: []json.RawMessage{json.RawMessage(`1`)}}); err == nil {
		t.Fatal("accepted truncated cursor")
	}
}

func TestTableNumericRangeBindsDecimalWithoutIntegerCast(t *testing.T) {
	req := domain.TableRequest{PageSize: 25, Query: domain.TableQuery{Filters: []domain.TableFilter{{Field: "accepted", Op: "range", From: json.RawMessage(`1.5`)}}}}
	query, err := applyTableQuery(sqlBuilder.Select("id").From("telemetry_collection_log"), "ingestlogs", req)
	if err != nil {
		t.Fatal(err)
	}
	sql, args, err := build(query)
	if err != nil || !strings.Contains(sql, "accepted >= $1::numeric") || len(args) != 1 || args[0] != "1.5" {
		t.Fatalf("decimal threshold should compare as numeric: %s %v %v", sql, args, err)
	}
}

func TestTableEventsWellIDSortIsExactNumeric(t *testing.T) {
	q, err := applyTableQuery(sqlBuilder.Select("*").From("events"), "events", domain.TableRequest{PageSize: 25, Query: domain.TableQuery{Sort: []domain.TableSort{{Field: "well_id", Direction: "asc"}}}})
	if err != nil {
		t.Fatal(err)
	}
	sql, _, err := build(q)
	if err != nil || !strings.Contains(sql, "ORDER BY well_id ASC NULLS LAST, source_rank DESC NULLS LAST, target_key COLLATE \"C\" DESC NULLS LAST, revision DESC NULLS LAST") {
		t.Fatalf("numeric Pulse ID order lost: %s %v", sql, err)
	}
}
