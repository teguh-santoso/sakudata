package tenant

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// defaultLiteral returns a SQL literal used to backfill a newly added column.
func defaultLiteral(f Field) string {
	switch f.Type {
	case "number":
		return "0"
	case "bool":
		return "0"
	case "autodate":
		return "'" + now() + "'"
	default:
		return "''"
	}
}

func columnDefs(fields []Field, auth bool) string {
	cols := []string{`"id" TEXT PRIMARY KEY`, `"created" TEXT NOT NULL`, `"updated" TEXT NOT NULL`}
	for _, f := range fields {
		t, _ := sqliteType(f.Type)
		nn := ""
		if f.Required {
			nn = " NOT NULL"
		}
		cols = append(cols, fmt.Sprintf("%q %s%s", f.Name, t, nn))
	}
	if auth {
		cols = append(cols,
			`"verified" INTEGER NOT NULL DEFAULT 0`,
			`"tokenKey" TEXT NOT NULL DEFAULT ''`,
		)
	}
	return strings.Join(cols, ", ")
}

// SetCollectionSchema replaces the field definition of a collection, migrating
// existing rows. SQLite has limited ALTER support, so this rebuilds the table
// in a single transaction (create new, copy shared columns, swap, reindex).
func (m *Manager) SetCollectionSchema(db *sql.DB, name string, fields []Field) (*Collection, error) {
	c, err := m.GetCollection(db, name)
	if err != nil {
		return nil, err
	}
	oldFieldsList := c.Fields
	c.Fields = fields
	if err := validateCollection(c); err != nil {
		return nil, err
	}

	old := tableName(name)
	tmp := old + "__new"
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(fmt.Sprintf("CREATE TABLE %q (%s)", tmp, columnDefs(fields, c.Type == "auth"))); err != nil {
		return nil, err
	}

	oldFields := map[string]bool{}
	for _, f := range oldFieldsList {
		oldFields[f.Name] = true
	}

	dst := []string{`"id"`, `"created"`, `"updated"`}
	src := []string{`"id"`, `"created"`, `"updated"`}
	for _, f := range fields {
		dst = append(dst, fmt.Sprintf("%q", f.Name))
		if oldFields[f.Name] {
			src = append(src, fmt.Sprintf("%q", f.Name))
		} else {
			src = append(src, defaultLiteral(f))
		}
	}
	if c.Type == "auth" {
		dst = append(dst, `"verified"`, `"tokenKey"`)
		src = append(src, `COALESCE("verified",0)`, `COALESCE("tokenKey",'')`)
	}

	insert := fmt.Sprintf("INSERT INTO %q (%s) SELECT %s FROM %q",
		tmp, strings.Join(dst, ", "), strings.Join(src, ", "), old)
	if _, err := tx.Exec(insert); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(fmt.Sprintf("DROP TABLE %q", old)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(fmt.Sprintf("ALTER TABLE %q RENAME TO %q", tmp, old)); err != nil {
		return nil, err
	}

	for _, f := range fields {
		if f.Unique {
			idx := fmt.Sprintf("uq_%s_%s", name, f.Name)
			if _, err := tx.Exec(fmt.Sprintf("CREATE UNIQUE INDEX %q ON %q (%q)", idx, old, f.Name)); err != nil {
				return nil, err
			}
		}
	}
	if c.Type == "auth" {
		emailUnique := false
		for _, f := range fields {
			if f.Name == "email" && f.Unique {
				emailUnique = true
			}
		}
		if !emailUnique {
			idx := fmt.Sprintf("uq_%s_email", name)
			if _, err := tx.Exec(fmt.Sprintf("CREATE UNIQUE INDEX %q ON %q (%q)", idx, old, "email")); err != nil {
				return nil, err
			}
		}
	}

	raw, _ := json.Marshal(fields)
	if _, err := tx.Exec(`UPDATE _collections SET schema = ? WHERE name = ?`, string(raw), name); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return m.GetCollection(db, name)
}
