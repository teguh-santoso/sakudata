package tenant

import (
	"strings"
	"testing"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	return NewManager(t.TempDir())
}

func TestValidName(t *testing.T) {
	for _, good := range []string{"db1", "A_b2", "x"} {
		if !ValidName(good) {
			t.Fatalf("%q should be valid", good)
		}
	}
	for _, bad := range []string{"", "1abc", "a-b", "a b", "a;b", "_priv", "dbs/x", strings.Repeat("a", 70)} {
		if ValidName(bad) {
			t.Fatalf("%q should be invalid", bad)
		}
	}
}

func TestCollectionCRUD(t *testing.T) {
	m := NewManager(t.TempDir())
	db, err := m.Open("toko")
	if err != nil {
		t.Fatal(err)
	}
	c := &Collection{
		Name: "produk", Type: "base",
		Fields: []Field{
			{Name: "nama", Type: "text", Required: true},
			{Name: "harga", Type: "number"},
			{Name: "aktif", Type: "bool"},
			{Name: "meta", Type: "json"},
		},
	}
	if err := m.CreateCollection(db, c); err != nil {
		t.Fatal(err)
	}
	rec, err := m.CreateRecord(db, c, map[string]any{
		"nama": "Buku", "harga": 50000.0, "aktif": true, "meta": `{"warna":"biru"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec["nama"] != "Buku" || rec["aktif"] != true {
		t.Fatalf("bad record: %v", rec)
	}
	if _, err := m.CreateRecord(db, c, map[string]any{"harga": 1.0}); err == nil {
		t.Fatal("missing required field accepted")
	}
	items, total, err := m.ListRecords(db, c, ListOptions{PerPage: 10})
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("list: %v %d %v", items, total, err)
	}
	got, err := m.GetRecord(db, c, rec["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if got["harga"] != 50000.0 {
		t.Fatalf("bad harga: %v", got["harga"])
	}
	upd, err := m.UpdateRecord(db, c, rec["id"].(string), map[string]any{"harga": 60000.0})
	if err != nil {
		t.Fatal(err)
	}
	if upd["harga"] != 60000.0 {
		t.Fatalf("update failed: %v", upd)
	}
	if err := m.DeleteRecord(db, c, rec["id"].(string)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.GetRecord(db, c, rec["id"].(string)); err == nil {
		t.Fatal("deleted record still readable")
	}
}

func TestInjectionRejected(t *testing.T) {
	m := testManager(t)
	if _, err := m.Open("x; DROP TABLE admins;--"); err == nil {
		t.Fatal("malicious db name accepted")
	}
	db, err := m.Open("ok")
	if err != nil {
		t.Fatal(err)
	}
	bad := &Collection{Name: "c\"; DROP TABLE x;--", Type: "base"}
	if err := m.CreateCollection(db, bad); err == nil {
		t.Fatal("malicious collection accepted")
	}
}
