package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Store is the system database: admins, database registry, settings.
type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path)
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
			return nil, fmt.Errorf("pragma %q: %w", p, err)
		}
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS admins(
			id TEXT PRIMARY KEY,
			email TEXT UNIQUE NOT NULL,
			hash TEXT NOT NULL,
			created TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS databases(
			name TEXT PRIMARY KEY,
			created TEXT NOT NULL,
			id TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS settings(
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS logs(
			id TEXT PRIMARY KEY,
			created TEXT NOT NULL,
			level TEXT NOT NULL,
			message TEXT NOT NULL,
			data TEXT NOT NULL DEFAULT '{}',
			actor TEXT NOT NULL DEFAULT '',
			db TEXT NOT NULL DEFAULT '',
			collection TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_logs_created ON logs(created DESC)`,
		`CREATE TABLE IF NOT EXISTS api_tokens(
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			db_id TEXT NOT NULL,
			token_hash TEXT UNIQUE NOT NULL,
			prefix TEXT NOT NULL,
			created TEXT NOT NULL,
			last_used TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_api_tokens_db ON api_tokens(db_id)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	// Migration: databases get an opaque public ID. Backfill existing rows so
	// API URLs can stop exposing (and allow enumerating) database names.
	if _, err := s.db.Exec(`ALTER TABLE databases ADD COLUMN id TEXT NOT NULL DEFAULT ''`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			return err
		}
	}
	if _, err := s.db.Exec(`UPDATE databases SET id = lower(hex(randomblob(12))) WHERE id IS NULL OR id = ''`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_databases_id ON databases(id)`); err != nil {
		return err
	}
	return nil
}

func NewID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// --- admins ---

type Admin struct {
	ID    string
	Email string
	Hash  string
}

func (s *Store) AdminCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM admins`).Scan(&n)
	return n, err
}

func (s *Store) CreateAdmin(email, hash string) (*Admin, error) {
	a := &Admin{ID: NewID(), Email: email, Hash: hash}
	_, err := s.db.Exec(`INSERT INTO admins(id,email,hash,created) VALUES(?,?,?,?)`,
		a.ID, a.Email, a.Hash, now())
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Store) GetAdminByEmail(email string) (*Admin, error) {
	a := &Admin{}
	err := s.db.QueryRow(`SELECT id,email,hash FROM admins WHERE email=?`, email).
		Scan(&a.ID, &a.Email, &a.Hash)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Store) GetAdmin(id string) (*Admin, error) {
	a := &Admin{}
	err := s.db.QueryRow(`SELECT id,email,hash FROM admins WHERE id=?`, id).
		Scan(&a.ID, &a.Email, &a.Hash)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Store) ListAdmins() ([]Admin, error) {
	rows, err := s.db.Query(`SELECT id,email,hash FROM admins ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Admin
	for rows.Next() {
		var a Admin
		if err := rows.Scan(&a.ID, &a.Email, &a.Hash); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateAdmin changes the email and/or password hash (empty keeps the old hash).
func (s *Store) UpdateAdmin(id, email, hash string) error {
	if hash != "" {
		_, err := s.db.Exec(`UPDATE admins SET email=?, hash=? WHERE id=?`, email, hash, id)
		return err
	}
	_, err := s.db.Exec(`UPDATE admins SET email=? WHERE id=?`, email, id)
	return err
}

func (s *Store) DeleteAdmin(id string) error {
	_, err := s.db.Exec(`DELETE FROM admins WHERE id=?`, id)
	return err
}

// --- databases ---

type Database struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Created string `json:"created"`
}

func (s *Store) CreateDatabase(name string) (*Database, error) {
	d := &Database{ID: NewID(), Name: name, Created: now()}
	_, err := s.db.Exec(`INSERT INTO databases(name,created,id) VALUES(?,?,?)`, d.Name, d.Created, d.ID)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Store) ListDatabases() ([]Database, error) {
	rows, err := s.db.Query(`SELECT id,name,created FROM databases ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Database{}
	for rows.Next() {
		var d Database
		if err := rows.Scan(&d.ID, &d.Name, &d.Created); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDatabaseByName looks a database up by its unique display name.
func (s *Store) GetDatabaseByName(name string) (*Database, error) {
	d := &Database{}
	err := s.db.QueryRow(`SELECT id,name,created FROM databases WHERE name=?`, name).
		Scan(&d.ID, &d.Name, &d.Created)
	if err != nil {
		return nil, err
	}
	return d, nil
}

// GetDatabaseByID looks a database up by its opaque public ID.
func (s *Store) GetDatabaseByID(id string) (*Database, error) {
	d := &Database{}
	err := s.db.QueryRow(`SELECT id,name,created FROM databases WHERE id=?`, id).
		Scan(&d.ID, &d.Name, &d.Created)
	if err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Store) DeleteDatabaseByID(id string) error {
	_, err := s.db.Exec(`DELETE FROM databases WHERE id=?`, id)
	return err
}

// --- settings ---

// BackupTo writes a consistent copy of the system database using VACUUM INTO.
func (s *Store) BackupTo(path string) error {
	_ = os.Remove(path)
	_, err := s.db.Exec(`VACUUM INTO '` + strings.ReplaceAll(path, "'", "''") + `'`)
	return err
}

func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	return v, err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// --- audit logs ---

type LogEntry struct {
	ID         string `json:"id"`
	Created    string `json:"created"`
	Level      string `json:"level"`
	Message    string `json:"message"`
	Data       string `json:"data"`
	Actor      string `json:"actor"`
	DB         string `json:"db"`
	Collection string `json:"collection"`
}

func (s *Store) AddLog(e LogEntry) error {
	if e.ID == "" {
		e.ID = NewID()
	}
	if e.Created == "" {
		e.Created = now()
	}
	if e.Data == "" {
		e.Data = "{}"
	}
	if e.Level == "" {
		e.Level = "info"
	}
	_, err := s.db.Exec(`INSERT INTO logs(id,created,level,message,data,actor,db,collection)
		VALUES(?,?,?,?,?,?,?,?)`,
		e.ID, e.Created, e.Level, e.Message, e.Data, e.Actor, e.DB, e.Collection)
	return err
}

func (s *Store) ListLogs(level, db string, page, perPage int) ([]LogEntry, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 200 {
		perPage = 30
	}
	var where []string
	var args []any
	if level != "" {
		where = append(where, "level = ?")
		args = append(args, level)
	}
	if db != "" {
		where = append(where, "db = ?")
		args = append(args, db)
	}
	clause := ""
	if len(where) > 0 {
		clause = "WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM logs "+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := "SELECT id,created,level,message,data,actor,db,collection FROM logs " + clause +
		" ORDER BY created DESC LIMIT ? OFFSET ?"
	args = append(args, perPage, (page-1)*perPage)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []LogEntry
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.ID, &e.Created, &e.Level, &e.Message, &e.Data, &e.Actor, &e.DB, &e.Collection); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

func (s *Store) DeleteOldLogs(before time.Time) error {
	_, err := s.db.Exec(`DELETE FROM logs WHERE created < ?`, before.UTC().Format(time.RFC3339Nano))
	return err
}

// --- API tokens (per-database machine credentials) ---

type APIToken struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	DBID     string `json:"dbId"`
	DBName   string `json:"dbName"`
	Prefix   string `json:"prefix"`
	Created  string `json:"created"`
	LastUsed string `json:"lastUsed"`
}

func (s *Store) CreateAPIToken(name, dbID, hash, prefix string) (*APIToken, error) {
	t := &APIToken{ID: NewID(), Name: name, DBID: dbID, Prefix: prefix, Created: now()}
	_, err := s.db.Exec(`INSERT INTO api_tokens(id,name,db_id,token_hash,prefix,created) VALUES(?,?,?,?,?,?)`,
		t.ID, t.Name, t.DBID, hash, t.Prefix, t.Created)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Store) ListAPITokens() ([]APIToken, error) {
	rows, err := s.db.Query(`SELECT t.id,t.name,t.db_id,COALESCE(d.name,''),t.prefix,t.created,t.last_used
		FROM api_tokens t LEFT JOIN databases d ON d.id = t.db_id ORDER BY t.created DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIToken{}
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.DBID, &t.DBName, &t.Prefix, &t.Created, &t.LastUsed); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetAPITokenByHash resolves an active token by its SHA-256 hash.
func (s *Store) GetAPITokenByHash(hash string) (*APIToken, error) {
	t := &APIToken{}
	err := s.db.QueryRow(`SELECT t.id,t.name,t.db_id,COALESCE(d.name,''),t.prefix,t.created,t.last_used
		FROM api_tokens t LEFT JOIN databases d ON d.id = t.db_id WHERE t.token_hash = ?`, hash).
		Scan(&t.ID, &t.Name, &t.DBID, &t.DBName, &t.Prefix, &t.Created, &t.LastUsed)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Store) TouchAPIToken(id string) error {
	_, err := s.db.Exec(`UPDATE api_tokens SET last_used = ? WHERE id = ?`, now(), id)
	return err
}

func (s *Store) DeleteAPIToken(id string) error {
	_, err := s.db.Exec(`DELETE FROM api_tokens WHERE id = ?`, id)
	return err
}
