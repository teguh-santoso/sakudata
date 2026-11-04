package rules

import (
	"strings"
	"testing"
)

func TestCompileSpecialRules(t *testing.T) {
	schema := Schema{"nama": "text"}
	cases := []struct {
		expr string
		auth *Auth
		sql  string
	}{
		{"*", nil, "1=1"},
		{"user", nil, "1=0"},
		{"user", &Auth{LoggedIn: true}, "1=1"},
		{"", nil, "1=0"},
	}
	for _, c := range cases {
		sql, _, err := Compile(c.expr, schema, c.auth, nil)
		if err != nil {
			t.Fatalf("%q: %v", c.expr, err)
		}
		if sql != c.sql {
			t.Fatalf("%q = %q, want %q", c.expr, sql, c.sql)
		}
	}
}

func TestCompileComparisons(t *testing.T) {
	schema := Schema{"nama": "text", "harga": "number", "aktif": "bool"}

	sql, args, err := Compile(`nama ~ "buk" && harga >= 1000 && aktif = true`, schema, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, "LIKE") || !strings.Contains(sql, ">=") {
		t.Fatalf("unexpected sql: %s", sql)
	}
	if len(args) != 3 {
		t.Fatalf("args = %v", args)
	}
	if args[0] != "%buk%" {
		t.Fatalf("like arg = %v", args[0])
	}
}

func TestCompileRequestAuth(t *testing.T) {
	schema := Schema{"owner": "text"}

	sql, args, err := Compile(`owner = @request.auth.id`, schema, &Auth{LoggedIn: true, UserID: "u1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sql != `("owner" = ?)` || len(args) != 1 || args[0] != "u1" {
		t.Fatalf("sql=%q args=%v", sql, args)
	}

	// Anonymous caller resolves to empty, so the comparison is false at runtime.
	_, args, err = Compile(`@request.auth.id != ""`, schema, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 2 || args[0] != "" || args[1] != "" {
		t.Fatalf("anon args = %v", args)
	}
}

func TestCompileInjectionIsParameterized(t *testing.T) {
	schema := Schema{"nama": "text"}
	sql, args, err := Compile(`nama = "x'; DROP TABLE users; --"`, schema, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sql, "DROP") {
		t.Fatalf("injected sql: %s", sql)
	}
	if len(args) != 1 || args[0] != "x'; DROP TABLE users; --" {
		t.Fatalf("args = %v", args)
	}
}

func TestCompileParams(t *testing.T) {
	schema := Schema{"nama": "text"}
	sql, args, err := Compile(`nama = {:name}`, schema, nil, map[string]string{"name": "Buku"})
	if err != nil {
		t.Fatal(err)
	}
	if sql != `("nama" = ?)` || args[0] != "Buku" {
		t.Fatalf("sql=%q args=%v", sql, args)
	}
}

func TestEvalRecord(t *testing.T) {
	schema := Schema{"owner": "text", "harga": "number"}
	rec := map[string]any{"owner": "u1", "harga": float64(500)}
	auth := &Auth{LoggedIn: true, UserID: "u1"}

	ok, err := Eval(`owner = @request.auth.id && harga > 100`, rec, schema, auth)
	if err != nil || !ok {
		t.Fatalf("expected true, got %v %v", ok, err)
	}
	ok, _ = Eval(`owner = @request.auth.id`, rec, schema, &Auth{LoggedIn: true, UserID: "u2"})
	if ok {
		t.Fatal("expected false for another user")
	}
	ok, _ = Eval(`*`, rec, schema, nil)
	if !ok {
		t.Fatal("* should be true")
	}
}

func TestParseErrors(t *testing.T) {
	for _, expr := range []string{`nama =`, `nama = "x`, `(nama = "x"`, `nama @ "x"`} {
		if _, _, err := Compile(expr, Schema{"nama": "text"}, nil, nil); err == nil {
			t.Fatalf("expected error for %q", expr)
		}
	}
}
