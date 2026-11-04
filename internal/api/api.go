package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"sakudata/internal/auth"
	"sakudata/internal/rules"
	"sakudata/internal/store"
	"sakudata/internal/tenant"
)

type Server struct {
	store   *store.Store
	tenants *tenant.Manager
	secret  []byte
	dataDir string
	hub     *tenant.Hub

	storeMu   sync.Mutex
	storeHash string
	fileStore tenant.FileStore
	backupMu  sync.Mutex
}

func NewServer(st *store.Store, tm *tenant.Manager, secret []byte, dataDir string) *Server {
	return &Server{store: st, tenants: tm, secret: secret, dataDir: dataDir, hub: tenant.NewHub()}
}

// files returns the active file backend (local disk, or S3 when configured).
func (s *Server) files() tenant.FileStore {
	cfg := s.s3Config()
	want := "local"
	if cfg.Enabled {
		want = strings.Join([]string{cfg.Endpoint, cfg.Region, cfg.Bucket, cfg.AccessKey, cfg.SecretKey, cfg.Prefix, boolStr(cfg.ForcePathStyle)}, "|")
	}
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	if s.fileStore != nil && s.storeHash == want {
		return s.fileStore
	}
	var fs tenant.FileStore = tenant.NewLocalStore(s.dataDir)
	if cfg.Enabled {
		if s3fs, err := tenant.NewS3Store(context.Background(), cfg); err == nil {
			fs = s3fs
		}
	}
	s.fileStore, s.storeHash = fs, want
	return fs
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func (s *Server) s3Config() tenant.S3Config {
	get := func(k string) string {
		v, _ := s.store.GetSetting("s3_" + k)
		return v
	}
	return tenant.S3Config{
		Enabled:        get("enabled") == "1",
		Endpoint:       get("endpoint"),
		Region:         get("region"),
		Bucket:         get("bucket"),
		AccessKey:      get("access"),
		SecretKey:      get("secret"),
		Prefix:         get("prefix"),
		ForcePathStyle: get("pathstyle") == "1",
	}
}

// emit broadcasts a record event to SSE subscribers and webhooks.
func (s *Server) emit(dbName string, c *tenant.Collection, action string, rec map[string]any) {
	e := tenant.Event{Event: action, DB: dbName, Collection: c.Name, Record: rec}
	s.hub.Broadcast(e)
	if urls := tenant.HooksFor(c, action); len(urls) > 0 {
		tenant.FireWebhooks(urls, e)
	}
}

func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/meta", s.publicMeta)
	mux.HandleFunc("POST /api/admin/setup", s.adminSetup)
	mux.HandleFunc("POST /api/admin/login", s.adminLogin)
	mux.HandleFunc("GET /api/admin/settings", s.requireAdmin(s.getSettings))
	mux.HandleFunc("PUT /api/admin/settings", s.requireAdmin(s.putSettings))
	mux.HandleFunc("GET /api/admin/logs", s.requireAdmin(s.listLogs))
	mux.HandleFunc("GET /api/admin/backups", s.requireAdmin(s.listBackups))
	mux.HandleFunc("POST /api/admin/backups", s.requireAdmin(s.createBackupHandler))
	mux.HandleFunc("GET /api/admin/backups/{name}", s.requireAdmin(s.downloadBackup))
	mux.HandleFunc("DELETE /api/admin/backups/{name}", s.requireAdmin(s.deleteBackup))
	mux.HandleFunc("POST /api/admin/backups/{name}/restore", s.requireAdmin(s.restoreBackup))
	mux.HandleFunc("GET /api/admin/tokens", s.requireAdmin(s.listAPITokens))
	mux.HandleFunc("POST /api/admin/tokens", s.requireAdmin(s.createAPIToken))
	mux.HandleFunc("DELETE /api/admin/tokens/{id}", s.requireAdmin(s.deleteAPIToken))
	mux.HandleFunc("GET /api/admin/superusers", s.requireAdmin(s.listSuperusers))
	mux.HandleFunc("POST /api/admin/superusers", s.requireAdmin(s.createSuperuser))
	mux.HandleFunc("PATCH /api/admin/superusers/{id}", s.requireAdmin(s.updateSuperuser))
	mux.HandleFunc("DELETE /api/admin/superusers/{id}", s.requireAdmin(s.deleteSuperuser))
	mux.HandleFunc("GET /api/admin/databases", s.requireAdmin(s.listDatabases))
	mux.HandleFunc("POST /api/admin/databases", s.requireAdmin(s.createDatabase))
	mux.HandleFunc("DELETE /api/admin/databases/{id}", s.requireAdmin(s.deleteDatabase))
	mux.HandleFunc("POST /api/d/{db}/sql", s.requireAdmin(s.runSQL))
	mux.HandleFunc("GET /api/d/{db}/collections", s.requireAdmin(s.listCollections))
	mux.HandleFunc("POST /api/d/{db}/collections", s.requireAdmin(s.createCollection))
	mux.HandleFunc("DELETE /api/d/{db}/collections/{name}", s.requireAdmin(s.deleteCollection))
	mux.HandleFunc("GET /api/d/{db}/collections/{name}", s.requireAdmin(s.getCollection))
	mux.HandleFunc("PATCH /api/d/{db}/collections/{name}", s.requireAdmin(s.updateCollectionMeta))
	mux.HandleFunc("PUT /api/d/{db}/collections/{name}", s.requireAdmin(s.updateCollectionMeta))
	mux.HandleFunc("POST /api/d/{db}/c/{col}/auth/login", s.userLogin)
	mux.HandleFunc("POST /api/d/{db}/c/{col}/auth/refresh", s.userRefresh)
	mux.HandleFunc("GET /api/admin/settings/s3", s.requireAdmin(s.getS3Settings))
	mux.HandleFunc("PUT /api/admin/settings/s3", s.requireAdmin(s.putS3Settings))
	mux.HandleFunc("POST /api/d/{db}/c/{col}/records/{id}/files/{field}", s.access("update", s.uploadFile))
	mux.HandleFunc("GET /api/d/{db}/c/{col}/records/{id}/files/{field}", s.access("view", s.downloadFile))
	mux.HandleFunc("GET /api/d/{db}/c/{col}/records/{id}/files/{field}/{name}", s.access("view", s.downloadFile))
	mux.HandleFunc("DELETE /api/d/{db}/c/{col}/records/{id}/files/{field}", s.access("update", s.deleteFile))
	mux.HandleFunc("GET /api/d/{db}/c/{col}/subscribe", s.access("view", s.subscribe))
	mux.HandleFunc("GET /api/d/{db}/c/{col}/export", s.access("list", s.exportRecords))
	mux.HandleFunc("POST /api/d/{db}/c/{col}/import", s.access("create", s.importRecords))
	mux.HandleFunc("GET /api/d/{db}/c/{col}/records", s.access("list", s.listRecords))
	mux.HandleFunc("POST /api/d/{db}/c/{col}/records", s.access("create", s.createRecord))
	mux.HandleFunc("GET /api/d/{db}/c/{col}/records/{id}", s.access("view", s.getRecord))
	mux.HandleFunc("PATCH /api/d/{db}/c/{col}/records/{id}", s.access("update", s.updateRecord))
	mux.HandleFunc("DELETE /api/d/{db}/c/{col}/records/{id}", s.access("delete", s.deleteRecord))
	return mux
}

// Wrap applies the server middleware chain to an arbitrary handler. It is used
// by main to cover the admin UI routes as well as the API mux.
func (s *Server) Wrap(h http.Handler) http.Handler {
	return s.middleware(h)
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"code": code, "message": msg, "data": map[string]any{}})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	return dec.Decode(v)
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		h = r.Header.Get("X-API-Key")
	}
	if h == "" {
		// EventSource cannot set headers; allow ?token= for SSE.
		return strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return strings.TrimSpace(h)
}

func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cl, err := auth.Parse(s.secret, bearer(r))
		if err != nil || cl.Role != "admin" {
			writeErr(w, http.StatusUnauthorized, "Missing or invalid admin token.")
			return
		}
		next(w, r)
	}
}

// ctx carries the resolved tenant database, collection and caller.
type ctxKey string

const (
	ctxDB     ctxKey = "db"
	ctxCol    ctxKey = "col"
	ctxUser   ctxKey = "user"
	ctxRule   ctxKey = "rule"
	ctxAuth   ctxKey = "auth"
	ctxDBID   ctxKey = "dbid"
	ctxDBName ctxKey = "dbname"
)

// access enforces collection rules: "" admin-only, "*" public, "user" any
// authenticated user of the same database and auth collection. Admin tokens
// bypass every rule. Handlers read the resolved values via fromCtx.
func (s *Server) access(op string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dbID := r.PathValue("db")
		raw := bearer(r)
		db, c, dbName, code, msg := s.resolve(r, true)
		if code != 0 {
			// Never reveal whether a database/collection exists to anonymous
			// callers: always answer 401 in that case.
			if raw == "" {
				writeErr(w, http.StatusUnauthorized, "Missing or invalid token.")
				return
			}
			writeErr(w, code, msg)
			return
		}
		if strings.HasPrefix(raw, apiTokenPrefix) {
			tk, err := s.store.GetAPITokenByHash(hashAPIToken(raw))
			if err != nil {
				writeErr(w, http.StatusUnauthorized, "Missing or invalid token.")
				return
			}
			if tk.DBID != dbID {
				writeErr(w, http.StatusForbidden, "This token is not valid for this database.")
				return
			}
			_ = s.store.TouchAPIToken(tk.ID)
			ctx := withCtx(r, db, c, nil, dbID, dbName)
			ctx = context.WithValue(ctx, ctxRule, "*")
			next(w, r.WithContext(ctx))
			return
		}
		var user *auth.Claims
		hasToken := raw != ""
		if hasToken {
			if cl, err := auth.Parse(s.secret, raw); err == nil {
				if cl.Role == "admin" {
					ctx := withCtx(r, db, c, nil, dbID, dbName)
					ctx = context.WithValue(ctx, ctxRule, "*")
					next(w, r.WithContext(ctx))
					return
				}
				if cl.Role == "user" && cl.DB == dbID && cl.Col == c.Name {
					user = cl
				}
			}
		}
		var rule string
		switch op {
		case "list":
			rule = c.Rules.List
		case "view":
			rule = c.Rules.View
		case "create":
			rule = c.Rules.Create
		case "update":
			rule = c.Rules.Update
		case "delete":
			rule = c.Rules.Delete
		}
		rule = strings.TrimSpace(rule)
		var authCtx *rules.Auth
		if user != nil {
			authCtx = &rules.Auth{LoggedIn: true, UserID: user.Sub, DB: user.DB, Collection: user.Col, Email: user.Email}
		}
		// Coarse gate. Expression rules are enforced per record in the handlers
		// (and as a WHERE clause for list).
		switch rule {
		case "":
			if !hasToken {
				writeErr(w, http.StatusUnauthorized, "Missing or invalid token.")
			} else {
				writeErr(w, http.StatusForbidden, "The authorized record is not allowed to perform this action.")
			}
			return
		case "user":
			if authCtx == nil {
				if !hasToken {
					writeErr(w, http.StatusUnauthorized, "Missing or invalid token.")
				} else {
					writeErr(w, http.StatusForbidden, "The authorized record is not allowed to perform this action.")
				}
				return
			}
		}
		ctx := withCtx(r, db, c, user, dbID, dbName)
		ctx = context.WithValue(ctx, ctxRule, rule)
		ctx = context.WithValue(ctx, ctxAuth, authCtx)
		next(w, r.WithContext(ctx))
	}
}

// ruleFromCtx returns the resolved access rule and caller context.
func ruleFromCtx(r *http.Request) (string, *rules.Auth) {
	rule, _ := r.Context().Value(ctxRule).(string)
	a, _ := r.Context().Value(ctxAuth).(*rules.Auth)
	return rule, a
}

// recordInput converts a stored record back into writable field values.
func recordInput(c *tenant.Collection, rec map[string]any) map[string]any {
	out := map[string]any{}
	for _, f := range c.Fields {
		if f.Name == "password" {
			continue
		}
		v, ok := rec[f.Name]
		if !ok {
			continue
		}
		if f.Type == "json" && v != nil {
			if _, isStr := v.(string); !isStr {
				if b, err := json.Marshal(v); err == nil {
					out[f.Name] = string(b)
					continue
				}
			}
		}
		out[f.Name] = v
	}
	return out
}

func schemaOfCollection(c *tenant.Collection) rules.Schema {
	s := rules.Schema{}
	for _, f := range c.Fields {
		s[f.Name] = f.Type
	}
	return s
}

// authorizeRecord evaluates the current operation rule against a single record.
func (s *Server) authorizeRecord(r *http.Request, rec map[string]any) (bool, error) {
	_, c := fromCtx(r)
	rule, a := ruleFromCtx(r)
	return rules.Eval(rule, rec, schemaOfCollection(c), a)
}

func (s *Server) denyRecord(w http.ResponseWriter) {
	writeErr(w, http.StatusForbidden, "The authorized record is not allowed to perform this action.")
}

func withCtx(r *http.Request, db *sql.DB, c *tenant.Collection, u *auth.Claims, dbID, dbName string) context.Context {
	ctx := context.WithValue(r.Context(), ctxDB, db)
	ctx = context.WithValue(ctx, ctxCol, c)
	ctx = context.WithValue(ctx, ctxDBID, dbID)
	ctx = context.WithValue(ctx, ctxDBName, dbName)
	if u != nil {
		ctx = context.WithValue(ctx, ctxUser, u)
	}
	return ctx
}

func fromCtx(r *http.Request) (*sql.DB, *tenant.Collection) {
	db, _ := r.Context().Value(ctxDB).(*sql.DB)
	c, _ := r.Context().Value(ctxCol).(*tenant.Collection)
	return db, c
}

func dbNameFromCtx(r *http.Request) string {
	v, _ := r.Context().Value(ctxDBName).(string)
	return v
}

func dbIDFromCtx(r *http.Request) string {
	v, _ := r.Context().Value(ctxDBID).(string)
	return v
}

// resolve maps the opaque database ID in the URL to its tenant database and
// (optionally) loads a collection. It returns a status code and a uniform
// message on failure so callers cannot tell a missing DB from a missing
// collection.
func (s *Server) resolve(r *http.Request, needCol bool) (*sql.DB, *tenant.Collection, string, int, string) {
	dbID := r.PathValue("db")
	rec, err := s.store.GetDatabaseByID(dbID)
	if err != nil {
		// Legacy compatibility: allow the old name-based URLs while the UI and
		// new clients use the opaque ID.
		if !tenant.ValidName(dbID) {
			return nil, nil, "", http.StatusNotFound, "Not found."
		}
		rec, err = s.store.GetDatabaseByName(dbID)
		if err != nil {
			return nil, nil, "", http.StatusNotFound, "Not found."
		}
	}
	dbName := rec.Name
	db, err := s.tenants.Open(dbName)
	if err != nil {
		return nil, nil, "", http.StatusInternalServerError, "Cannot open database."
	}
	if !needCol {
		return db, nil, dbName, 0, ""
	}
	c, err := s.tenants.GetCollection(db, r.PathValue("col"))
	if err != nil {
		return nil, nil, "", http.StatusNotFound, "Not found."
	}
	return db, c, dbName, 0, ""
}

// --- handlers ---

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"message": "API is healthy.", "code": 200, "data": map[string]any{},
	})
}

func (s *Server) adminSetup(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.AdminCount()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Setup failed.")
		return
	}
	if n > 0 {
		writeErr(w, http.StatusConflict, "Admin already exists.")
		return
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil || in.Email == "" {
		writeErr(w, http.StatusBadRequest, "Email and password are required.")
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a, err := s.store.CreateAdmin(in.Email, hash)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "Cannot create admin (email may be taken).")
		return
	}
	cl := auth.AdminClaims(a.ID, 12*time.Hour)
	cl.Email = a.Email
	tok, _ := auth.Sign(s.secret, cl)
	writeJSON(w, http.StatusOK, map[string]any{
		"token": tok, "record": map[string]any{"id": a.ID, "email": a.Email},
	})
}

func (s *Server) adminLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Identity string `json:"identity"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	email := in.Identity
	if email == "" {
		email = in.Email
	}
	a, err := s.store.GetAdminByEmail(email)
	if err != nil || !auth.VerifyPassword(a.Hash, in.Password) {
		writeErr(w, http.StatusUnauthorized, "Invalid email or password.")
		return
	}
	cl := auth.AdminClaims(a.ID, 12*time.Hour)
	cl.Email = a.Email
	tok, _ := auth.Sign(s.secret, cl)
	s.log(r, "info", "admin login", map[string]any{"email": a.Email}, "", "")
	writeJSON(w, http.StatusOK, map[string]any{
		"token": tok, "record": map[string]any{"id": a.ID, "email": a.Email},
	})
}

func (s *Server) listDatabases(w http.ResponseWriter, r *http.Request) {
	dbs, err := s.store.ListDatabases()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Cannot list databases.")
		return
	}
	writeJSON(w, http.StatusOK, dbs)
}

func (s *Server) createDatabase(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil || !tenant.ValidName(in.Name) {
		writeErr(w, http.StatusBadRequest, "Invalid database name (letters, digits, underscore).")
		return
	}
	if _, err := s.store.GetDatabaseByName(in.Name); err == nil {
		writeErr(w, http.StatusConflict, "Database already exists.")
		return
	}
	if _, err := s.tenants.Open(in.Name); err != nil {
		writeErr(w, http.StatusInternalServerError, "Cannot create database file.")
		return
	}
	d, err := s.store.CreateDatabase(in.Name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Cannot register database.")
		return
	}
	s.log(r, "info", "database created", nil, d.Name, "")
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) deleteDatabase(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rec, err := s.store.GetDatabaseByID(id)
	if err != nil && tenant.ValidName(id) {
		rec, err = s.store.GetDatabaseByName(id)
	}
	if err != nil {
		writeErr(w, http.StatusNotFound, "Not found.")
		return
	}
	name := rec.Name
	s.tenants.Close(name)
	s.tenants.DropFile(name)
	_ = s.files().DropDB(name)
	if err := s.store.DeleteDatabaseByID(rec.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "Cannot delete database.")
		return
	}
	s.log(r, "warn", "database deleted", nil, name, "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listCollections(w http.ResponseWriter, r *http.Request) {
	db, _, _, code, msg := s.resolve(r, false)
	if code != 0 {
		writeErr(w, code, msg)
		return
	}
	cols, err := s.tenants.ListCollections(db)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Cannot list collections.")
		return
	}
	if cols == nil {
		cols = []tenant.Collection{}
	}
	writeJSON(w, http.StatusOK, cols)
}

func (s *Server) createCollection(w http.ResponseWriter, r *http.Request) {
	db, _, dbName, code, msg := s.resolve(r, false)
	if code != 0 {
		writeErr(w, code, msg)
		return
	}
	var c tenant.Collection
	if err := readJSON(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid collection definition.")
		return
	}
	if err := s.tenants.CreateCollection(db, &c); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			writeErr(w, http.StatusConflict, err.Error())
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, _ := s.tenants.GetCollection(db, c.Name)
	s.log(r, "info", "collection created", nil, dbName, c.Name)
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) deleteCollection(w http.ResponseWriter, r *http.Request) {
	db, _, dbName, code, msg := s.resolve(r, false)
	if code != 0 {
		writeErr(w, code, msg)
		return
	}
	name := r.PathValue("name")
	if _, err := s.tenants.GetCollection(db, name); err != nil {
		writeErr(w, http.StatusNotFound, "Collection not found.")
		return
	}
	_ = s.files().DropPrefix(dbName, name)
	if err := s.tenants.DropCollection(db, name); err != nil {
		writeErr(w, http.StatusInternalServerError, "Cannot delete collection.")
		return
	}
	s.log(r, "warn", "collection deleted", nil, dbName, name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getCollection(w http.ResponseWriter, r *http.Request) {
	db, _, _, code, msg := s.resolve(r, false)
	if code != 0 {
		writeErr(w, code, msg)
		return
	}
	c, err := s.tenants.GetCollection(db, r.PathValue("name"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "Collection not found.")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) updateCollectionMeta(w http.ResponseWriter, r *http.Request) {
	db, _, dbName, code, msg := s.resolve(r, false)
	if code != 0 {
		writeErr(w, code, msg)
		return
	}
	var in struct {
		Rules   *tenant.Rules       `json:"rules"`
		Hooks   map[string][]string `json:"hooks"`
		Options map[string]any      `json:"options"`
		Fields  *[]tenant.Field     `json:"fields"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	name := r.PathValue("name")
	if in.Fields != nil {
		if _, err := s.tenants.SetCollectionSchema(db, name, *in.Fields); err != nil {
			if strings.Contains(err.Error(), "no such") || strings.Contains(err.Error(), "no row") {
				writeErr(w, http.StatusNotFound, "Collection not found.")
				return
			}
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	updated, err := s.tenants.UpdateMeta(db, name, in.Rules, in.Hooks, in.Options)
	if err != nil {
		if strings.Contains(err.Error(), "no such") || strings.Contains(err.Error(), "no row") {
			writeErr(w, http.StatusNotFound, "Collection not found.")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log(r, "info", "collection updated", map[string]any{"fields": in.Fields != nil}, dbName, name)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) userLogin(w http.ResponseWriter, r *http.Request) {
	db, c, dbName, code, msg := s.resolve(r, true)
	if code != 0 {
		writeErr(w, code, msg)
		return
	}
	dbID := r.PathValue("db")
	if c.Type != "auth" {
		writeErr(w, http.StatusBadRequest, "Collection is not an auth collection.")
		return
	}
	var in struct {
		Identity string `json:"identity"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	id := in.Identity
	if id == "" {
		id = in.Email
	}
	rec, err := s.tenants.AuthenticateUser(db, c, id, in.Password)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "Invalid identity or password.")
		return
	}
	cl := auth.UserClaims(rec["id"].(string), dbID, c.Name, 7*24*time.Hour)
	if em, _ := rec["email"].(string); em != "" {
		cl.Email = em
	}
	tok, _ := auth.Sign(s.secret, cl)
	s.log(r, "info", "user login", map[string]any{"email": cl.Email}, dbName, c.Name)
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "record": rec})
}

func (s *Server) userRefresh(w http.ResponseWriter, r *http.Request) {
	cl, err := auth.Parse(s.secret, bearer(r))
	if err != nil || cl.Role != "user" || cl.DB != r.PathValue("db") || cl.Col != r.PathValue("col") {
		writeErr(w, http.StatusUnauthorized, "Missing or invalid token.")
		return
	}
	db, c, _, code, msg := s.resolve(r, true)
	if code != 0 {
		writeErr(w, code, msg)
		return
	}
	rec, err := s.tenants.GetRecord(db, c, cl.Sub)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "Account no longer exists.")
		return
	}
	next := auth.UserClaims(cl.Sub, cl.DB, cl.Col, 7*24*time.Hour)
	next.Email = cl.Email
	tok, _ := auth.Sign(s.secret, next)
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "record": rec})
}

// --- S3 settings (admin; secret is never returned) ---

var s3Fields = []string{"enabled", "endpoint", "region", "bucket", "access", "secret", "prefix", "pathstyle"}

func (s *Server) getS3Settings(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"backend": "local"}
	for _, k := range s3Fields {
		v, _ := s.store.GetSetting("s3_" + k)
		out[k] = v
	}
	if sec, _ := out["secret"].(string); sec != "" {
		out["secret"] = "••••••"
	}
	if out["enabled"] == "1" {
		out["backend"] = "s3"
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) putS3Settings(w http.ResponseWriter, r *http.Request) {
	var in map[string]string
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid settings.")
		return
	}
	for _, k := range s3Fields {
		v, ok := in[k]
		if !ok {
			continue
		}
		if k == "secret" && (v == "" || v == "••••••") {
			continue // keep existing secret
		}
		if (k == "enabled" || k == "pathstyle") && v != "1" {
			v = "0"
		}
		if err := s.store.SetSetting("s3_"+k, v); err != nil {
			writeErr(w, http.StatusInternalServerError, "Cannot save settings.")
			return
		}
	}
	s.getS3Settings(w, r)
}

// --- files ---

func fileField(c *tenant.Collection, name string) *tenant.Field {
	for i := range c.Fields {
		if c.Fields[i].Name == name && c.Fields[i].Type == "file" {
			return &c.Fields[i]
		}
	}
	return nil
}

func (s *Server) removeFiles(dbName string, c *tenant.Collection, rec map[string]any) {
	fs := s.files()
	id, _ := rec["id"].(string)
	for _, f := range c.Fields {
		if f.Type != "file" {
			continue
		}
		if stored, _ := rec[f.Name].(string); stored != "" {
			_ = fs.Delete(dbName, c.Name, id, f.Name, stored)
		}
	}
}

func (s *Server) uploadFile(w http.ResponseWriter, r *http.Request) {
	db, c := fromCtx(r)
	dbName, id, field := dbNameFromCtx(r), r.PathValue("id"), r.PathValue("field")
	if fileField(c, field) == nil {
		writeErr(w, http.StatusBadRequest, "Not a file field.")
		return
	}
	rec, err := s.tenants.GetRecord(db, c, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "Record not found.")
		return
	}
	if ok, err := s.authorizeRecord(r, rec); err != nil || !ok {
		s.denyRecord(w)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid multipart upload.")
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "Multipart field \"file\" is required.")
		return
	}
	defer f.Close()
	stored, _, err := s.files().Put(dbName, c.Name, id, field, hdr.Filename, f)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := s.tenants.UpdateRecord(db, c, id, map[string]any{field: stored})
	if err != nil {
		_ = s.files().Delete(dbName, c.Name, id, field, stored)
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if old, _ := rec[field].(string); old != "" && old != stored {
		_ = s.files().Delete(dbName, c.Name, id, field, old)
	}
	s.emit(dbName, c, "update", updated)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) downloadFile(w http.ResponseWriter, r *http.Request) {
	db, c := fromCtx(r)
	rec, err := s.tenants.GetRecord(db, c, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "Record not found.")
		return
	}
	if ok, err := s.authorizeRecord(r, rec); err != nil || !ok {
		s.denyRecord(w)
		return
	}
	field := r.PathValue("field")
	if fileField(c, field) == nil {
		writeErr(w, http.StatusBadRequest, "Not a file field.")
		return
	}
	stored, _ := rec[field].(string)
	if stored == "" {
		writeErr(w, http.StatusNotFound, "No file attached.")
		return
	}
	rc, size, err := s.files().Get(dbNameFromCtx(r), c.Name, r.PathValue("id"), field, stored)
	if err != nil {
		writeErr(w, http.StatusNotFound, "File not found in storage.")
		return
	}
	defer rc.Close()
	if ct := mime.TypeByExtension(filepath.Ext(stored)); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Disposition", "inline")
	_, _ = io.Copy(w, rc)
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	db, c := fromCtx(r)
	dbName, id, field := dbNameFromCtx(r), r.PathValue("id"), r.PathValue("field")
	if fileField(c, field) == nil {
		writeErr(w, http.StatusBadRequest, "Not a file field.")
		return
	}
	rec, err := s.tenants.GetRecord(db, c, id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "Record not found.")
		return
	}
	if ok, err := s.authorizeRecord(r, rec); err != nil || !ok {
		s.denyRecord(w)
		return
	}
	if old, _ := rec[field].(string); old != "" {
		_ = s.files().Delete(dbName, c.Name, id, field, old)
	}
	updated, err := s.tenants.UpdateRecord(db, c, id, map[string]any{field: ""})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.emit(dbName, c, "update", updated)
	writeJSON(w, http.StatusOK, updated)
}

// --- realtime (SSE) ---

func (s *Server) subscribe(w http.ResponseWriter, r *http.Request) {
	// access("view") already authorized this request.
	_, c := fromCtx(r)
	dbName := dbNameFromCtx(r)
	ch, unsub := s.hub.Subscribe(dbName, c.Name)
	defer unsub()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "Streaming unsupported.")
		return
	}
	_, _ = w.Write([]byte(": connected\n\n"))
	fl.Flush()

	tick := time.NewTicker(25 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			if rule, a := ruleFromCtx(r); rule != "" && rule != "*" && rule != "user" && e.Event != "delete" {
				if allowed, err := rules.Eval(rule, e.Record, schemaOfCollection(c), a); err != nil || !allowed {
					continue
				}
			}
			raw, _ := json.Marshal(e)
			_, _ = w.Write([]byte("data: " + string(raw) + "\n\n"))
			fl.Flush()
		case <-tick.C:
			_, _ = w.Write([]byte(": ping\n\n"))
			fl.Flush()
		}
	}
}

func (s *Server) listRecords(w http.ResponseWriter, r *http.Request) {
	db, c := fromCtx(r)
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	perPage, _ := strconv.Atoi(q.Get("perPage"))
	skipTotal := q.Get("skipTotal") == "1" || q.Get("skipTotal") == "true"

	params := map[string]string{}
	for k, v := range q {
		switch k {
		case "page", "perPage", "sort", "filter", "fields", "expand", "skipTotal", "token":
			continue
		}
		if len(v) > 0 {
			params[k] = v[0]
		}
	}

	rule, authCtx := ruleFromCtx(r)
	var clauses []string
	if rule != "" && rule != "*" && rule != "user" {
		clauses = append(clauses, "("+rule+")")
	}
	if f := strings.TrimSpace(q.Get("filter")); f != "" {
		clauses = append(clauses, "("+f+")")
	}

	items, total, err := s.tenants.ListRecords(db, c, tenant.ListOptions{
		Page:       page,
		PerPage:    perPage,
		Sort:       q.Get("sort"),
		FilterExpr: strings.Join(clauses, " && "),
		Params:     params,
		Auth:       authCtx,
		SkipTotal:  skipTotal,
	})
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if items == nil {
		items = []map[string]any{}
	}
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 200 {
		perPage = 20
	}
	if fields := q.Get("fields"); fields != "" {
		keep := map[string]bool{}
		for _, f := range strings.Split(fields, ",") {
			keep[strings.TrimSpace(f)] = true
		}
		for _, item := range items {
			for k := range item {
				if !keep[k] {
					delete(item, k)
				}
			}
		}
	}
	pages := 0
	if !skipTotal {
		pages = (total + perPage - 1) / perPage
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"page": page, "perPage": perPage, "totalItems": total,
		"totalPages": pages, "items": items,
	})
}

func (s *Server) createRecord(w http.ResponseWriter, r *http.Request) {
	db, c := fromCtx(r)
	var data map[string]any
	if err := readJSON(r, &data); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid record data.")
		return
	}
	rec, err := s.tenants.CreateRecord(db, c, data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if ok, err := s.authorizeRecord(r, rec); err != nil || !ok {
		_ = s.tenants.DeleteRecord(db, c, rec["id"].(string))
		s.denyRecord(w)
		return
	}
	dbName := dbNameFromCtx(r)
	s.log(r, "info", "record created", map[string]any{"id": rec["id"]}, dbName, c.Name)
	s.emit(dbName, c, "create", rec)
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) getRecord(w http.ResponseWriter, r *http.Request) {
	db, c := fromCtx(r)
	rec, err := s.tenants.GetRecord(db, c, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "Record not found.")
			return
		}
		writeErr(w, http.StatusInternalServerError, "Cannot load record.")
		return
	}
	if ok, err := s.authorizeRecord(r, rec); err != nil || !ok {
		s.denyRecord(w)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) updateRecord(w http.ResponseWriter, r *http.Request) {
	db, c := fromCtx(r)
	var data map[string]any
	if err := readJSON(r, &data); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid record data.")
		return
	}
	existing, err := s.tenants.GetRecord(db, c, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "Record not found.")
			return
		}
		writeErr(w, http.StatusInternalServerError, "Cannot load record.")
		return
	}
	if ok, err := s.authorizeRecord(r, existing); err != nil || !ok {
		s.denyRecord(w)
		return
	}
	rec, err := s.tenants.UpdateRecord(db, c, r.PathValue("id"), data)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "Record not found.")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if ok, err := s.authorizeRecord(r, rec); err != nil || !ok {
		// The row now violates the rule: restore the previous values.
		_, _ = s.tenants.UpdateRecord(db, c, r.PathValue("id"), recordInput(c, existing))
		s.denyRecord(w)
		return
	}
	dbName := dbNameFromCtx(r)
	s.log(r, "info", "record updated", map[string]any{"id": rec["id"]}, dbName, c.Name)
	s.emit(dbName, c, "update", rec)
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) deleteRecord(w http.ResponseWriter, r *http.Request) {
	db, c := fromCtx(r)
	id := r.PathValue("id")
	rec, err := s.tenants.GetRecord(db, c, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "Record not found.")
			return
		}
		writeErr(w, http.StatusInternalServerError, "Cannot load record.")
		return
	}
	if ok, err := s.authorizeRecord(r, rec); err != nil || !ok {
		s.denyRecord(w)
		return
	}
	if err := s.tenants.DeleteRecord(db, c, id); err != nil {
		writeErr(w, http.StatusInternalServerError, "Cannot delete record.")
		return
	}
	dbName := dbNameFromCtx(r)
	s.removeFiles(dbName, c, rec)
	s.log(r, "info", "record deleted", map[string]any{"id": id}, dbName, c.Name)
	s.emit(dbName, c, "delete", map[string]any{"id": id})
	w.WriteHeader(http.StatusNoContent)
}
