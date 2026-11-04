package tenant

import "testing"

func TestSetCollectionSchemaPreservesData(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	db, err := m.Open("test")
	if err != nil {
		t.Fatal(err)
	}

	col := &Collection{
		Name: "produk",
		Type: "base",
		Fields: []Field{
			{Name: "nama", Type: "text", Required: true},
			{Name: "harga", Type: "number"},
		},
	}
	if err := m.CreateCollection(db, col); err != nil {
		t.Fatal(err)
	}
	rec, err := m.CreateRecord(db, col, map[string]any{"nama": "Buku", "harga": float64(50000)})
	if err != nil {
		t.Fatal(err)
	}

	// Add a column.
	updated, err := m.SetCollectionSchema(db, "produk", []Field{
		{Name: "nama", Type: "text", Required: true},
		{Name: "harga", Type: "number"},
		{Name: "stok", Type: "number"},
		{Name: "status", Type: "select", Options: []string{"draft", "published"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Fields) != 4 {
		t.Fatalf("fields = %+v", updated.Fields)
	}
	got, err := m.GetRecord(db, updated, rec["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if got["nama"] != "Buku" || got["harga"] != float64(50000) {
		t.Fatalf("data lost after add: %+v", got)
	}

	// Remove a column and change another.
	updated, err = m.SetCollectionSchema(db, "produk", []Field{
		{Name: "nama", Type: "text", Required: true},
		{Name: "stok", Type: "number"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err = m.GetRecord(db, updated, rec["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if got["nama"] != "Buku" {
		t.Fatalf("data lost after remove: %+v", got)
	}
	if _, ok := got["harga"]; ok {
		t.Fatalf("removed column still present: %+v", got)
	}
}

func TestAuthCollectionAllowsEmailType(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir)
	db, err := m.Open("test")
	if err != nil {
		t.Fatal(err)
	}
	col := &Collection{
		Name: "members",
		Type: "auth",
		Fields: []Field{
			{Name: "email", Type: "email", Required: true, Unique: true},
			{Name: "password", Type: "text", Required: true},
		},
	}
	if err := m.CreateCollection(db, col); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateRecord(db, col, map[string]any{
		"email": "a@b.c", "password": "Rahasia123", "verified": true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AuthenticateUser(db, col, "a@b.c", "Rahasia123"); err != nil {
		t.Fatal(err)
	}
}
