package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
)

const apiTokenPrefix = "mdb_"

func hashAPIToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *Server) listAPITokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.store.ListAPITokens()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Cannot list API tokens.")
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

func (s *Server) createAPIToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		DBID string `json:"dbId"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || in.DBID == "" {
		writeErr(w, http.StatusBadRequest, "Name and database are required.")
		return
	}
	rec, err := s.store.GetDatabaseByID(in.DBID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "Database not found.")
		return
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		writeErr(w, http.StatusInternalServerError, "Cannot generate token.")
		return
	}
	raw := apiTokenPrefix + base64.RawURLEncoding.EncodeToString(buf)
	prefix := raw
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	t, err := s.store.CreateAPIToken(in.Name, rec.ID, hashAPIToken(raw), prefix)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Cannot create API token.")
		return
	}
	s.log(r, "info", "api token created", map[string]any{"name": t.Name, "database": rec.Name}, rec.Name, "")
	writeJSON(w, http.StatusOK, map[string]any{
		"id": t.ID, "name": t.Name, "dbId": t.DBID, "dbName": rec.Name,
		"prefix": t.Prefix, "created": t.Created, "token": raw,
	})
}

func (s *Server) deleteAPIToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.store.DeleteAPIToken(id); err != nil {
		writeErr(w, http.StatusInternalServerError, "Cannot revoke API token.")
		return
	}
	s.log(r, "warn", "api token revoked", map[string]any{"id": id}, "", "")
	w.WriteHeader(http.StatusNoContent)
}
