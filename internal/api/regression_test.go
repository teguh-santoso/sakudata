package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// An empty collection list must serialize as [] (not null) so the admin UI can
// safely call array methods.
func TestListCollectionsReturnsEmptyArray(t *testing.T) {
	s, _ := testServer(t)
	_, out := do(s, "POST", "/api/admin/setup", "", `{"email":"a@x.id","password":"Rahasia123"}`)
	tok := out["token"].(string)
	do(s, "POST", "/api/admin/databases", tok, `{"name":"empty"}`)

	req := httptest.NewRequest("GET", "/api/d/empty/collections", nil)
	req.Header.Set("Authorization", tok)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Fatalf("body = %q, want []", got)
	}
}
