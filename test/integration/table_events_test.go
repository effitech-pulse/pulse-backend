package integration

import (
	"fmt"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
)

func TestTableEventsGlobalUnionKeysAndPermissionIntersection(t *testing.T) {
	pool := testDB(t)
	eventsWell(t, pool, 1, 1)
	eventsWell(t, pool, 2, 2)
	for i := 0; i < 35; i++ {
		eventsCheck(t, pool, 1, fmt.Sprintf("event-check-%02d", i))
		eventsExec(t, pool, `INSERT INTO well_event(well_id,revision,created_at,kind,actor,reason,before_state,after_state) VALUES (1,$1,$2,'metadata','{"id":"shared","authenticated":false,"name":"Технолог"}','fixture','{}','{}')`, i+1, eventsAt)
	}
	eventsCheck(t, pool, 2, "forbidden")
	store := postgres.New(pool)
	actor := rbacReadUser(t, pool, "table-events-reader", `{"kind":"ngdu","ngdu_id":1,"department_id":null}`, "wells.read", "checks.read", "wells.history.read")
	ctx := reviewActor(t, store, actor)
	svc := eventsService(t, pool, nil, "research")
	raw := `{"version":1,"filters":[{"field":"occurred_at","op":"range","from":"2026-10-22T15:00:01.123456+03:00","to":"2026-10-22T12:00:01.123456Z"}],"sort":[{"field":"actor_name","direction":"desc"},{"field":"event_type","direction":"asc"}]}`
	cursor := ""
	seen := map[string]bool{}
	unknown := false
	for {
		page, err := svc.ListTable(ctx, raw, 7, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Items {
			if row.Well.ID != 1 || seen[row.EventID] {
				t.Fatal("scope/duplicate", row)
			}
			seen[row.EventID] = true
			if row.Actor.Name == nil {
				unknown = true
			} else if unknown {
				t.Fatal("known actor after null")
			}
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 70 {
		t.Fatal("global UNION count", len(seen))
	}
	if _, err := svc.ListTable(ctx, `{"version":1,"filters":[],"sort":[]}`, 7, cursor); err == nil {
		t.Fatal("cursor query replay")
	}
}
