package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"sakudata/internal/auth"
	"sakudata/internal/store"
	"sakudata/internal/tenant"
)

// log records an audit entry, resolving the actor from the bearer token.
func (s *Server) log(r *http.Request, level, message string, data map[string]any, dbName, col string) {
	actor := ""
	if cl, err := auth.Parse(s.secret, bearer(r)); err == nil {
		actor = cl.Email
		if actor == "" {
			actor = cl.Sub
		}
	}
	raw := "{}"
	if data != nil {
		if b, err := json.Marshal(data); err == nil {
			raw = string(b)
		}
	}
	_ = s.store.AddLog(store.LogEntry{
		Level: level, Message: message, Data: raw, Actor: actor, DB: dbName, Collection: col,
	})
}

func (s *Server) publicMeta(w http.ResponseWriter, r *http.Request) {
	name, _ := s.store.GetSetting("app_name")
	if name == "" {
		name = "SakuData"
	}
	admins, _ := s.store.AdminCount()
	writeJSON(w, http.StatusOK, map[string]any{
		"appName":    name,
		"version":    "0.1.0",
		"fieldTypes": tenant.FieldTypes,
		"needsSetup": admins == 0,
	})
}

// --- application settings ---

var appSettingKeys = []string{
	"app_name", "app_base_url", "rate_limit_login",
	"mail_from", "mail_host", "mail_port", "mail_user", "mail_pass",
	"backup_enabled", "backup_interval_hours", "backup_keep", "backup_last",
}

func sensitiveKey(k string) bool {
	k = strings.ToLower(k)
	return strings.Contains(k, "secret") || strings.Contains(k, "pass") || strings.Contains(k, "key")
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	out := map[string]string{}
	for _, k := range appSettingKeys {
		out[k], _ = s.store.GetSetting(k)
	}
	// Keep the S3 settings shape the old UI expects.
	s3 := map[string]string{}
	for _, k := range s3Fields {
		s3[k], _ = s.store.GetSetting("s3_" + k)
	}
	if s3["secret"] != "" {
		s3["secret"] = "••••••"
	}
	if out["mail_pass"] != "" {
		out["mail_pass"] = "••••••"
	}
	backend := "local"
	if s3["enabled"] == "1" {
		backend = "s3"
	}
	writeJSON(w, http.StatusOK, map[string]any{"app": out, "s3": s3, "backend": backend})
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var in map[string]string
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid settings.")
		return
	}
	allowed := map[string]bool{}
	for _, k := range appSettingKeys {
		allowed[k] = true
	}
	for _, k := range s3Fields {
		allowed["s3_"+k] = true
	}
	for k, v := range in {
		if !allowed[k] || k == "jwt_secret" {
			continue
		}
		if sensitiveKey(k) && (v == "" || v == "••••••") {
			continue
		}
		if err := s.store.SetSetting(k, v); err != nil {
			writeErr(w, http.StatusInternalServerError, "Cannot save settings.")
			return
		}
	}
	s.log(r, "info", "settings updated", nil, "", "")
	s.getSettings(w, r)
}

// --- audit logs ---

func (s *Server) listLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	perPage, _ := strconv.Atoi(q.Get("perPage"))
	items, total, err := s.store.ListLogs(q.Get("level"), q.Get("db"), page, perPage)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Cannot load logs.")
		return
	}
	if items == nil {
		items = []store.LogEntry{}
	}
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 200 {
		perPage = 30
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"page": page, "perPage": perPage, "totalItems": total,
		"totalPages": (total + perPage - 1) / perPage, "items": items,
	})
}

// --- superusers ---

func (s *Server) listSuperusers(w http.ResponseWriter, r *http.Request) {
	admins, err := s.store.ListAdmins()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Cannot list superusers.")
		return
	}
	out := make([]map[string]any, 0, len(admins))
	for _, a := range admins {
		out = append(out, map[string]any{"id": a.ID, "email": a.Email})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createSuperuser(w http.ResponseWriter, r *http.Request) {
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
		writeErr(w, http.StatusBadRequest, "Cannot create superuser (email may be taken).")
		return
	}
	s.log(r, "info", "superuser created", map[string]any{"email": a.Email}, "", "")
	writeJSON(w, http.StatusOK, map[string]any{"id": a.ID, "email": a.Email})
}

func (s *Server) updateSuperuser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cur, err := s.store.GetAdmin(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "Superuser not found.")
		return
	}
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	if in.Email == "" {
		in.Email = cur.Email
	}
	hash := ""
	if in.Password != "" {
		h, err := auth.HashPassword(in.Password)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		hash = h
	}
	if err := s.store.UpdateAdmin(id, in.Email, hash); err != nil {
		writeErr(w, http.StatusBadRequest, "Cannot update superuser (email may be taken).")
		return
	}
	s.log(r, "info", "superuser updated", map[string]any{"email": in.Email}, "", "")
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "email": in.Email})
}

func (s *Server) deleteSuperuser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	n, err := s.store.AdminCount()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Cannot delete superuser.")
		return
	}
	if n <= 1 {
		writeErr(w, http.StatusConflict, "At least one superuser must remain.")
		return
	}
	if err := s.store.DeleteAdmin(id); err != nil {
		writeErr(w, http.StatusInternalServerError, "Cannot delete superuser.")
		return
	}
	s.log(r, "warn", "superuser deleted", map[string]any{"id": id}, "", "")
	w.WriteHeader(http.StatusNoContent)
}
