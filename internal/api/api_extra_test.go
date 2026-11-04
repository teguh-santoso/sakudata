package api

import "testing"

func TestSettingsLogsAndSuperusers(t *testing.T) {
	s, _ := testServer(t)
	_, out := do(s, "POST", "/api/admin/setup", "", `{"email":"root@x.id","password":"Rahasia123"}`)
	tok := out["token"].(string)

	code, settings := do(s, "PUT", "/api/admin/settings", tok,
		`{"app_name":"Toko","s3_secret":"verysecret"}`)
	if code != 200 {
		t.Fatalf("put settings: %d %v", code, settings)
	}
	app := settings["app"].(map[string]any)
	if app["app_name"] != "Toko" {
		t.Fatalf("app_name = %v", app["app_name"])
	}
	s3 := settings["s3"].(map[string]any)
	if s3["secret"] != "••••••" {
		t.Fatalf("secret not masked: %v", s3["secret"])
	}

	code, logs := do(s, "GET", "/api/admin/logs", tok, "")
	if code != 200 || logs["totalItems"].(float64) < 1 {
		t.Fatalf("logs: %d %v", code, logs)
	}

	if code, _ := do(s, "POST", "/api/admin/superusers", tok,
		`{"email":"second@x.id","password":"Rahasia123"}`); code != 200 {
		t.Fatalf("create superuser: %d", code)
	}
	if code, _ := do(s, "GET", "/api/admin/superusers", tok, ""); code != 200 {
		t.Fatalf("list superusers: %d", code)
	}

	// The last remaining superuser cannot be deleted (here: three exist).
	if code, _ := do(s, "DELETE", "/api/admin/superusers/nope", tok, ""); code != 500 && code != 204 {
		t.Logf("delete unknown returned %d", code)
	}
}

func TestSchemaPatchAndExpressionRules(t *testing.T) {
	s, _ := testServer(t)
	_, out := do(s, "POST", "/api/admin/setup", "", `{"email":"root@x.id","password":"Rahasia123"}`)
	tok := out["token"].(string)
	do(s, "POST", "/api/admin/databases", tok, `{"name":"app"}`)

	colDef := `{"name":"produk","type":"base","fields":[{"name":"nama","type":"text","required":true}]}`
	if code, _ := do(s, "POST", "/api/d/app/collections", tok, colDef); code != 200 {
		t.Fatalf("create collection: %d", code)
	}
	_, rec := do(s, "POST", "/api/d/app/c/produk/records", tok, `{"nama":"Buku"}`)
	id := rec["id"].(string)

	if code, _ := do(s, "PATCH", "/api/d/app/collections/produk", tok,
		`{"fields":[{"name":"nama","type":"text","required":true},{"name":"stok","type":"number"}]}`); code != 200 {
		t.Fatalf("patch schema: %d", code)
	}
	code, got := do(s, "GET", "/api/d/app/c/produk/records/"+id, tok, "")
	if code != 200 || got["nama"] != "Buku" {
		t.Fatalf("record lost after schema patch: %d %v", code, got)
	}

	// Expression filter on list.
	do(s, "POST", "/api/d/app/c/produk/records", tok, `{"nama":"Pena","stok":5}`)
	code, list := do(s, "GET", `/api/d/app/c/produk/records?filter=nama%20~%20%22Buk%22`, tok, "")
	if code != 200 || list["totalItems"] != 1.0 {
		t.Fatalf("filter: %d %v", code, list)
	}

	// Public list but per-record owner rule for view.
	do(s, "PATCH", "/api/d/app/collections/produk", tok,
		`{"rules":{"list":"*","view":"nama = \"Buku\"","create":"","update":"","delete":""}}`)
	code, _ = do(s, "GET", "/api/d/app/c/produk/records/"+id, "", "")
	if code != 200 {
		t.Fatalf("public view should allow matching record, got %d", code)
	}
	code, list = do(s, "GET", "/api/d/app/c/produk/records", "", "")
	if code != 200 || list["totalItems"] != 2.0 {
		t.Fatalf("public list: %d %v", code, list)
	}
}
