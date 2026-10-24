package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestTablePeopleGlobalSafeProjectionAndCurrentEpoch(t *testing.T) {
	pool := testDB(t)
	store := postgres.New(pool)
	actor := rbacReadUser(t, pool, "people-reader", `{"kind":"none","ngdu_id":null,"department_id":null}`)
	ctx := reviewActor(t, store, actor)
	svc, err := service.NewAccess(store, true)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 33; i++ {
		var title *string
		if i >= 26 {
			s := "ӘӨҮҗңһ"
			title = &s
		}
		if _, err := pool.Exec(context.Background(), `INSERT INTO auth_user(login,name,password_hash,role,scope,job_title,active) VALUES ($1,$2,'SECRET','viewer','{"funds":["operational"]}',$3,$4)`, fmt.Sprintf("fixture%d", i), fmt.Sprintf("Person%02d", i), title, i%2 == 0); err != nil {
			t.Fatal(err)
		}
	}
	raw := `{"version":1,"filters":[{"field":"job_title","op":"contains","value":"әөүҖҢҺ"}],"sort":[{"field":"active","direction":"asc"},{"field":"name","direction":"desc"}]}`
	cursor := ""
	seen := map[int64]bool{}
	activeSeen := false
	for {
		page, err := svc.ListPeopleTable(ctx, raw, 3, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range page.Items {
			if seen[row.ID] {
				t.Fatal("duplicate")
			}
			seen[row.ID] = true
			if row.Active {
				activeSeen = true
			} else if activeSeen {
				t.Fatal("false coerced to missing")
			}
			out, _ := json.Marshal(row)
			if strings.Contains(string(out), "login") || strings.Contains(string(out), "SECRET") || strings.Contains(string(out), "permission") {
				t.Fatal("unsafe People projection", string(out))
			}
		}
		if page.NextCursor == nil {
			break
		}
		cursor = *page.NextCursor
	}
	if len(seen) != 7 {
		t.Fatal("not global", len(seen))
	}
	if _, err := pool.Exec(context.Background(), `UPDATE auth_user SET authz_epoch=authz_epoch+1 WHERE id=$1`, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListPeopleTable(ctx, raw, 3, ""); err == nil {
		t.Fatal("revoked People reader admitted")
	}
}
