package ui

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func get(t *testing.T, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

func TestAdminPageServes(t *testing.T) {
	code, body := get(t, "/")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	for _, want := range []string{"SakuData", "assets/v10/app.css", "assets/v10/js/app.js"} {
		if !strings.Contains(body, want) {
			t.Fatalf("index missing %q", want)
		}
	}
}

func TestAssetsServe(t *testing.T) {
	code, css := get(t, "/assets/v10/app.css")
	if code != 200 || !strings.Contains(css, "--primary") {
		t.Fatalf("css: %d", code)
	}
	code, js := get(t, "/assets/v10/js/app.js")
	if code != 200 {
		t.Fatalf("app.js: %d", code)
	}
	for _, want := range []string{"renderRecords", "renderSettings", "renderSuperusers"} {
		if !strings.Contains(js, want) {
			t.Fatalf("app.js missing %q", want)
		}
	}
	code, login := get(t, "/assets/v10/js/views/login.js")
	if code != 200 || !strings.Contains(login, "api/admin/login") {
		t.Fatalf("login.js: %d", code)
	}
}
