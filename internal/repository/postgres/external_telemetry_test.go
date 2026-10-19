package postgres

import (
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestExternalQueryQuotesIdentifiersAndBindsValues(t *testing.T) {
	s := domain.TelemetrySource{TableName: "public.measurements", TimeField: "measured_at"}
	builder, err := externalQuery(s, time.Unix(0, 0), `"`)
	if err != nil {
		t.Fatal(err)
	}
	q, args, err := build(builder)
	if err != nil || len(args) != 1 || !strings.Contains(q, `FROM "public"."measurements"`) || !strings.Contains(q, "$1") {
		t.Fatal(q, args, err)
	}
	s.TableName = "measurements; DROP TABLE well"
	if _, err = externalQuery(s, time.Now(), `"`); err == nil {
		t.Fatal("SQL identifier injection accepted")
	}
}
