package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/repository/postgres"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

func TestAccessTableHTTPFullGraphAndPreviewReceipt(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	app := New(testConfig(), pool, &fakeML{})
	access, e := service.NewAccess(app.store, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = access.Bootstrap(ctx, accountInput("admin", "admin")); e != nil {
		t.Fatal(e)
	}
	access.WithRBACMode(domain.RBACEnforce)
	app.http.Access = access
	h := app.Handler()
	cookie := loginCookie(t, h, "admin")
	if _, e = pool.Exec(ctx, `INSERT INTO auth_user(login,name,password_hash,role,scope,organization_scope) SELECT 'user'||i,'ӘБВ'||lpad(i::text,3,'0'),'hash','viewer','{"funds":["operational","presentation"],"ngdus":null,"departments":null}'::jsonb,'{"kind":"organization","ngdu_id":null,"department_id":null}'::jsonb FROM generate_series(1,60) i`); e != nil {
		t.Fatal(e)
	}
	for _, query := range []string{`INSERT INTO auth_role(name,description) SELECT 'role'||i,'ӘБВ'||lpad(i::text,3,'0') FROM generate_series(1,60) i`, `INSERT INTO auth_group(name,description) SELECT 'group'||i,'ӘБВ'||lpad(i::text,3,'0') FROM generate_series(1,60) i`} {
		if _, e = pool.Exec(ctx, query); e != nil {
			t.Fatal(e)
		}
	}
	raw := `{"version":1,"filters":[{"field":"name","op":"contains","value":"әбв"}],"sort":[{"field":"name","direction":"desc"}]}`
	path := "/api/v1/auth/users?access_version=1&table_query=" + url.QueryEscape(raw)
	type page struct {
		Items []struct {
			ID   json.Number `json:"id"`
			Name string      `json:"name"`
		}
		NextCursor *string `json:"next_cursor"`
		Total      int     `json:"total_visible"`
		Revision   string  `json:"graph_revision"`
	}
	seen := map[json.Number]bool{}
	cursor := ""
	last := ""
	var revision string
	var firstCursor string
	for {
		var v page
		res := authRequest(t, h, "GET", path+cursor, "", cookie, 200)
		if e = json.Unmarshal(res.Body.Bytes(), &v); e != nil {
			t.Fatal(e)
		}
		revision = v.Revision
		for _, row := range v.Items {
			if seen[row.ID] || last != "" && row.Name >= last {
				t.Fatal("unstable order", row, last)
			}
			seen[row.ID] = true
			last = row.Name
		}
		if v.Total != 60 {
			t.Fatal("full count", v.Total)
		}
		if v.NextCursor == nil {
			break
		}
		if firstCursor == "" {
			firstCursor = *v.NextCursor
		}
		cursor = "&cursor=" + url.QueryEscape(*v.NextCursor)
	}
	if len(seen) != 60 {
		t.Fatal("missing graph rows", len(seen))
	}
	authRequest(t, h, "GET", strings.Replace(path, "desc", "asc", 1)+"&cursor="+url.QueryEscape(firstCursor), "", cookie, 400)
	authRequest(t, h, "GET", path+"&search=old", "", cookie, 400)
	// Query mode remains paged even without the legacy access_version flag.
	var queryOnly page
	res := authRequest(t, h, "GET", strings.Replace(path, "access_version=1&", "", 1), "", cookie, 200)
	if e = json.Unmarshal(res.Body.Bytes(), &queryOnly); e != nil || len(queryOnly.Items) != 25 {
		t.Fatal("query envelope", res.Body.String(), e)
	}
	for _, dataset := range []string{"roles", "groups"} {
		query := `{"version":1,"filters":[{"field":"description","op":"contains","value":"әбв060"}],"sort":[{"field":"name","direction":"desc"}]}`
		var v page
		res = authRequest(t, h, "GET", "/api/v1/auth/"+dataset+"?table_query="+url.QueryEscape(query), "", cookie, 200)
		if e = json.Unmarshal(res.Body.Bytes(), &v); e != nil || len(v.Items) != 1 || v.Total != 1 {
			t.Fatal(dataset, res.Body.String(), e)
		}
	}
	// Current group members are queried globally inside the same authorized graph.
	var groupID int64
	if e = pool.QueryRow(ctx, `SELECT id FROM auth_group WHERE name='group1'`).Scan(&groupID); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO auth_group_member(group_id,user_id) SELECT $1,id FROM auth_user WHERE login LIKE 'user%'`, groupID); e != nil {
		t.Fatal(e)
	}
	memberQuery := `{"version":1,"filters":[{"field":"name","op":"contains","value":"әбв060"}],"sort":[{"field":"active","direction":"asc"}]}`
	var memberPage page
	res = authRequest(t, h, "GET", fmt.Sprintf("/api/v1/auth/groups/%d/members?table_query=%s", groupID, url.QueryEscape(memberQuery)), "", cookie, 200)
	if e = json.Unmarshal(res.Body.Bytes(), &memberPage); e != nil || len(memberPage.Items) != 1 || memberPage.Total != 1 {
		t.Fatal("members full graph", res.Body.String(), e)
	}
	// Preview filtering cannot alter the receipt/aggregate used by pending Apply.
	store := postgres.New(pool)
	var actor, target int64
	if e = pool.QueryRow(ctx, `SELECT id FROM auth_user WHERE login='admin'`).Scan(&actor); e != nil {
		t.Fatal(e)
	}
	if e = pool.QueryRow(ctx, `SELECT id FROM auth_user WHERE login='user60'`).Scan(&target); e != nil {
		t.Fatal(e)
	}
	change := rbacChange(t, store, target)
	change.Active = false
	before, e := store.PreviewAccessChange(ctx, actor, domain.AccessChange{User: &change}, domain.AccessPageQuery{})
	if e != nil {
		t.Fatal(e)
	}
	noMatch := `{"version":1,"filters":[{"field":"user_name","op":"contains","value":"absent"}],"sort":[]}`
	req, e := service.ParseAccessTableRequest(ctx, "access_preview", 0, noMatch, 25, "")
	if e != nil {
		t.Fatal(e)
	}
	after, e := store.PreviewAccessChange(ctx, actor, domain.AccessChange{User: &change}, domain.AccessPageQuery{Table: &req})
	if e != nil {
		t.Fatal(e)
	}
	if len(after.Items) != 0 {
		t.Fatal("preview filter ignored")
	}
	if before.TableFilterOptions != nil || after.TableFilterOptions == nil {
		t.Fatal("preview metadata legacy/query-mode presence")
	}
	before.TableFilterOptions = after.TableFilterOptions
	before.Items = []domain.AccessImpact{}
	before.NextCursor = nil
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("receipt/aggregates changed before=%+v after=%+v", before, after)
	}
	// A prepared group preview spans pages, and its cursor is tied to the immutable change receipt.
	group, e := store.AccessGroup(ctx, actor, groupID)
	if e != nil {
		t.Fatal(e)
	}
	members := []int64{}
	dbRows, e := pool.Query(ctx, `SELECT user_id FROM auth_group_member WHERE group_id=$1 ORDER BY user_id`, groupID)
	if e != nil {
		t.Fatal(e)
	}
	for dbRows.Next() {
		var id int64
		if e = dbRows.Scan(&id); e != nil {
			t.Fatal(e)
		}
		members = append(members, id)
	}
	dbRows.Close()
	if e = dbRows.Err(); e != nil {
		t.Fatal(e)
	}
	groupChange := domain.AccessChange{Group: &domain.GroupAccessChange{ID: &groupID, Revision: group.Revision, Name: group.Name, Description: "prepared", RoleIDs: group.RoleIDs, MemberIDs: members}}
	allQuery := `{"version":1,"filters":[],"sort":[{"field":"user_name","direction":"desc"}]}`
	previewReq, e := service.ParseAccessTableRequest(ctx, "access_preview", 0, allQuery, 25, "")
	if e != nil {
		t.Fatal(e)
	}
	groupPreview, e := store.PreviewAccessChange(ctx, actor, groupChange, domain.AccessPageQuery{Table: &previewReq})
	if e != nil || len(groupPreview.Items) != 25 || groupPreview.NextCursor == nil || groupPreview.AssignedUsers != 60 {
		t.Fatal("paged preview", groupPreview, e)
	}
	nextReq, e := service.ParseAccessTableRequest(ctx, "access_preview", 0, allQuery, 25, *groupPreview.NextCursor)
	if e != nil {
		t.Fatal(e)
	}
	nextPreview, e := store.PreviewAccessChange(ctx, actor, groupChange, domain.AccessPageQuery{Table: &nextReq})
	if e != nil || len(nextPreview.Items) != 25 || nextPreview.Fingerprint != groupPreview.Fingerprint || nextPreview.AssignedUsers != 60 {
		t.Fatal("preview receipt page2", nextPreview, e)
	}
	groupChange.Group.Description = "different prepared change"
	if _, e = store.PreviewAccessChange(ctx, actor, groupChange, domain.AccessPageQuery{Table: &nextReq}); e == nil {
		t.Fatal("cursor accepted another preview receipt")
	}
	if _, e = pool.Exec(ctx, `UPDATE auth_rbac_state SET revision=revision+1`); e != nil {
		t.Fatal(e)
	}
	authRequest(t, h, "GET", path+"&cursor="+url.QueryEscape(firstCursor), "", cookie, 400)
	authRequest(t, h, "GET", path+"&graph_revision="+revision, "", cookie, 400)
}

func TestAccessTableBoundedManagementNeverWidens(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	store := postgres.New(pool)
	actor := rbacReadUser(t, pool, "leader", `{"kind":"department","ngdu_id":1,"department_id":3}`, "users.read", "roles.read", "groups.read")
	allowed := rbacReadUser(t, pool, "allowed", `{"kind":"department","ngdu_id":1,"department_id":3}`)
	_ = rbacReadUser(t, pool, "outside", `{"kind":"department","ngdu_id":1,"department_id":4}`)
	_ = rbacReadUser(t, pool, "privileged", `{"kind":"department","ngdu_id":1,"department_id":3}`, "access.manage_global")
	raw := `{"version":1,"filters":[],"sort":[{"field":"name","direction":"asc"}]}`
	req, e := service.ParseAccessTableRequest(ctx, "access_users", 0, raw, 25, "")
	if e != nil {
		t.Fatal(e)
	}
	page, e := store.ListManagedAccounts(ctx, actor, domain.AccessPageQuery{Table: &req})
	if e != nil {
		t.Fatal(e)
	}
	if len(page.Items) != 2 {
		t.Fatal("scope widened", page.Items)
	}
	found := false
	for _, row := range page.Items {
		if row.ID == allowed {
			found = true
		}
		if row.Login == "outside" || row.Login == "privileged" {
			t.Fatal("unmanageable row", row)
		}
	}
	if !found {
		t.Fatal("manageable row lost")
	}
}

func TestAccessTableFilterOptionsUsesOnlyVisibleAccountSummaries(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	store := postgres.New(pool)
	actor := rbacReadUser(t, pool, "reader", `{"kind":"department","ngdu_id":1,"department_id":3}`, "users.read", "wells.read")
	visible := rbacReadUser(t, pool, "visible", `{"kind":"department","ngdu_id":1,"department_id":3}`, "wells.read")
	hidden := rbacReadUser(t, pool, "hidden", `{"kind":"department","ngdu_id":1,"department_id":4}`, "wells.read")
	var visibleGroup, hiddenGroup int64
	if e := pool.QueryRow(ctx, `INSERT INTO auth_group(name)VALUES('visible group') RETURNING id`).Scan(&visibleGroup); e != nil {
		t.Fatal(e)
	}
	if e := pool.QueryRow(ctx, `INSERT INTO auth_group(name)VALUES('hidden group') RETURNING id`).Scan(&hiddenGroup); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO auth_group_member(group_id,user_id)VALUES($1,$2),($3,$4)`, visibleGroup, visible, hiddenGroup, hidden); e != nil {
		t.Fatal(e)
	}
	account, e := store.Account(ctx, actor)
	if e != nil {
		t.Fatal(e)
	}
	principal := domain.AccountPrincipal(account)
	ctx = domain.WithPrincipal(ctx, principal)
	access, e := service.NewAccess(store, true)
	if e != nil {
		t.Fatal(e)
	}
	// HTTP authorization also accepts users.read without roles.read/groups.read.
	hash, err := bcrypt.GenerateFromPassword([]byte("long-local-test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE auth_user SET password_hash=$1 WHERE id=$2`, string(hash), actor); err != nil {
		t.Fatal(err)
	}
	app := New(testConfig(), pool, &fakeML{})
	app.http.Access = access.WithRBACMode(domain.RBACEnforce)
	handler := app.Handler()
	cookie := loginCookie(t, handler, "reader")
	response := authRequest(t, handler, "GET", "/api/v1/auth/users/filter-options?field=group_ids&search=group", "", cookie, 200)
	var httpOptions domain.AccessFilterOptionsPage
	if err = json.Unmarshal(response.Body.Bytes(), &httpOptions); err != nil || len(httpOptions.Items) != 1 || httpOptions.Items[0].Label != "visible group" {
		t.Fatal(response.Body.String(), err)
	}
	authRequest(t, handler, "GET", "/api/v1/auth/roles", "", cookie, 403)
	authRequest(t, handler, "GET", "/api/v1/auth/users/filter-options?field=permission_keys", "", cookie, 400)
	roles, e := access.UserFilterOptions(ctx, "role_ids", "", 1, "")
	if e != nil || len(roles.Items) != 1 || roles.NextCursor == nil {
		t.Fatal(roles, e)
	}
	next, e := access.UserFilterOptions(ctx, "role_ids", "", 1, *roles.NextCursor)
	if e != nil || len(next.Items) != 1 || next.NextCursor != nil {
		t.Fatal(next, e)
	}
	names := []string{roles.Items[0].Label, next.Items[0].Label}
	if !slices.Contains(names, "reader") || !slices.Contains(names, "visible") || slices.Contains(names, "hidden") {
		t.Fatal("hidden role option", names)
	}
	groups, e := access.UserFilterOptions(ctx, "group_ids", "group", 25, "")
	if e != nil || len(groups.Items) != 1 || groups.Items[0].Label != "visible group" {
		t.Fatal("hidden group option", groups, e)
	}
	if _, e = access.UserFilterOptions(ctx, "role_ids", "different", 1, *roles.NextCursor); e == nil {
		t.Fatal("cursor accepted changed search")
	}
	if _, e = pool.Exec(ctx, `UPDATE auth_rbac_state SET revision=revision+1`); e != nil {
		t.Fatal(e)
	}
	if _, e = access.UserFilterOptions(ctx, "role_ids", "", 1, *roles.NextCursor); e == nil {
		t.Fatal("cursor accepted stale graph")
	}
}

func TestAccessTableGroupFilterOptionsNeedsOnlyGroupsRead(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	store := postgres.New(pool)
	actor := rbacReadUser(t, pool, "groups-reader", `{"kind":"organization","ngdu_id":null,"department_id":null}`, "groups.read")
	var visibleA, visibleB, hidden int64
	for i, name := range []string{"visible-A", "visible-B", "hidden-role"} {
		var role int64
		if e := pool.QueryRow(ctx, `INSERT INTO auth_role(name)VALUES($1) RETURNING id`, name).Scan(&role); e != nil {
			t.Fatal(e)
		}
		switch i {
		case 0:
			visibleA = role
		case 1:
			visibleB = role
		case 2:
			hidden = role
		}
	}
	if _, e := pool.Exec(ctx, `INSERT INTO auth_role_permission(role_id,permission_key)VALUES($1,'wells.read')`, hidden); e != nil {
		t.Fatal(e)
	}
	for i, roles := range [][]int64{{visibleA, visibleB}, {visibleA}, {hidden}} {
		var group int64
		if e := pool.QueryRow(ctx, `INSERT INTO auth_group(name)VALUES($1) RETURNING id`, fmt.Sprintf("group-%d", i)).Scan(&group); e != nil {
			t.Fatal(e)
		}
		for _, role := range roles {
			if _, e := pool.Exec(ctx, `INSERT INTO auth_group_role(group_id,role_id)VALUES($1,$2)`, group, role); e != nil {
				t.Fatal(e)
			}
		}
	}
	account, e := store.Account(ctx, actor)
	if e != nil {
		t.Fatal(e)
	}
	ctx = domain.WithPrincipal(ctx, domain.AccountPrincipal(account))
	access, e := service.NewAccess(store, true)
	if e != nil {
		t.Fatal(e)
	}
	page, e := access.GroupFilterOptions(ctx, "role_ids", "", 1, "")
	if e != nil || len(page.Items) != 1 || page.NextCursor == nil {
		t.Fatal(page, e)
	}
	next, e := access.GroupFilterOptions(ctx, "role_ids", "", 1, *page.NextCursor)
	if e != nil || len(next.Items) != 1 || next.NextCursor != nil {
		t.Fatal(next, e)
	}
	ids := []int64{page.Items[0].Value, next.Items[0].Value}
	if !slices.Contains(ids, visibleA) || !slices.Contains(ids, visibleB) || slices.Contains(ids, hidden) {
		t.Fatal("hidden group ref escaped", ids)
	}
	for _, row := range append(page.Items, next.Items...) {
		if row.Label != "ID "+strconv.FormatInt(row.Value, 10) {
			t.Fatal("unexpected role definition exposed", row)
		}
	}
	if _, e = access.GroupFilterOptions(ctx, "role_ids", "changed", 1, *page.NextCursor); e == nil {
		t.Fatal("cursor accepted changed search")
	}
	hash, e := bcrypt.GenerateFromPassword([]byte("long-local-test-password"), bcrypt.MinCost)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE auth_user SET password_hash=$1 WHERE id=$2`, string(hash), actor); e != nil {
		t.Fatal(e)
	}
	app := New(testConfig(), pool, &fakeML{})
	app.http.Access = access.WithRBACMode(domain.RBACEnforce)
	handler := app.Handler()
	cookie := loginCookie(t, handler, "groups-reader")
	response := authRequest(t, handler, "GET", "/api/v1/auth/groups/filter-options?field=role_ids", "", cookie, 200)
	var options domain.AccessFilterOptionsPage
	if e = json.Unmarshal(response.Body.Bytes(), &options); e != nil || len(options.Items) != 2 {
		t.Fatal(response.Body.String(), e)
	}
	authRequest(t, handler, "GET", "/api/v1/auth/users/filter-options?field=role_ids", "", cookie, 403)
	authRequest(t, handler, "GET", "/api/v1/auth/roles", "", cookie, 403)
	authRequest(t, handler, "GET", "/api/v1/auth/groups/filter-options?field=group_ids", "", cookie, 400)
	if _, e = pool.Exec(ctx, `UPDATE auth_rbac_state SET revision=revision+1`); e != nil {
		t.Fatal(e)
	}
	if _, e = access.GroupFilterOptions(ctx, "role_ids", "", 1, *page.NextCursor); e == nil {
		t.Fatal("cursor accepted stale graph")
	}
}

func TestAccessTablePreviewOptionsWithoutUsersReadAndDraftSources(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	store := postgres.New(pool)
	_ = rbacManager(t, pool)
	actor := rbacReadUser(t, pool, "preview-manager", `{"kind":"organization","ngdu_id":null,"department_id":null}`, "access.manage_global", "groups.read", "groups.manage", "roles.read", "roles.manage", "wells.read")
	const roleID int64 = 9007199254740993
	if _, e := pool.Exec(ctx, `INSERT INTO auth_role(id,name) OVERRIDING SYSTEM VALUE VALUES($1,'Новая роль');`, roleID); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO auth_role_permission(role_id,permission_key)VALUES($1,'wells.read')`, roleID); e != nil {
		t.Fatal(e)
	}
	_ = rbacReadUser(t, pool, "unrelated-hidden-source", `{"kind":"organization","ngdu_id":null,"department_id":null}`, "wells.read")
	members := []int64{}
	memberStrings := []string{}
	for i := 0; i < 30; i++ {
		id := rbacReadUser(t, pool, fmt.Sprintf("impact-%02d", i), `{"kind":"organization","ngdu_id":null,"department_id":null}`)
		members = append(members, id)
		memberStrings = append(memberStrings, strconv.FormatInt(id, 10))
	}
	account, e := store.Account(ctx, actor)
	if e != nil {
		t.Fatal(e)
	}
	ctx = domain.WithPrincipal(ctx, domain.AccountPrincipal(account))
	access, e := service.NewAccess(store, true)
	if e != nil {
		t.Fatal(e)
	}
	change := domain.AccessChange{Group: &domain.GroupAccessChange{Name: "Черновая группа", RoleIDs: []int64{roleID}, MemberIDs: members}}
	all := `{"version":1,"filters":[],"sort":[{"field":"user_name","direction":"asc"}]}`
	req, e := service.ParseAccessTableRequest(ctx, "access_preview", 0, all, 25, "")
	if e != nil {
		t.Fatal(e)
	}
	first, e := access.Preview(ctx, change, domain.AccessPageQuery{Table: &req})
	if e != nil || len(first.Items) != 25 || first.NextCursor == nil || first.AssignedUsers != 30 {
		t.Fatal(first, e)
	}
	want := &domain.AccessPreviewFilterOptions{SourceRoleIDs: []domain.AccessFilterOption{{Value: roleID, Label: "Новая роль"}}, SourceGroupIDs: []domain.AccessFilterOption{{Value: -1, Label: "Черновая группа"}}}
	if !reflect.DeepEqual(first.TableFilterOptions, want) {
		t.Fatalf("draft/full source metadata %+v want %+v", first.TableFilterOptions, want)
	}
	req, e = service.ParseAccessTableRequest(ctx, "access_preview", 0, all, 25, *first.NextCursor)
	if e != nil {
		t.Fatal(e)
	}
	next, e := access.Preview(ctx, change, domain.AccessPageQuery{Table: &req})
	if e != nil || len(next.Items) != 5 || !reflect.DeepEqual(next.TableFilterOptions, want) || next.Fingerprint != first.Fingerprint {
		t.Fatal("metadata changed page", next, e)
	}
	noMatch := `{"version":1,"filters":[{"field":"user_name","op":"contains","value":"no-match"}],"sort":[]}`
	req, e = service.ParseAccessTableRequest(ctx, "access_preview", 0, noMatch, 25, "")
	if e != nil {
		t.Fatal(e)
	}
	filtered, e := access.Preview(ctx, change, domain.AccessPageQuery{Table: &req})
	if e != nil || len(filtered.Items) != 0 || !reflect.DeepEqual(filtered.TableFilterOptions, want) || filtered.Fingerprint != first.Fingerprint || filtered.AssignedUsers != 30 || filtered.ChangedUsers != first.ChangedUsers {
		t.Fatal("metadata or receipt changed filter", filtered, e)
	}
	hash, e := bcrypt.GenerateFromPassword([]byte("long-local-test-password"), bcrypt.MinCost)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE auth_user SET password_hash=$1 WHERE id=$2`, string(hash), actor); e != nil {
		t.Fatal(e)
	}
	app := New(testConfig(), pool, &fakeML{})
	app.http.Access = access.WithRBACMode(domain.RBACEnforce)
	handler := app.Handler()
	cookie := loginCookie(t, handler, "preview-manager")
	body, e := json.Marshal(map[string]any{"access_version": 1, "change": map[string]any{"group": map[string]any{"name": "Черновая группа", "role_ids": []string{strconv.FormatInt(roleID, 10)}, "member_ids": memberStrings}}})
	if e != nil {
		t.Fatal(e)
	}
	res := authRequest(t, handler, "POST", "/api/v1/auth/access/preview?table_query="+url.QueryEscape(noMatch), string(body), cookie, 200)
	var wire struct {
		Options     *domain.AccessPreviewFilterOptions `json:"table_filter_options"`
		Fingerprint string                             `json:"fingerprint"`
	}
	if e = json.Unmarshal(res.Body.Bytes(), &wire); e != nil || !reflect.DeepEqual(wire.Options, want) || wire.Fingerprint != first.Fingerprint {
		t.Fatal(res.Body.String(), e)
	}
	legacy := authRequest(t, handler, "POST", "/api/v1/auth/access/preview", string(body), cookie, 200)
	var legacyFields map[string]json.RawMessage
	if e = json.Unmarshal(legacy.Body.Bytes(), &legacyFields); e != nil {
		t.Fatal(e)
	}
	if _, ok := legacyFields["table_filter_options"]; ok {
		t.Fatal("legacy response shape changed", legacy.Body.String())
	}
	authRequest(t, handler, "GET", "/api/v1/auth/users/filter-options?field=role_ids", "", cookie, 403)
}

func TestAccessTableOrganizationFilterOptionsNeedsOnlyUsersRead(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	store := postgres.New(pool)
	actor := rbacReadUser(t, pool, "org-reader", `{"kind":"ngdu","ngdu_id":1,"department_id":null}`, "users.read", "wells.read")
	for i := 0; i < 30; i++ {
		code := int64(i)
		if i == 1 {
			code = -900
		}
		rbacReadUser(t, pool, fmt.Sprintf("org-visible-%02d", i), fmt.Sprintf(`{"kind":"department","ngdu_id":1,"department_id":%d}`, code), "wells.read")
	}
	rbacReadUser(t, pool, "org-outside", `{"kind":"department","ngdu_id":2,"department_id":999}`, "wells.read")
	rbacReadUser(t, pool, "org-privileged", `{"kind":"department","ngdu_id":1,"department_id":777}`, "wells.read", "access.manage_global")
	hash, err := bcrypt.GenerateFromPassword([]byte("long-local-test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE auth_user SET password_hash=$1 WHERE id=$2`, string(hash), actor); err != nil {
		t.Fatal(err)
	}
	access, err := service.NewAccess(store, true)
	if err != nil {
		t.Fatal(err)
	}
	app := New(testConfig(), pool, &fakeML{})
	app.http.Access = access.WithRBACMode(domain.RBACEnforce)
	handler := app.Handler()
	cookie := loginCookie(t, handler, "org-reader")
	authRequest(t, handler, "GET", "/api/v1/auth/organization-options", "", cookie, 403)
	type wirePage struct {
		Items []struct {
			Value string `json:"value"`
			Label string `json:"label"`
		} `json:"items"`
		Next *string `json:"next_cursor"`
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		path := "/api/v1/auth/users/filter-options?field=department_id&page_size=7"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		response := authRequest(t, handler, "GET", path, "", cookie, 200)
		var page wirePage
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		for _, option := range page.Items {
			if option.Value == "999" || option.Value == "777" || seen[option.Value] || option.Label != "ID "+option.Value {
				t.Fatal("scope/duplicate/label", option)
			}
			seen[option.Value] = true
		}
		if page.Next == nil {
			break
		}
		cursor = *page.Next
	}
	if len(seen) != 30 || !seen["0"] || !seen["-900"] {
		t.Fatal("full signed codes lost", seen)
	}
	response := authRequest(t, handler, "GET", "/api/v1/auth/users/filter-options?field=ngdu_id", "", cookie, 200)
	var ngdu wirePage
	if err := json.Unmarshal(response.Body.Bytes(), &ngdu); err != nil || len(ngdu.Items) != 1 || ngdu.Items[0].Value != "1" {
		t.Fatal("outside ngdu option", response.Body.String(), err)
	}
}
