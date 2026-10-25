package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/effitech-pulse/pulse-backend/internal/adapter/spreadsheet"
	"github.com/effitech-pulse/pulse-backend/internal/domain"
	"github.com/effitech-pulse/pulse-backend/internal/service"
)

// TestEquipmentPassportHTTPRoundTrip uses the production router, personal session,
// service, PostgreSQL and codec; migrations/DB execution belong to the root gate.
func TestEquipmentPassportHTTPRoundTrip(t *testing.T) {
	pool := testDB(t)
	ctx := context.Background()
	app := New(testConfig(), pool, &fakeML{})
	access, err := service.NewAccess(app.store, true)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := access.Bootstrap(ctx, accountInput("passport-owner", "admin"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := access.Create(domain.WithPrincipal(ctx, domain.AccountPrincipal(admin)), accountInput("passport-other", "admin"))
	if err != nil {
		t.Fatal(err)
	}
	// Test-only explicit grants: enrollment itself has a separate acceptance suite.
	_, err = pool.Exec(ctx, `INSERT INTO well(ngdu,department)VALUES(0,0);
 INSERT INTO auth_role(name)VALUES('passport-http');
 INSERT INTO auth_role_permission SELECT id,k FROM auth_role CROSS JOIN unnest(ARRAY['wells.read','equipment_passports.read','equipment_passports.update','equipment_passports.import','equipment_passports.export','equipment_passports.history.read'])k WHERE name='passport-http'`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO auth_user_role SELECT u.id,r.id FROM auth_user u CROSS JOIN auth_role r WHERE u.id=ANY($1) AND r.name='passport-http'`, []int64{admin.ID, other.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err = app.store.ValidateEquipmentPassports(ctx); err != nil {
		t.Fatal(err)
	}
	access.WithRBACMode(domain.RBACEnforce)
	app.http.Access = access
	app.http.EquipmentPassports, err = service.NewEquipmentPassports(app.store, spreadsheet.PassportCodec{}, service.PassportOptions{AppScope: "research"})
	if err != nil {
		t.Fatal(err)
	}
	h := app.Handler()
	owner := loginCookie(t, h, "passport-owner")
	stranger := loginCookie(t, h, "passport-other")
	read := func(path string, cookie *http.Cookie, want int) *httptest.ResponseRecorder {
		return authRequest(t, h, "GET", path, "", cookie, want)
	}
	var preview domain.PassportPreview
	raw, _ := json.Marshal(equipmentRows(equipmentRegister(1)))
	w := authRequest(t, h, "POST", "/api/v1/equipment-passports/previews", string(raw), owner, 200)
	if err = json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	previewPath := fmt.Sprintf("/api/v1/equipment-passports/previews/%d", preview.ID)
	read(previewPath, stranger, 404)
	authRequest(t, h, "DELETE", previewPath, "", stranger, 404)
	commit := func(v domain.PassportPreview, key string, want int) domain.PassportReceipt {
		t.Helper()
		body, _ := json.Marshal(struct {
			Digest string `json:"digest"`
			Ack    bool   `json:"acknowledge_warnings"`
		}{v.Digest, true})
		req := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/equipment-passports/previews/%d/commit", v.ID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", key)
		req.AddCookie(owner)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		if out.Code != want {
			t.Fatalf("commit %d: %s", out.Code, out.Body.String())
		}
		var receipt domain.PassportReceipt
		if want == 200 {
			if err := json.Unmarshal(out.Body.Bytes(), &receipt); err != nil {
				t.Fatal(err)
			}
		}
		return receipt
	}
	key := uuid.NewString()
	receipt := commit(preview, key, 200)
	replay := commit(preview, key, 200)
	if replay.ID != receipt.ID {
		t.Fatal("replay duplicated receipt")
	}
	read(previewPath, owner, 410)
	commit(preview, uuid.NewString(), 410)
	receiptPath := fmt.Sprintf("/api/v1/equipment-passports/receipts/%d", receipt.ID)
	read(receiptPath, stranger, 404)
	read(receiptPath, owner, 200)
	exported := authRequest(t, h, "POST", "/api/v1/equipment-passports/export", `{"well_ids":["1"],"mode":"current_components"}`, owner, 200)
	if exported.Header().Get("Content-Type") != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Fatal(exported.Header())
	}
	var upload bytes.Buffer
	mw := multipart.NewWriter(&upload)
	part, _ := mw.CreateFormFile("file", "passport.xlsx")
	_, _ = part.Write(exported.Body.Bytes())
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/api/v1/equipment-passports/import/previews", &upload)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(owner)
	out := httptest.NewRecorder()
	h.ServeHTTP(out, req)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	if err = json.Unmarshal(out.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.ChangedCount != 0 || preview.UnchangedCount != 1 {
		t.Fatal("roundtrip changed records", preview)
	}
	imported := commit(preview, uuid.NewString(), 200)
	if imported.Source != "excel" || imported.SourceFileSHA256 == nil || imported.ChangedCount != 0 {
		t.Fatal(imported)
	}
	if equipmentCount(t, pool, "equipment_passport_event") != 1 {
		t.Fatal("no-op wrote event")
	}
	read("/api/v1/wells/1/equipment-passport/history?limit=1", owner, 200)
	read("/api/v1/wells/1/equipment-passport/history?limit=1&page_size=1", owner, 400)
	// Template requires import, independently of export.
	_, err = pool.Exec(ctx, `DELETE FROM auth_role_permission WHERE role_id=(SELECT id FROM auth_role WHERE name='passport-http') AND permission_key='equipment_passports.export'`)
	if err != nil {
		t.Fatal(err)
	}
	authRequest(t, h, "POST", "/api/v1/equipment-passports/template", `{"well_ids":["1"]}`, owner, 200)
	authRequest(t, h, "POST", "/api/v1/equipment-passports/export", `{"well_ids":["1"],"mode":"current_components"}`, owner, 403)
	_, err = pool.Exec(ctx, `INSERT INTO auth_role_permission SELECT id,'equipment_passports.export' FROM auth_role WHERE name='passport-http'; DELETE FROM auth_role_permission WHERE role_id=(SELECT id FROM auth_role WHERE name='passport-http') AND permission_key='equipment_passports.history.read'`)
	if err != nil {
		t.Fatal(err)
	}
	authRequest(t, h, "POST", "/api/v1/equipment-passports/export", `{"well_ids":["1"],"mode":"history"}`, owner, 403)
	authRequest(t, h, "POST", "/api/v1/equipment-passports/export", `{"well_ids":["1"],"mode":"current_components"}`, owner, 200)
	// Receipt and current values recheck current scope on every read.
	_, err = pool.Exec(ctx, `UPDATE auth_user SET organization_scope='{"kind":"none","ngdu_id":null,"department_id":null}' WHERE id=$1`, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	read(receiptPath, owner, 404)
	read("/api/v1/wells/1/equipment-passport", owner, 404)
}
