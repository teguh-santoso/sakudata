package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"sakudata/internal/store"
	"sakudata/internal/tenant"
)

func testServer(t *testing.T) (*Server, string) {
	t.Helper()
	tmp := t.TempDir()
	st, err := store.Open(tmp + "/system.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return NewServer(st, tenant.NewManager(tmp), []byte("test-secret-32-bytes-padded!!!!"), tmp), tmp
}

func do(s *Server, method, path, token, body string) (int, map[string]any) {
	var r *httptest.ResponseRecorder
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	r = httptest.NewRecorder()
	s.Routes().ServeHTTP(r, req)
	var out map[string]any
	_ = json.Unmarshal(r.Body.Bytes(), &out)
	return r.Code, out
}

func TestEndToEnd(t *testing.T) {
	s, _ := testServer(t)

	if code, _ := do(s, "GET", "/api/health", "", ""); code != 200 {
		t.Fatalf("health: %d", code)
	}
	// setup twice: second must conflict
	code, out := do(s, "POST", "/api/admin/setup", "", `{"email":"a@x.id","password":"Rahasia123"}`)
	if code != 200 || out["token"] == "" {
		t.Fatalf("setup: %d %v", code, out)
	}
	if code, _ := do(s, "POST", "/api/admin/setup", "", `{"email":"b@x.id","password":"Rahasia123"}`); code != 409 {
		t.Fatalf("second setup should conflict, got %d", code)
	}
	tok := out["token"].(string)

	// unauthenticated admin route
	if code, _ := do(s, "GET", "/api/admin/databases", "", ""); code != 401 {
		t.Fatalf("missing token should be 401, got %d", code)
	}

	// database lifecycle
	if code, _ := do(s, "POST", "/api/admin/databases", tok, `{"name":"toko"}`); code != 200 {
		t.Fatalf("create db: %d", code)
	}
	if code, _ := do(s, "POST", "/api/admin/databases", tok, `{"name":"toko"}`); code != 409 {
		t.Fatalf("duplicate db should conflict, got %d", code)
	}
	if code, _ := do(s, "POST", "/api/admin/databases", tok, `{"name":"a-b"}`); code != 400 {
		t.Fatalf("bad db name should be 400, got %d", code)
	}

	// collection + records
	colDef := `{"name":"produk","type":"base","fields":[{"name":"nama","type":"text","required":true},{"name":"harga","type":"number"}]}`
	if code, out := do(s, "POST", "/api/d/toko/collections", tok, colDef); code != 200 {
		t.Fatalf("create collection: %d %v", code, out)
	}
	code, out = do(s, "POST", "/api/d/toko/c/produk/records", tok, `{"nama":"Buku","harga":50000}`)
	if code != 200 || out["nama"] != "Buku" {
		t.Fatalf("create record: %d %v", code, out)
	}
	id := out["id"].(string)
	if code, _ := do(s, "GET", "/api/d/toko/c/produk/records/"+id, tok, ""); code != 200 {
		t.Fatalf("get record: %d", code)
	}
	if code, out := do(s, "GET", "/api/d/toko/c/produk/records?page=1&perPage=10", tok, ""); code != 200 || out["totalItems"] != 1.0 {
		t.Fatalf("list records: %d %v", code, out)
	}
	if code, _ := do(s, "PATCH", "/api/d/toko/c/produk/records/"+id, tok, `{"harga":60000}`); code != 200 {
		t.Fatalf("update record: %d", code)
	}
	if code, _ := do(s, "DELETE", "/api/d/toko/c/produk/records/"+id, tok, ""); code != 204 {
		t.Fatalf("delete record: %d", code)
	}
	if code, _ := do(s, "DELETE", "/api/admin/databases/toko", tok, ""); code != 204 {
		t.Fatalf("delete db: %d", code)
	}
}

func TestUserAuthAndRules(t *testing.T) {
	s, _ := testServer(t)
	_, out := do(s, "POST", "/api/admin/setup", "", `{"email":"a@x.id","password":"Rahasia123"}`)
	adminTok := out["token"].(string)

	do(s, "POST", "/api/admin/databases", adminTok, `{"name":"app"}`)
	colDef := `{"name":"members","type":"auth","fields":[
		{"name":"email","type":"text","required":true,"unique":true},
		{"name":"password","type":"text","required":true},
		{"name":"nama","type":"text"}]}`
	if code, out := do(s, "POST", "/api/d/app/collections", adminTok, colDef); code != 200 {
		t.Fatalf("create auth collection: %d %v", code, out)
	}

	// user creation requires email+password, hashes password, hides it
	if code, _ := do(s, "POST", "/api/d/app/c/members/records", adminTok,
		`{"nama":"Tanpa Kredensial"}`); code != 400 {
		t.Fatalf("missing email/password should be 400, got %d", code)
	}
	code, out := do(s, "POST", "/api/d/app/c/members/records", adminTok,
		`{"email":"u@x.id","password":"Rahasia123","nama":"Uji","verified":true}`)
	if code != 200 {
		t.Fatalf("create user: %d %v", code, out)
	}
	if _, hasPw := out["password"]; hasPw {
		t.Fatal("password hash leaked in response")
	}
	if out["verified"] != true {
		t.Fatalf("verified flag lost: %v", out)
	}

	login := func(id, pw string) (int, map[string]any) {
		return do(s, "POST", "/api/d/app/c/members/auth/login", "",
			`{"identity":"`+id+`","password":"`+pw+`"}`)
	}
	if code, _ := login("u@x.id", "salah"); code != 401 {
		t.Fatalf("wrong password should be 401, got %d", code)
	}
	code, out = login("u@x.id", "Rahasia123")
	if code != 200 || out["token"] == "" {
		t.Fatalf("login: %d %v", code, out)
	}
	userTok := out["token"].(string)

	// unverified account cannot log in
	do(s, "POST", "/api/d/app/c/members/records", adminTok,
		`{"email":"baru@x.id","password":"Rahasia123"}`)
	if code, _ := login("baru@x.id", "Rahasia123"); code != 401 {
		t.Fatalf("unverified login should be 401, got %d", code)
	}

	// default rules "" : user forbidden, anonymous unauthorized
	if code, _ := do(s, "GET", "/api/d/app/c/members/records", userTok, ""); code != 403 {
		t.Fatalf("user on admin-only rule should be 403, got %d", code)
	}
	if code, _ := do(s, "GET", "/api/d/app/c/members/records", "", ""); code != 401 {
		t.Fatalf("anonymous on admin-only rule should be 401, got %d", code)
	}

	// open to authenticated users of this db
	ruleUser := `{"rules":{"list":"user","view":"user","create":"user","update":"user","delete":"user"}}`
	if code, _ := do(s, "PATCH", "/api/d/app/collections/members", adminTok, ruleUser); code != 200 {
		t.Fatalf("set rules: %d", code)
	}
	if code, _ := do(s, "GET", "/api/d/app/c/members/records", userTok, ""); code != 200 {
		t.Fatalf("user on user-rule should be 200, got %d", code)
	}
	if code, _ := do(s, "GET", "/api/d/app/c/members/records", "", ""); code != 401 {
		t.Fatalf("anonymous on user-rule should be 401, got %d", code)
	}

	// token from another database must not work here
	do(s, "POST", "/api/admin/databases", adminTok, `{"name":"lain"}`)
	do(s, "POST", "/api/d/lain/collections", adminTok, colDef)
	do(s, "POST", "/api/d/lain/c/members/records", adminTok,
		`{"email":"u@x.id","password":"Rahasia123","verified":true}`)
	_, other := do(s, "POST", "/api/d/lain/c/members/auth/login", "",
		`{"identity":"u@x.id","password":"Rahasia123"}`)
	if code, _ := do(s, "GET", "/api/d/app/c/members/records", other["token"].(string), ""); code != 403 {
		t.Fatalf("cross-db token should be 403, got %d", code)
	}

	// invalid rule values rejected
	if code, _ := do(s, "PATCH", "/api/d/app/collections/members", adminTok,
		`{"rules":{"list":"owner"}}`); code != 400 {
		t.Fatalf("bad rule should be 400, got %d", code)
	}

	// public rule allows anonymous
	if code, _ := do(s, "PATCH", "/api/d/app/collections/members", adminTok,
		`{"rules":{"list":"*","view":"*","create":"","update":"","delete":""}}`); code != 200 {
		t.Fatalf("set public rule: %d", code)
	}
	if code, _ := do(s, "GET", "/api/d/app/c/members/records", "", ""); code != 200 {
		t.Fatalf("anonymous on public rule should be 200, got %d", code)
	}

	// refresh
	if code, out := do(s, "POST", "/api/d/app/c/members/auth/refresh", userTok, ""); code != 200 || out["token"] == "" {
		t.Fatalf("refresh: %d %v", code, out)
	}
	if code, _ := do(s, "POST", "/api/d/app/c/members/auth/refresh", adminTok, ""); code != 401 {
		t.Fatalf("refresh with admin token should be 401, got %d", code)
	}
}

func uploadFile(t *testing.T, s *Server, tok, url, filename, body string) (int, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte(body))
	mw.Close()
	req := httptest.NewRequest("POST", url, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if tok != "" {
		req.Header.Set("Authorization", tok)
	}
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestFilesAndWebhooks(t *testing.T) {
	s, _ := testServer(t)
	_, out := do(s, "POST", "/api/admin/setup", "", `{"email":"a@x.id","password":"Rahasia123"}`)
	adminTok := out["token"].(string)
	do(s, "POST", "/api/admin/databases", adminTok, `{"name":"arsip"}`)
	colDef := `{"name":"dokumen","type":"base","fields":[
		{"name":"judul","type":"text","required":true},
		{"name":"lampiran","type":"file"}]}`

	// webhook receiver
	got := make(chan map[string]any, 4)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var e map[string]any
		_ = json.NewDecoder(r.Body).Decode(&e)
		got <- e
	}))
	defer hook.Close()

	if code, out := do(s, "POST", "/api/d/arsip/collections", adminTok, colDef); code != 200 {
		t.Fatalf("create collection: %d %v", code, out)
	}

	// subscribe without token is rejected immediately (rules still admin-only)
	if code, _ := do(s, "GET", "/api/d/arsip/c/dokumen/subscribe", "", ""); code != 401 {
		t.Fatalf("anonymous subscribe should be 401, got %d", code)
	}
	meta := `{"rules":{"list":"*","view":"*","create":"*","update":"*","delete":"*"},
		"hooks":{"create":["` + hook.URL + `"]}}`
	if code, out := do(s, "PATCH", "/api/d/arsip/collections/dokumen", adminTok, meta); code != 200 {
		t.Fatalf("set rules+hooks: %d %v", code, out)
	}
	if code, _ := do(s, "PATCH", "/api/d/arsip/collections/dokumen", adminTok,
		`{"hooks":{"create":["tidak-valid"]}}`); code != 400 {
		t.Fatalf("bad hook url should be 400, got %d", code)
	}

	// create record triggers webhook
	code, out := do(s, "POST", "/api/d/arsip/c/dokumen/records", adminTok, `{"judul":"Laporan"}`)
	if code != 200 {
		t.Fatalf("create record: %d %v", code, out)
	}
	id := out["id"].(string)
	select {
	case e := <-got:
		if e["event"] != "create" || e["collection"] != "dokumen" {
			t.Fatalf("bad webhook event: %v", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("webhook not received")
	}

	// upload
	code, out = uploadFile(t, s, adminTok, "/api/d/arsip/c/dokumen/records/"+id+"/files/lampiran", "a.txt", "isi-file")
	if code != 200 {
		t.Fatalf("upload: %d %v", code, out)
	}
	stored, _ := out["lampiran"].(string)
	if stored == "" || !strings.HasSuffix(stored, "_a.txt") {
		t.Fatalf("bad stored name: %v", out)
	}

	// path traversal is neutralized to the base name by the multipart parser
	code, out = uploadFile(t, s, adminTok, "/api/d/arsip/c/dokumen/records/"+id+"/files/lampiran", "../../x", "x")
	if code != 200 {
		t.Fatalf("traversal upload: %d %v", code, out)
	}
	if stored, _ := out["lampiran"].(string); !strings.HasSuffix(stored, "_x") {
		t.Fatalf("traversal name not neutralized: %v", out)
	}
	// characters outside the allowlist are rejected by our validator
	if code, _ := uploadFile(t, s, adminTok, "/api/d/arsip/c/dokumen/records/"+id+"/files/lampiran", "a b.txt", "x"); code != 400 {
		t.Fatalf("bad filename should be 400, got %d", code)
	}
	// restore the original file for the download check
	if code, _ := uploadFile(t, s, adminTok, "/api/d/arsip/c/dokumen/records/"+id+"/files/lampiran", "a.txt", "isi-file"); code != 200 {
		t.Fatalf("re-upload: %d", code)
	}

	// download
	req := httptest.NewRequest("GET", "/api/d/arsip/c/dokumen/records/"+id+"/files/lampiran", nil)
	req.Header.Set("Authorization", adminTok)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != "isi-file" {
		t.Fatalf("download: %d %q", rec.Code, rec.Body.String())
	}

	// delete file clears the field
	code, out = do(s, "DELETE", "/api/d/arsip/c/dokumen/records/"+id+"/files/lampiran", adminTok, "")
	if code != 200 || out["lampiran"] != nil {
		t.Fatalf("delete file: %d %v", code, out)
	}

	// live SSE: subscribe, create a record, expect the event on the stream
	ctx, cancel := context.WithCancel(context.Background())
	subReq := httptest.NewRequest("GET", "/api/d/arsip/c/dokumen/subscribe?token="+adminTok, nil).WithContext(ctx)
	subRec := httptest.NewRecorder()
	subDone := make(chan struct{})
	go func() {
		s.Routes().ServeHTTP(subRec, subReq)
		close(subDone)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for s.hub.SubCount("arsip", "dokumen") == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if s.hub.SubCount("arsip", "dokumen") == 0 {
		cancel()
		t.Fatal("SSE subscription not established")
	}
	code, out = do(s, "POST", "/api/d/arsip/c/dokumen/records", adminTok, `{"judul":"Live"}`)
	if code != 200 {
		cancel()
		t.Fatalf("create record: %d %v", code, out)
	}
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-subDone
	if !strings.Contains(subRec.Body.String(), `"event":"create"`) ||
		!strings.Contains(subRec.Body.String(), out["id"].(string)) {
		t.Fatalf("SSE stream missing create event: %q", subRec.Body.String())
	}
}
