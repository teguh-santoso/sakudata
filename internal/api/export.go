package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"sakudata/internal/tenant"
)

// exportRecords streams every record visible under the collection's list rule.
func (s *Server) exportRecords(w http.ResponseWriter, r *http.Request) {
	db, c := fromCtx(r)
	q := r.URL.Query()

	params := map[string]string{}
	for k, v := range q {
		if len(v) > 0 && k != "format" {
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

	var all []map[string]any
	for page := 1; ; page++ {
		items, total, err := s.tenants.ListRecords(db, c, tenant.ListOptions{
			Page: page, PerPage: 200, Sort: q.Get("sort"),
			FilterExpr: strings.Join(clauses, " && "), Params: params, Auth: authCtx,
		})
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		all = append(all, items...)
		if len(items) == 0 || len(all) >= total || page > 10000 {
			break
		}
	}

	format := strings.ToLower(q.Get("format"))
	if format == "csv" {
		cols := []string{"id"}
		for _, f := range c.Fields {
			if f.Name != "password" {
				cols = append(cols, f.Name)
			}
		}
		cols = append(cols, "created", "updated")
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.csv"`, c.Name))
		cw := csv.NewWriter(w)
		_ = cw.Write(cols)
		for _, rec := range all {
			row := make([]string, len(cols))
			for i, col := range cols {
				v := rec[col]
				if v == nil {
					row[i] = ""
					continue
				}
				switch t := v.(type) {
				case string:
					row[i] = t
				case float64:
					row[i] = strconv.FormatFloat(t, 'f', -1, 64)
				case bool:
					row[i] = strconv.FormatBool(t)
				default:
					b, _ := json.Marshal(t)
					row[i] = string(b)
				}
			}
			_ = cw.Write(row)
		}
		cw.Flush()
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.json"`, c.Name))
	_ = json.NewEncoder(w).Encode(all)
}

// importRecords creates records from a JSON array (max 5000 rows).
func (s *Server) importRecords(w http.ResponseWriter, r *http.Request) {
	db, c := fromCtx(r)
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20)
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	var rows []map[string]any
	if err := dec.Decode(&rows); err != nil {
		writeErr(w, http.StatusBadRequest, "Expected a JSON array of records.")
		return
	}
	if len(rows) == 0 {
		writeErr(w, http.StatusBadRequest, "No records to import.")
		return
	}
	if len(rows) > 5000 {
		writeErr(w, http.StatusBadRequest, "Too many records (max 5000 per import).")
		return
	}
	created, failed := 0, 0
	var errs []string
	for i, row := range rows {
		rec, err := s.tenants.CreateRecord(db, c, row)
		if err != nil {
			failed++
			if len(errs) < 20 {
				errs = append(errs, fmt.Sprintf("row %d: %v", i+1, err))
			}
			continue
		}
		if ok, _ := s.authorizeRecord(r, rec); !ok {
			_ = s.tenants.DeleteRecord(db, c, rec["id"].(string))
			failed++
			if len(errs) < 20 {
				errs = append(errs, fmt.Sprintf("row %d: not allowed by rule", i+1))
			}
			continue
		}
		created++
	}
	s.log(r, "info", "records imported", map[string]any{"created": created, "failed": failed}, dbNameFromCtx(r), c.Name)
	writeJSON(w, http.StatusOK, map[string]any{"created": created, "failed": failed, "errors": errs})
}
