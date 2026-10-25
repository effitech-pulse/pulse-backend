package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
)

func TestTableQueryRejectsInvalidInput(t *testing.T) {
	cases := []string{
		`{"version":2,"filters":[],"sort":[]}`,
		`{"version":1,"filters":[],"sort":[{"field":"ts; DROP TABLE well","direction":"asc"}]}`,
		`{"version":1,"filters":[{"field":"ts","op":"range","from":"2026-10-24T12:00:00Z"}],"sort":[]}`,
		`{"version":1,"filters":[{"field":"status_code","op":"range","from":500,"to":200}],"sort":[]}`,
		`{"version":1,"filters":[{"field":"status_code","op":"in","values":[false]}],"sort":[]}`,
		`{"version":1,"filters":[],"sort":[{"field":"actor","direction":"asc"},{"field":"actor","direction":"desc"}]}`,
		`{"version":1,"filters":[],"sort":[],"sql":"true"}`,
	}
	for _, raw := range cases {
		if _, err := parseTableRequest(context.Background(), "audit", 0, raw, 25, ""); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestTableCursorBindsQueryParentAndPrincipal(t *testing.T) {
	ctx := context.Background()
	raw := `{"version":1,"filters":[],"sort":[{"field":"created_at","direction":"desc"}]}`
	req, err := parseTableRequest(ctx, "manualchecks", 12, raw, 25, "")
	if err != nil {
		t.Fatal(err)
	}
	token, err := encodeTableCursor(req, []byte(`["2026-10-24T10:00:00Z","abc"]`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = parseTableRequest(ctx, "manualchecks", 12, raw, 25, token); err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct {
		parent int64
		raw    string
		token  string
	}{{13, raw, token}, {12, strings.Replace(raw, "desc", "asc", 1), token}, {12, raw, token + "x"}} {
		if _, err = parseTableRequest(ctx, "manualchecks", p.parent, p.raw, 25, p.token); err == nil {
			t.Error("accepted mismatched cursor")
		}
	}
}

func TestTableQueryExactIntegerRangeAndCanonicalSets(t *testing.T) {
	ctx := context.Background()
	if _, err := parseTableRequest(ctx, "ingestlogs", 0, `{"version":1,"filters":[{"field":"accepted","op":"range","from":9007199254740993,"to":9007199254740992}],"sort":[]}`, 25, ""); err == nil {
		t.Fatal("rounded integer range accepted")
	}
	a, err := parseTableRequest(ctx, "audit", 0, `{"version":1,"filters":[{"field":"method","op":"in","values":["POST","GET","GET"]},{"field":"actor","op":"in","values":[]}],"sort":[]}`, 25, "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := parseTableRequest(ctx, "audit", 0, `{"version":1,"filters":[{"field":"method","op":"in","values":["GET","POST"]}],"sort":[]}`, 25, "")
	if err != nil || a.Fingerprint != b.Fingerprint {
		t.Fatal("equivalent sets not normalized", err)
	}
}

func TestTableCursorDistinguishesAccountScopeAndPageSize(t *testing.T) {
	id := int64(1)
	all := domain.Principal{AccountID: &id, Authenticated: true, Mechanism: "session", Role: "technologist", Scope: domain.AccessScope{Funds: []string{"operational"}}}
	ctx := domain.WithPrincipal(context.Background(), all)
	raw := `{"version":1,"filters":[],"sort":[]}`
	req, err := parseTableRequest(ctx, "audit", 0, raw, 25, "")
	if err != nil {
		t.Fatal(err)
	}
	token, err := encodeTableCursor(req, []byte(`["2026-10-24T10:00:00",1]`))
	if err != nil {
		t.Fatal(err)
	}
	empty := []int64{}
	restricted := all
	restricted.Scope.NGDUs = &empty
	if _, err := parseTableRequest(domain.WithPrincipal(context.Background(), restricted), "audit", 0, raw, 25, token); err == nil {
		t.Fatal("nil all and empty none scope conflated")
	}
	other := int64(2)
	otherActor := all
	otherActor.AccountID = &other
	if _, err := parseTableRequest(domain.WithPrincipal(context.Background(), otherActor), "audit", 0, raw, 25, token); err == nil {
		t.Fatal("other actor accepted")
	}
	if _, err := parseTableRequest(ctx, "audit", 0, raw, 50, token); err == nil {
		t.Fatal("changed page size accepted")
	}
}

func TestTableAuditWallClockNeverInventsTimezone(t *testing.T) {
	raw := `{"version":1,"filters":[{"field":"ts","op":"range","from":"2026-10-24T10:00:00.123456","to":"2026-10-24T10:00:00.123457"}],"sort":[]}`
	req, err := parseTableRequest(context.Background(), "audit", 0, raw, 25, "")
	if err != nil {
		t.Fatal("valid unspecified-zone wall clock rejected", err)
	}
	if string(req.Query.Filters[0].From) != `"2026-10-24T10:00:00.123456"` {
		t.Fatal("zone/time changed", string(req.Query.Filters[0].From))
	}
	if _, err := parseTableRequest(context.Background(), "audit", 0, strings.ReplaceAll(raw, ".123456\"", ".123456Z\""), 25, ""); err == nil {
		t.Fatal("offset assigned to unknown audit zone")
	}
}

func TestTableTimeBoundsRejectUnrepresentableSubmicroseconds(t *testing.T) {
	for _, stamp := range []string{"2026-10-24T10:00:00.1234561Z", "2026-10-24T10:00:00.1234569Z", "2026-10-24T10:00:00.1234560001Z"} {
		raw := `{"version":1,"filters":[{"field":"created_at","op":"range","from":"` + stamp + `"}],"sort":[]}`
		if _, err := parseTableRequest(context.Background(), "manualchecks", 1, raw, 25, ""); err == nil {
			t.Errorf("would round PG bound %s", stamp)
		}
	}
	for _, stamp := range []string{"2026-10-24T10:00:00.123456Z", "2026-10-24T13:00:00.123456000+03:00"} {
		raw := `{"version":1,"filters":[{"field":"created_at","op":"range","to":"` + stamp + `"}],"sort":[]}`
		req, err := parseTableRequest(context.Background(), "manualchecks", 1, raw, 25, "")
		if err != nil || string(req.Query.Filters[0].To) != `"2026-10-24T10:00:00.123456Z"` {
			t.Fatal("exact microsecond rejected", stamp, req, err)
		}
	}
}

func TestTableNumericRangeAcceptsExactCanonicalDecimalString(t *testing.T) {
	raw := `{"version":1,"filters":[{"field":"accepted","op":"range","from":"9007199254740993","to":"9007199254740994"}],"sort":[]}`
	req, err := parseTableRequest(context.Background(), "ingestlogs", 0, raw, 25, "")
	if err != nil {
		t.Fatal(err)
	}
	if string(req.Query.Filters[0].From) != "9007199254740993" || string(req.Query.Filters[0].To) != "9007199254740994" {
		t.Fatal("decimal string rounded", req.Query.Filters)
	}
	reversed := strings.Replace(raw, `"from":"9007199254740993","to":"9007199254740994"`, `"from":"9007199254740994","to":"9007199254740993"`, 1)
	if _, err := parseTableRequest(context.Background(), "ingestlogs", 0, reversed, 25, ""); err == nil {
		t.Fatal("reversed exact string range accepted")
	}
	for _, bad := range []string{"+1", "01", "NaN", "1e3"} {
		if _, err := tableValue(json.RawMessage(`"`+bad+`"`), "number"); err == nil {
			t.Error("noncanonical numeric string", bad)
		}
	}
}
