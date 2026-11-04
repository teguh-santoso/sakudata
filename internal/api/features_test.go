package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDatabaseOpaqueIDAndHardening(t *testing.T) {
	s, _ := testServer(t)
	_, out := do(s, "POST", "/api/admin/setup", "", `{"email":"a@x.id","password":"Rahasia123"}`)
	tok := out["token"].(string)

	code, db := do(s, "POST", "/api/admin/databases", tok, `{"name":"app"}`)
	if code != 200 {
		t.Fatalf("create db: %d", code)
	}
	id, _ := db["id"].(string)
	if id == "" || id == "app" {
		t.Fatalf("expected opaque id, got %v", db)
	}
	if code, _ := do(s, "GET", "/api/d/"+id+"/collections", tok, ""); code != 200 {
		t.Fatalf("id-based URL: %d", code)
	}

	// Anonymous callers get 401 for a missing collection, not a 404 that would
	// reveal whether it exists.
	if code, _ := do(s, "GET", "/api/d/"+id+"/c/nope/records", "", ""); code != 401 {
		t.Fatalf("anonymous missing collection = %d, want 401", code)
	}
	// Same for an unknown database ID.
	if code, _ := do(s, "GET", "/api/d/doesnotexist/c/nope/records", "", ""); code != 401 {
		t.Fatalf("anonymous missing db = %d, want 401", code)
	}
}

func TestBackupLifecycle(t *testing.T) {
	s, _ := testServer(t)
	_, out := do(s, "POST", "/api/admin/setup", "", `{"email":"a@x.id","password":"Rahasia123"}`)
	tok := out["token"].(string)
	do(s, "POST", "/api/admin/databases", tok, `{"name":"app"}`)

	code, item := do(s, "POST", "/api/admin/backups", tok, "")
	if code != 200 {
		t.Fatalf("create backup: %d %v", code, item)
	}
	name, _ := item["name"].(string)
	if name == "" {
		t.Fatalf("backup name missing: %v", item)
	}

	code, list := do(s, "GET", "/api/admin/backups", tok, "")
	if code != 200 {
		t.Fatalf("list backups: %d", code)
	}
	found := false
	if arr, ok := list["items"].([]any); ok {
		for _, it := range arr {
			if m, ok := it.(map[string]any); ok && m["name"] == name {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("backup not listed: %v", list)
	}

	req := httptest.NewRequest("GET", "/api/admin/backups/"+name, nil)
	req.Header.Set("Authorization", tok)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.Len() == 0 {
		t.Fatalf("download backup: %d (%d bytes)", rec.Code, rec.Body.Len())
	}

	if code, _ := do(s, "DELETE", "/api/admin/backups/"+name, tok, ""); code != 204 {
		t.Fatalf("delete backup: %d", code)
	}
}

func TestAPITokens(t *testing.T) {
	s, _ := testServer(t)
	_, out := do(s, "POST", "/api/admin/setup", "", `{"email":"a@x.id","password":"Rahasia123"}`)
	admin := out["token"].(string)

	_, a := do(s, "POST", "/api/admin/databases", admin, `{"name":"appa"}`)
	_, b := do(s, "POST", "/api/admin/databases", admin, `{"name":"appb"}`)
	idA, idB := a["id"].(string), b["id"].(string)
	do(s, "POST", "/api/d/"+idA+"/collections", admin,
		`{"name":"notes","type":"base","fields":[{"name":"title","type":"text","required":true}]}`)
	do(s, "POST", "/api/d/"+idB+"/collections", admin,
		`{"name":"notes","type":"base","fields":[{"name":"title","type":"text","required":true}]}`)

	code, created := do(s, "POST", "/api/admin/tokens", admin, `{"name":"machine","dbId":"`+idA+`"}`)
	if code != 200 {
		t.Fatalf("create token: %d %v", code, created)
	}
	token, _ := created["token"].(string)
	if !strings.HasPrefix(token, "mdb_") {
		t.Fatalf("unexpected token: %q", token)
	}

	// Token can use the data API without a JWT.
	if code, _ := do(s, "POST", "/api/d/"+idA+"/c/notes/records", token, `{"title":"via token"}`); code != 200 {
		t.Fatalf("token record create: %d", code)
	}
	if code, _ := do(s, "GET", "/api/d/"+idA+"/c/notes/records", token, ""); code != 200 {
		t.Fatalf("token record list: %d", code)
	}
	// Scoped to one database.
	if code, _ := do(s, "GET", "/api/d/"+idB+"/c/notes/records", token, ""); code != 403 {
		t.Fatalf("cross-db token = %d, want 403", code)
	}
	// Not allowed to manage schema/admin routes.
	if code, _ := do(s, "GET", "/api/d/"+idA+"/collections", token, ""); code != 401 {
		t.Fatalf("token admin route = %d, want 401", code)
	}

	// X-API-Key header also works.
	req := httptest.NewRequest("GET", "/api/d/"+idA+"/c/notes/records", nil)
	req.Header.Set("X-API-Key", token)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("x-api-key: %d", rec.Code)
	}

	// Revocation is immediate.
	id := created["id"].(string)
	if code, _ := do(s, "DELETE", "/api/admin/tokens/"+id, admin, ""); code != 204 {
		t.Fatalf("revoke: %d", code)
	}
	if code, _ := do(s, "GET", "/api/d/"+idA+"/c/notes/records", token, ""); code != 401 {
		t.Fatalf("revoked token = %d, want 401", code)
	}
}

func TestSQLConsole(t *testing.T) {
	s, _ := testServer(t)
	_, out := do(s, "POST", "/api/admin/setup", "", `{"email":"a@x.id","password":"Rahasia123"}`)
	tok := out["token"].(string)
	_, db := do(s, "POST", "/api/admin/databases", tok, `{"name":"app"}`)
	id := db["id"].(string)
	endpoint := "/api/d/" + id + "/sql"

	code, res := do(s, "POST", endpoint, tok,
		`{"query":"CREATE TABLE notes(id TEXT PRIMARY KEY, title TEXT)"}`)
	if code != 200 || res["type"] != "exec" {
		t.Fatalf("create table via sql: %d %v", code, res)
	}
	if code, _ := do(s, "POST", endpoint, tok,
		`{"query":"INSERT INTO notes VALUES ('1','hello')"}`); code != 200 {
		t.Fatalf("insert: %d", code)
	}
	code, res = do(s, "POST", endpoint, tok, `{"query":"SELECT id,title FROM notes"}`)
	if code != 200 || res["type"] != "query" {
		t.Fatalf("select: %d %v", code, res)
	}
	rows, _ := res["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("rows = %v", res["rows"])
	}
	first := rows[0].([]any)
	if first[0] != "1" || first[1] != "hello" {
		t.Fatalf("row = %v", first)
	}
	if code, _ := do(s, "POST", endpoint, tok, `{"query":"SELECT * FROM missing"}`); code != 400 {
		t.Fatalf("bad sql = %d, want 400", code)
	}
	// Non-admin access is rejected.
	if code, _ := do(s, "POST", endpoint, "", `{"query":"SELECT 1"}`); code != 401 {
		t.Fatalf("anonymous sql = %d, want 401", code)
	}
}

func TestExportImportRecords(t *testing.T) {
	s, _ := testServer(t)
	_, out := do(s, "POST", "/api/admin/setup", "", `{"email":"a@x.id","password":"Rahasia123"}`)
	tok := out["token"].(string)
	_, db := do(s, "POST", "/api/admin/databases", tok, `{"name":"app"}`)
	id := db["id"].(string)
	do(s, "POST", "/api/d/"+id+"/collections", tok,
		`{"name":"notes","type":"base","fields":[{"name":"title","type":"text","required":true}]}`)

	code, res := do(s, "POST", "/api/d/"+id+"/c/notes/import", tok,
		`[{"title":"one"},{"title":"two"}]`)
	if code != 200 || res["created"] != 2.0 {
		t.Fatalf("import: %d %v", code, res)
	}

	req := httptest.NewRequest("GET", "/api/d/"+id+"/c/notes/export?format=json", nil)
	req.Header.Set("Authorization", tok)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"one"`) {
		t.Fatalf("export: %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest("GET", "/api/d/"+id+"/c/notes/export?format=csv", nil)
	req.Header.Set("Authorization", tok)
	rec = httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "title") {
		t.Fatalf("csv export: %d", rec.Code)
	}
}
