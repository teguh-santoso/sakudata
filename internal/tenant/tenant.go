package tenant

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"sakudata/internal/auth"
	"sakudata/internal/rules"

	_ "modernc.org/sqlite"
)

func removeFile(p string) error { return os.Remove(p) }

var identRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

// ValidName reports whether s is a safe identifier for databases,
// collections and fields (letters, digits, underscore; no SQL injection).
func ValidName(s string) bool { return identRe.MatchString(s) }

func newID() string {
	const abc = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 15)
	rb := make([]byte, 15)
	_, _ = rand.Read(rb)
	for i := range b {
		b[i] = abc[int(rb[i])%len(abc)]
	}
	return string(b)
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// --- field & collection definitions ---

type Field struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"` // text|number|bool|json|file|email|url|date|select|relation|autodate
	Required bool     `json:"required"`
	Unique   bool     `json:"unique"`
	Options  []string `json:"options,omitempty"` // allowed values for select
}

type Collection struct {
	Name    string              `json:"name"`
	Type    string              `json:"type"` // base|auth
	Fields  []Field             `json:"fields"`
	Rules   Rules               `json:"rules"`
	Hooks   map[string][]string `json:"hooks,omitempty"`   // action -> webhook urls
	Options map[string]any      `json:"options,omitempty"` // per-collection settings
}

// Rules controls record access per operation. Empty means admin-only.
// "*" means public, "user" means any authenticated user of this database.
type Rules struct {
	List   string `json:"list"`
	View   string `json:"view"`
	Create string `json:"create"`
	Update string `json:"update"`
	Delete string `json:"delete"`
}

func validateHooks(c *Collection) error {
	for action, urls := range c.Hooks {
		if action != "create" && action != "update" && action != "delete" {
			return fmt.Errorf("unknown hook action %q", action)
		}
		for _, u := range urls {
			if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
				return fmt.Errorf("hook url must start with http:// or https://")
			}
		}
	}
	return nil
}

func sqliteType(t string) (string, bool) {
	switch t {
	case "text", "json", "file", "email", "url", "date", "select", "relation", "autodate":
		return "TEXT", true
	case "number":
		return "REAL", true
	case "bool":
		return "INTEGER", true
	}
	return "", false
}

// FieldTypes lists every supported field type for the admin UI.
var FieldTypes = []string{"text", "number", "bool", "json", "file", "email", "url", "date", "select", "relation", "autodate"}

// --- manager ---

type Manager struct {
	dir string
	mu  sync.Mutex
	dbs map[string]*sql.DB
}

func NewManager(dir string) *Manager {
	return &Manager{dir: dir, dbs: make(map[string]*sql.DB)}
}

func (m *Manager) path(name string) string { return m.dir + "/dbs/" + name + ".db" }

// Open returns a cached handle to a tenant database, creating the file if needed.
func (m *Manager) Open(name string) (*sql.DB, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("invalid database name %q", name)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if db, ok := m.dbs[name]; ok {
		return db, nil
	}
	if err := os.MkdirAll(m.dir+"/dbs", 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+m.path(name))
	if err != nil {
		return nil, err
	}
	for _, p := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
	} {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, err
		}
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS _collections(
		name TEXT PRIMARY KEY,
		type TEXT NOT NULL,
		schema TEXT NOT NULL,
		rules TEXT NOT NULL,
		created TEXT NOT NULL
	)`); err != nil {
		db.Close()
		return nil, err
	}
	// Best-effort migrations for databases created before newer columns existed.
	for _, q := range []string{
		`ALTER TABLE _collections ADD COLUMN hooks TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE _collections ADD COLUMN options TEXT NOT NULL DEFAULT '{}'`,
	} {
		if _, err := db.Exec(q); err != nil {
			if !strings.Contains(err.Error(), "duplicate column") {
				db.Close()
				return nil, err
			}
		}
	}
	m.dbs[name] = db
	return db, nil
}

// DropFile deletes the database files from disk.
func (m *Manager) DropFile(name string) {
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		_ = removeFile(m.path(name) + suffix)
	}
}

// Close removes the cached handle (used before deleting a database file).
func (m *Manager) Close(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if db, ok := m.dbs[name]; ok {
		db.Close()
		delete(m.dbs, name)
	}
}

// CloseAll closes every cached tenant handle.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for name, db := range m.dbs {
		db.Close()
		delete(m.dbs, name)
	}
}

// BackupTo writes a consistent copy of one tenant database using VACUUM INTO.
func (m *Manager) BackupTo(name, path string) error {
	db, err := m.Open(name)
	if err != nil {
		return err
	}
	_ = os.Remove(path)
	_, err = db.Exec(`VACUUM INTO '` + strings.ReplaceAll(path, "'", "''") + `'`)
	return err
}

func tableName(col string) string { return "c_" + col }

// --- collections ---

func validateCollection(c *Collection) error {
	if !ValidName(c.Name) {
		return fmt.Errorf("invalid collection name %q", c.Name)
	}
	if strings.HasPrefix(c.Name, "_") {
		return fmt.Errorf("collection name must not start with underscore")
	}
	if c.Type != "base" && c.Type != "auth" {
		return fmt.Errorf("type must be base or auth")
	}
	seen := map[string]bool{}
	for _, f := range c.Fields {
		if !ValidName(f.Name) {
			return fmt.Errorf("invalid field name %q", f.Name)
		}
		if f.Name == "id" || f.Name == "created" || f.Name == "updated" {
			return fmt.Errorf("field name %q is reserved", f.Name)
		}
		if seen[f.Name] {
			return fmt.Errorf("duplicate field %q", f.Name)
		}
		seen[f.Name] = true
		if _, ok := sqliteType(f.Type); !ok {
			return fmt.Errorf("unknown field type %q", f.Type)
		}
		if f.Type == "select" && len(f.Options) == 0 {
			return fmt.Errorf("field %q: select requires at least one option", f.Name)
		}
	}
	if err := validateHooks(c); err != nil {
		return err
	}
	if c.Type == "auth" {
		hasEmail, hasPass := false, false
		for _, f := range c.Fields {
			if f.Name == "email" && (f.Type == "text" || f.Type == "email") {
				hasEmail = true
			}
			if f.Name == "password" && f.Type == "text" {
				hasPass = true
			}
		}
		if !hasEmail || !hasPass {
			return fmt.Errorf("auth collections require email and password text fields")
		}
	}
	return nil
}

func (m *Manager) CreateCollection(db *sql.DB, c *Collection) error {
	if err := validateCollection(c); err != nil {
		return err
	}
	var cols []string
	cols = append(cols, "id TEXT PRIMARY KEY", "created TEXT NOT NULL", "updated TEXT NOT NULL")
	for _, f := range c.Fields {
		t, _ := sqliteType(f.Type)
		nn := ""
		if f.Required {
			nn = " NOT NULL"
		}
		cols = append(cols, fmt.Sprintf("%q %s%s", f.Name, t, nn))
	}
	if _, err := db.Exec(fmt.Sprintf("CREATE TABLE %q (%s)", tableName(c.Name), strings.Join(cols, ", "))); err != nil {
		return err
	}
	for _, f := range c.Fields {
		if f.Unique {
			idx := fmt.Sprintf("uq_%s_%s", c.Name, f.Name)
			if _, err := db.Exec(fmt.Sprintf("CREATE UNIQUE INDEX %q ON %q (%q)", idx, tableName(c.Name), f.Name)); err != nil {
				return err
			}
		}
	}
	if c.Type == "auth" {
		// Login identity must be unique even if the schema forgot the flag.
		emailUnique := false
		for _, f := range c.Fields {
			if f.Name == "email" && f.Unique {
				emailUnique = true
			}
		}
		if !emailUnique {
			idx := fmt.Sprintf("uq_%s_email", c.Name)
			if _, err := db.Exec(fmt.Sprintf("CREATE UNIQUE INDEX %q ON %q (%q)", idx, tableName(c.Name), "email")); err != nil {
				return err
			}
		}
	}
	if c.Type == "auth" {
		tok := tableName(c.Name)
		for _, extra := range []string{
			fmt.Sprintf("ALTER TABLE %q ADD COLUMN verified INTEGER NOT NULL DEFAULT 0", tok),
			fmt.Sprintf("ALTER TABLE %q ADD COLUMN tokenKey TEXT NOT NULL DEFAULT ''", tok),
		} {
			if _, err := db.Exec(extra); err != nil {
				return err
			}
		}
	}
	schema, _ := json.Marshal(c.Fields)
	rules, _ := json.Marshal(c.Rules)
	hooks, _ := json.Marshal(c.Hooks)
	options, _ := json.Marshal(c.Options)
	_, err := db.Exec(`INSERT INTO _collections(name,type,schema,rules,hooks,options,created) VALUES(?,?,?,?,?,?,?)`,
		c.Name, c.Type, string(schema), string(rules), string(hooks), string(options), now())
	return err
}

func (m *Manager) GetCollection(db *sql.DB, name string) (*Collection, error) {
	if !ValidName(name) {
		return nil, fmt.Errorf("invalid collection name %q", name)
	}
	var c Collection
	var schema, rules, hooks, options string
	err := db.QueryRow(`SELECT name,type,schema,rules,COALESCE(hooks,'{}'),COALESCE(options,'{}') FROM _collections WHERE name=?`, name).
		Scan(&c.Name, &c.Type, &schema, &rules, &hooks, &options)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(schema), &c.Fields); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(rules), &c.Rules); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(hooks), &c.Hooks)
	_ = json.Unmarshal([]byte(options), &c.Options)
	return &c, nil
}

// DropCollection deletes a collection's table and metadata.
func (m *Manager) DropCollection(db *sql.DB, name string) error {
	if !ValidName(name) {
		return fmt.Errorf("invalid collection name %q", name)
	}
	if _, err := db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %q", tableName(name))); err != nil {
		return err
	}
	_, err := db.Exec(`DELETE FROM _collections WHERE name=?`, name)
	return err
}

func (m *Manager) ListCollections(db *sql.DB) ([]Collection, error) {
	rows, err := db.Query(`SELECT name,type,schema,rules,COALESCE(hooks,'{}'),COALESCE(options,'{}') FROM _collections ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Collection
	for rows.Next() {
		var c Collection
		var schema, rules, hooks, options string
		if err := rows.Scan(&c.Name, &c.Type, &schema, &rules, &hooks, &options); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(schema), &c.Fields)
		_ = json.Unmarshal([]byte(rules), &c.Rules)
		_ = json.Unmarshal([]byte(hooks), &c.Hooks)
		_ = json.Unmarshal([]byte(options), &c.Options)
		out = append(out, c)
	}
	return out, rows.Err()
}

// --- records ---

func convertValue(f Field, v any) (any, error) {
	if v == nil {
		if f.Required {
			return nil, fmt.Errorf("field %q is required", f.Name)
		}
		return nil, nil
	}
	switch f.Type {
	case "text", "json", "file", "email", "url", "date", "select", "relation", "autodate":
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("field %q must be a string", f.Name)
		}
		if f.Type == "json" && s != "" && !json.Valid([]byte(s)) {
			return nil, fmt.Errorf("field %q must be valid JSON", f.Name)
		}
		if f.Type == "email" && s != "" && !strings.Contains(s, "@") {
			return nil, fmt.Errorf("field %q must be a valid email", f.Name)
		}
		if f.Type == "url" && s != "" && !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
			return nil, fmt.Errorf("field %q must start with http:// or https://", f.Name)
		}
		if f.Type == "select" && s != "" {
			allowed := false
			for _, opt := range f.Options {
				if opt == s {
					allowed = true
					break
				}
			}
			if !allowed {
				return nil, fmt.Errorf("field %q must be one of %v", f.Name, f.Options)
			}
		}
		if f.Type == "file" {
			if s == "" {
				if f.Required {
					return nil, fmt.Errorf("field %q is required", f.Name)
				}
				return "", nil
			}
			if _, err := SanitizeFileName(s); err != nil {
				return nil, fmt.Errorf("field %q: %v", f.Name, err)
			}
		}
		return s, nil
	case "number":
		switch n := v.(type) {
		case float64:
			return n, nil
		case json.Number:
			f, err := n.Float64()
			return f, err
		default:
			return nil, fmt.Errorf("field %q must be a number", f.Name)
		}
	case "bool":
		b, ok := v.(bool)
		if !ok {
			return nil, fmt.Errorf("field %q must be a boolean", f.Name)
		}
		if b {
			return 1, nil
		}
		return 0, nil
	}
	return nil, fmt.Errorf("unknown field type %q", f.Type)
}

func (m *Manager) CreateRecord(db *sql.DB, c *Collection, data map[string]any) (map[string]any, error) {
	// Work on a copy so caller maps (and "verified"/hashing extras) stay untouched.
	in := make(map[string]any, len(data))
	for k, v := range data {
		in[k] = v
	}
	verified := false
	if c.Type == "auth" {
		if v, ok := in["verified"]; ok {
			if b, ok := v.(bool); ok {
				verified = b
			}
			delete(in, "verified")
		}
		em, _ := in["email"].(string)
		if em == "" {
			return nil, fmt.Errorf("email is required")
		}
		pw, _ := in["password"].(string)
		if pw == "" {
			return nil, fmt.Errorf("password is required")
		}
		hashed, err := auth.HashPassword(pw)
		if err != nil {
			return nil, err
		}
		in["password"] = hashed
	}
	rec := map[string]any{"id": newID(), "created": now(), "updated": now()}
	rec["created"] = rec["updated"]
	cols := []string{"id", "created", "updated"}
	vals := []any{rec["id"], rec["created"], rec["updated"]}
	for _, f := range c.Fields {
		v, present := in[f.Name]
		if !present {
			v = nil
		}
		if f.Type == "autodate" && (v == nil || v == "") {
			v = now()
		}
		cv, err := convertValue(f, v)
		if err != nil {
			return nil, err
		}
		cols = append(cols, f.Name)
		vals = append(vals, cv)
		if f.Name != "password" {
			rec[f.Name] = dataValueOut(f, cv)
		}
	}
	if c.Type == "auth" {
		tk := make([]byte, 16)
		_, _ = rand.Read(tk)
		cols = append(cols, "verified", "tokenKey")
		vals = append(vals, boolInt(verified), hex.EncodeToString(tk))
		rec["verified"] = verified
	}
	for k := range in {
		known := false
		for _, f := range c.Fields {
			if f.Name == k {
				known = true
				break
			}
		}
		if !known {
			return nil, fmt.Errorf("unknown field %q", k)
		}
	}
	ph := strings.Repeat("?,", len(cols))
	ph = ph[:len(ph)-1]
	qq := fmt.Sprintf("INSERT INTO %q (%s) VALUES (%s)", tableName(c.Name), quoteAll(cols), ph)
	if _, err := db.Exec(qq, vals...); err != nil {
		return nil, err
	}
	return rec, nil
}

func dataValueOut(f Field, v any) any {
	switch f.Type {
	case "file":
		if s, ok := v.(string); ok {
			if s == "" {
				return nil
			}
			return s
		}
		return v
	case "bool":
		if n, ok := v.(int); ok {
			return n == 1
		}
		return v
	case "json":
		if s, ok := v.(string); ok && s != "" {
			var out any
			if err := json.Unmarshal([]byte(s), &out); err == nil {
				return out
			}
		}
		return v
	}
	return v
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func quoteAll(cols []string) string {
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = fmt.Sprintf("%q", c)
	}
	return strings.Join(q, ", ")
}

func stringVal(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case int64:
		return fmt.Sprintf("%d", t)
	case float64:
		return fmt.Sprintf("%v", t)
	}
	return ""
}

func intVal(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case float64:
		return int64(t)
	case []byte:
		var n int64
		_, _ = fmt.Sscanf(string(t), "%d", &n)
		return n
	}
	return 0
}

type ListOptions struct {
	Page       int
	PerPage    int
	Sort       string // e.g. "-created,name" (multi-field)
	Filter     map[string]string
	FilterExpr string // PocketBase-inspired expression, e.g. `nama ~ "buku"`
	Params     map[string]string
	Auth       *rules.Auth
	SkipTotal  bool
}

func schemaOf(c *Collection) rules.Schema {
	s := rules.Schema{}
	for _, f := range c.Fields {
		s[f.Name] = f.Type
	}
	return s
}

func validSortField(c *Collection, col string) bool {
	return col == "id" || col == "created" || col == "updated" || fieldExists(c, col)
}

func buildOrderBy(c *Collection, sort string) (string, error) {
	sort = strings.TrimSpace(sort)
	if sort == "" {
		return "ORDER BY created DESC", nil
	}
	var parts []string
	for _, raw := range strings.Split(sort, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		desc := strings.HasPrefix(raw, "-") || strings.HasPrefix(raw, "+")
		col := strings.TrimLeft(raw, "+-")
		if !validSortField(c, col) {
			return "", fmt.Errorf("cannot sort by unknown field %q", col)
		}
		p := fmt.Sprintf("%q", col)
		if desc && strings.HasPrefix(raw, "-") {
			p += " DESC"
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return "ORDER BY created DESC", nil
	}
	return "ORDER BY " + strings.Join(parts, ", "), nil
}

func (m *Manager) ListRecords(db *sql.DB, c *Collection, opt ListOptions) ([]map[string]any, int, error) {
	if opt.Page < 1 {
		opt.Page = 1
	}
	if opt.PerPage < 1 || opt.PerPage > 200 {
		opt.PerPage = 20
	}
	var where string
	var args []any
	if opt.FilterExpr != "" {
		cond, fargs, err := rules.Compile(opt.FilterExpr, schemaOf(c), opt.Auth, opt.Params)
		if err != nil {
			return nil, 0, err
		}
		where = "WHERE " + cond
		args = fargs
	} else if len(opt.Filter) > 0 {
		where, args = buildFilter(c, opt.Filter)
	}
	var total int
	if !opt.SkipTotal {
		countQ := fmt.Sprintf("SELECT COUNT(*) FROM %q %s", tableName(c.Name), where)
		if err := db.QueryRow(countQ, args...).Scan(&total); err != nil {
			return nil, 0, err
		}
	}
	order, err := buildOrderBy(c, opt.Sort)
	if err != nil {
		return nil, 0, err
	}
	q := fmt.Sprintf("SELECT id,created,updated,%s FROM %q %s %s LIMIT ? OFFSET ?",
		selectCols(c), tableName(c.Name), where, order)
	args = append(args, opt.PerPage, (opt.Page-1)*opt.PerPage)
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		rec, err := scanRecordFull(db, rows, c)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, rec)
	}
	return out, total, rows.Err()
}

func fieldExists(c *Collection, name string) bool {
	for _, f := range c.Fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

func selectCols(c *Collection) string {
	cols := make([]string, len(c.Fields))
	for i, f := range c.Fields {
		cols[i] = fmt.Sprintf("%q", f.Name)
	}
	return strings.Join(cols, ", ")
}

func buildFilter(c *Collection, filter map[string]string) (string, []any) {
	if len(filter) == 0 {
		return "", nil
	}
	var parts []string
	var args []any
	for k, v := range filter {
		if k != "id" && k != "created" && k != "updated" && !fieldExists(c, k) {
			continue
		}
		parts = append(parts, fmt.Sprintf("%q = ?", k))
		args = append(args, v)
	}
	if len(parts) == 0 {
		return "", nil
	}
	return "WHERE " + strings.Join(parts, " AND "), args
}

// scanRecordFull reads a full row including auth extras when present.
func scanRecordFull(db *sql.DB, rows *sql.Rows, c *Collection) (map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	raw := make([]any, len(cols))
	ptr := make([]any, len(cols))
	for i := range raw {
		ptr[i] = &raw[i]
	}
	if err := rows.Scan(ptr...); err != nil {
		return nil, err
	}
	rec := map[string]any{}
	for i, name := range cols {
		switch name {
		case "id", "created", "updated", "tokenKey":
			rec[name] = stringVal(raw[i])
		case "verified":
			rec[name] = intVal(raw[i]) == 1
		case "password":
			continue // never expose password hash
		default:
			for _, f := range c.Fields {
				if f.Name == name {
					rec[name] = dataValueOut(f, raw[i])
				}
			}
		}
	}
	return rec, nil
}

func (m *Manager) GetRecord(db *sql.DB, c *Collection, id string) (map[string]any, error) {
	q := fmt.Sprintf("SELECT * FROM %q WHERE id = ?", tableName(c.Name))
	rows, err := db.Query(q, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	return scanRecordFull(db, rows, c)
}

func (m *Manager) UpdateRecord(db *sql.DB, c *Collection, id string, data map[string]any) (map[string]any, error) {
	var sets []string
	var args []any
	for _, f := range c.Fields {
		v, present := data[f.Name]
		if !present {
			continue
		}
		if c.Type == "auth" && f.Name == "password" {
			s, _ := v.(string)
			if s == "" {
				return nil, fmt.Errorf("password is required")
			}
			hashed, err := auth.HashPassword(s)
			if err != nil {
				return nil, err
			}
			v = hashed
		}
		cv, err := convertValue(f, v)
		if err != nil {
			return nil, err
		}
		sets = append(sets, fmt.Sprintf("%q = ?", f.Name))
		args = append(args, cv)
	}
	for k := range data {
		known := k == "verified"
		for _, f := range c.Fields {
			if f.Name == k {
				known = true
				break
			}
		}
		if !known {
			return nil, fmt.Errorf("unknown field %q", k)
		}
	}
	if v, ok := data["verified"]; ok {
		b, _ := v.(bool)
		n := 0
		if b {
			n = 1
		}
		sets = append(sets, "verified = ?")
		args = append(args, n)
	}
	if len(sets) == 0 {
		return m.GetRecord(db, c, id)
	}
	sets = append(sets, "updated = ?")
	args = append(args, now(), id)
	q := fmt.Sprintf("UPDATE %q SET %s WHERE id = ?", tableName(c.Name), strings.Join(sets, ", "))
	res, err := db.Exec(q, args...)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, sql.ErrNoRows
	}
	return m.GetRecord(db, c, id)
}

// AuthenticateUser verifies identity+password for an auth collection and
// returns the public record. Unverified accounts are rejected.
func (m *Manager) AuthenticateUser(db *sql.DB, c *Collection, identity, password string) (map[string]any, error) {
	if c.Type != "auth" {
		return nil, fmt.Errorf("collection %q is not an auth collection", c.Name)
	}
	var id, hash string
	var verified int
	err := db.QueryRow(
		fmt.Sprintf("SELECT id, password, verified FROM %q WHERE email = ?", tableName(c.Name)),
		identity).Scan(&id, &hash, &verified)
	if err != nil {
		return nil, fmt.Errorf("invalid identity or password")
	}
	if !auth.VerifyPassword(hash, password) {
		return nil, fmt.Errorf("invalid identity or password")
	}
	if verified != 1 {
		return nil, fmt.Errorf("email is not verified")
	}
	return m.GetRecord(db, c, id)
}

// UpdateMeta replaces the access rules, webhooks and options of a collection.
// Nil values leave that part unchanged. Rule values may be "", "*", "user" or
// a filter expression such as `@request.auth.id != "" && owner = @request.auth.id`.
func (m *Manager) UpdateMeta(db *sql.DB, name string, r *Rules, hooks map[string][]string, options map[string]any) (*Collection, error) {
	c, err := m.GetCollection(db, name)
	if err != nil {
		return nil, err
	}
	if r != nil {
		for _, v := range []string{r.List, r.View, r.Create, r.Update, r.Delete} {
			if v == "" || v == "*" || v == "user" {
				continue
			}
			if !strings.ContainsAny(v, "=!<>~&|()") {
				return nil, fmt.Errorf("invalid rule %q: expected a filter expression", v)
			}
			if _, _, err := rules.Compile(v, rules.Schema{}, nil, nil); err != nil {
				return nil, fmt.Errorf("invalid rule %q: %v", v, err)
			}
		}
		raw, _ := json.Marshal(r)
		if _, err := db.Exec(`UPDATE _collections SET rules = ? WHERE name = ?`, string(raw), name); err != nil {
			return nil, err
		}
	}
	if hooks != nil {
		tmp := *c
		tmp.Hooks = hooks
		if err := validateHooks(&tmp); err != nil {
			return nil, err
		}
		raw, _ := json.Marshal(hooks)
		if _, err := db.Exec(`UPDATE _collections SET hooks = ? WHERE name = ?`, string(raw), name); err != nil {
			return nil, err
		}
	}
	if options != nil {
		raw, _ := json.Marshal(options)
		if _, err := db.Exec(`UPDATE _collections SET options = ? WHERE name = ?`, string(raw), name); err != nil {
			return nil, err
		}
	}
	return m.GetCollection(db, name)
}

func (m *Manager) DeleteRecord(db *sql.DB, c *Collection, id string) error {
	res, err := db.Exec(fmt.Sprintf("DELETE FROM %q WHERE id = ?", tableName(c.Name)), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
