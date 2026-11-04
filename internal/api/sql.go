package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"sakudata/internal/tenant"
)

const sqlRowLimit = 1000

func sqlIsQuery(q string) bool {
	u := strings.ToUpper(strings.TrimSpace(q))
	for _, p := range []string{"SELECT", "WITH", "PRAGMA", "EXPLAIN", "VALUES"} {
		if u == p || strings.HasPrefix(u, p+" ") || strings.HasPrefix(u, p+"\n") || strings.HasPrefix(u, p+"(") {
			return true
		}
	}
	return false
}

func sqlConvert(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	default:
		return v
	}
}

func truncateSQL(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// runSQL executes one statement against a tenant database. Restricted to
// superusers; SELECT-like statements return columns and rows (max 1000).
func (s *Server) runSQL(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("db")
	rec, err := s.store.GetDatabaseByID(id)
	if err != nil && tenant.ValidName(id) {
		rec, err = s.store.GetDatabaseByName(id)
	}
	if err != nil {
		writeErr(w, http.StatusNotFound, "Not found.")
		return
	}
	db, err := s.tenants.Open(rec.Name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Cannot open database.")
		return
	}
	var in struct {
		Query  string `json:"query"`
		Params []any  `json:"params"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Query) == "" {
		writeErr(w, http.StatusBadRequest, "Query is required.")
		return
	}
	if len(in.Query) > 100_000 {
		writeErr(w, http.StatusBadRequest, "Query is too long.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	start := time.Now()

	if !sqlIsQuery(in.Query) {
		res, err := db.ExecContext(ctx, in.Query, in.Params...)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		n, _ := res.RowsAffected()
		s.log(r, "info", "sql exec: "+truncateSQL(in.Query, 160), map[string]any{"rows": n}, rec.Name, "")
		writeJSON(w, http.StatusOK, map[string]any{
			"type": "exec", "rowsAffected": n,
			"elapsedMs": time.Since(start).Milliseconds(),
		})
		return
	}

	rows, err := db.QueryContext(ctx, in.Query, in.Params...)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	out := make([][]any, 0, 32)
	truncated := false
	for rows.Next() {
		if len(out) >= sqlRowLimit {
			truncated = true
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		for i := range vals {
			vals[i] = sqlConvert(vals[i])
		}
		out = append(out, vals)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log(r, "info", "sql query: "+truncateSQL(in.Query, 160), map[string]any{"rows": len(out)}, rec.Name, "")
	writeJSON(w, http.StatusOK, map[string]any{
		"type": "query", "columns": cols, "rows": out,
		"rowCount": len(out), "truncated": truncated,
		"elapsedMs": time.Since(start).Milliseconds(),
	})
}
