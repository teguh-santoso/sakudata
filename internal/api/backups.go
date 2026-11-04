package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"sakudata/internal/backup"
	"sakudata/internal/store"
	"sakudata/internal/tenant"
)

type backupItem struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
	Source   string `json:"source"` // local | s3
}

func (s *Server) backupDir() string { return filepath.Join(s.dataDir, "backups") }

// s3BackupStore returns a client and object prefix when S3 is enabled.
func (s *Server) s3BackupStore() (*tenant.S3Store, string, bool) {
	cfg := s.s3Config()
	if !cfg.Enabled {
		return nil, "", false
	}
	st, err := tenant.NewS3Store(context.Background(), cfg)
	if err != nil {
		return nil, "", false
	}
	prefix := "backups/"
	if p := strings.Trim(cfg.Prefix, "/"); p != "" {
		prefix = p + "/backups/"
	}
	return st, prefix, true
}

func (s *Server) settingInt(key string, def int) int {
	v, err := s.store.GetSetting(key)
	if err != nil || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// buildBackup snapshots the system DB, every tenant DB and the files directory.
// SQLite files are copied with VACUUM INTO for consistency.
func (s *Server) buildBackup() (backupItem, error) {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()

	if err := os.MkdirAll(s.backupDir(), 0o755); err != nil {
		return backupItem{}, err
	}
	staging, err := os.MkdirTemp(s.dataDir, ".backup-staging-")
	if err != nil {
		return backupItem{}, err
	}
	defer os.RemoveAll(staging)
	if err := os.MkdirAll(filepath.Join(staging, "dbs"), 0o755); err != nil {
		return backupItem{}, err
	}
	if err := s.store.BackupTo(filepath.Join(staging, "system.db")); err != nil {
		return backupItem{}, fmt.Errorf("system db: %w", err)
	}
	dbs, err := s.store.ListDatabases()
	if err != nil {
		return backupItem{}, err
	}
	for _, d := range dbs {
		if err := s.tenants.BackupTo(d.Name, filepath.Join(staging, "dbs", d.Name+".db")); err != nil {
			return backupItem{}, fmt.Errorf("database %s: %w", d.Name, err)
		}
	}
	if err := backup.CopyDir(filepath.Join(s.dataDir, "files"), filepath.Join(staging, "files")); err != nil {
		return backupItem{}, fmt.Errorf("files: %w", err)
	}

	stamp := time.Now().UTC().Format("20060102-150405")
	name := fmt.Sprintf("backup-%s-%s.tar.gz", stamp, store.NewID()[:6])
	dst := filepath.Join(s.backupDir(), name)
	if err := backup.TarDir(staging, dst); err != nil {
		return backupItem{}, err
	}
	info, err := os.Stat(dst)
	if err != nil {
		return backupItem{}, err
	}
	item := backupItem{Name: name, Size: info.Size(), Modified: info.ModTime().UTC().Format(time.RFC3339), Source: "local"}

	if st, prefix, ok := s.s3BackupStore(); ok {
		f, err := os.Open(dst)
		if err == nil {
			if err := st.PutObject(prefix+name, f, info.Size(), "application/gzip"); err == nil {
				item.Source = "local+s3"
			}
			f.Close()
		}
	}
	if _, err := os.Stat(dst); err == nil {
		s.pruneBackups()
	}
	_ = s.store.SetSetting("backup_last", time.Now().UTC().Format(time.RFC3339))
	s.store.AddLog(store.LogEntry{
		Level: "info", Message: "backup created", Data: `{"name":"` + name + `"}`,
	})
	return item, nil
}

func (s *Server) pruneBackups() {
	keep := s.settingInt("backup_keep", 7)
	if keep < 1 {
		keep = 7
	}
	files, err := backup.List(s.backupDir())
	if err == nil && len(files) > keep {
		for _, f := range files[keep:] {
			_ = os.Remove(filepath.Join(s.backupDir(), f.Name))
		}
	}
	if st, prefix, ok := s.s3BackupStore(); ok {
		objs, err := st.ListObjects(prefix)
		if err == nil && len(objs) > keep {
			sort.Slice(objs, func(i, j int) bool { return objs[i].Key > objs[j].Key })
			for _, o := range objs[keep:] {
				_ = st.DeleteObject(o.Key)
			}
		}
	}
}

func (s *Server) createBackupHandler(w http.ResponseWriter, r *http.Request) {
	item, err := s.buildBackup()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Backup failed: "+err.Error())
		return
	}
	s.log(r, "info", "backup created", map[string]any{"name": item.Name}, "", "")
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) listBackups(w http.ResponseWriter, r *http.Request) {
	items := []backupItem{}
	local, err := backup.List(s.backupDir())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Cannot list backups.")
		return
	}
	seen := map[string]bool{}
	for _, f := range local {
		items = append(items, backupItem{Name: f.Name, Size: f.Size, Modified: f.ModTime, Source: "local"})
		seen[f.Name] = true
	}
	s3Enabled := false
	if st, prefix, ok := s.s3BackupStore(); ok {
		s3Enabled = true
		if objs, err := st.ListObjects(prefix); err == nil {
			for _, o := range objs {
				name := strings.TrimPrefix(o.Key, prefix)
				if name == "" || seen[name] {
					continue
				}
				items = append(items, backupItem{Name: name, Size: o.Size, Modified: o.Modified, Source: "s3"})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Modified > items[j].Modified })
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "s3": s3Enabled})
}

func safeBackupName(name string) (string, bool) {
	if name == "" || name != path.Base(name) || strings.ContainsAny(name, "/\\") || !strings.HasSuffix(name, ".tar.gz") {
		return "", false
	}
	return name, true
}

func (s *Server) downloadBackup(w http.ResponseWriter, r *http.Request) {
	name, ok := safeBackupName(r.PathValue("name"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "Invalid backup name.")
		return
	}
	localPath := filepath.Join(s.backupDir(), name)
	if f, err := os.Open(localPath); err == nil {
		defer f.Close()
		if info, err := f.Stat(); err == nil {
			w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		_, _ = io.Copy(w, f)
		return
	}
	st, prefix, ok := s.s3BackupStore()
	if ok {
		body, size, err := st.GetObject(prefix + name)
		if err == nil {
			defer body.Close()
			w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
			w.Header().Set("Content-Type", "application/gzip")
			w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
			_, _ = io.Copy(w, body)
			return
		}
	}
	writeErr(w, http.StatusNotFound, "Backup not found.")
}

func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) {
	name, ok := safeBackupName(r.PathValue("name"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "Invalid backup name.")
		return
	}
	_ = os.Remove(filepath.Join(s.backupDir(), name))
	if st, prefix, ok := s.s3BackupStore(); ok {
		_ = st.DeleteObject(prefix + name)
	}
	s.log(r, "warn", "backup deleted", map[string]any{"name": name}, "", "")
	w.WriteHeader(http.StatusNoContent)
}

// restoreBackup stages an archive into data/restore-pending. main applies it on
// the next start, which keeps the live process from swapping files underneath
// open SQLite handles.
func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	name, ok := safeBackupName(r.PathValue("name"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "Invalid backup name.")
		return
	}
	archive := filepath.Join(s.backupDir(), name)
	tempDownload := ""
	if _, err := os.Stat(archive); err != nil {
		st, prefix, enabled := s.s3BackupStore()
		if !enabled {
			writeErr(w, http.StatusNotFound, "Backup not found.")
			return
		}
		body, _, err := st.GetObject(prefix + name)
		if err != nil {
			writeErr(w, http.StatusNotFound, "Backup not found in storage.")
			return
		}
		f, err := os.CreateTemp(s.dataDir, ".restore-download-*.tar.gz")
		if err != nil {
			body.Close()
			writeErr(w, http.StatusInternalServerError, "Cannot stage restore.")
			return
		}
		if _, err := io.Copy(f, body); err != nil {
			f.Close()
			body.Close()
			os.Remove(f.Name())
			writeErr(w, http.StatusInternalServerError, "Cannot download backup.")
			return
		}
		f.Close()
		body.Close()
		archive = f.Name()
		tempDownload = f.Name()
	}
	if tempDownload != "" {
		defer os.Remove(tempDownload)
	}

	staging := filepath.Join(s.dataDir, "restore-pending")
	_ = os.RemoveAll(staging)
	if err := backup.Extract(archive, staging); err != nil {
		_ = os.RemoveAll(staging)
		writeErr(w, http.StatusBadRequest, "Invalid archive: "+err.Error())
		return
	}
	if _, err := os.Stat(filepath.Join(staging, "system.db")); err != nil {
		_ = os.RemoveAll(staging)
		writeErr(w, http.StatusBadRequest, "Archive does not contain system.db.")
		return
	}
	if err := os.WriteFile(filepath.Join(staging, ".ready"), []byte(time.Now().UTC().Format(time.RFC3339)), 0o644); err != nil {
		writeErr(w, http.StatusInternalServerError, "Cannot stage restore.")
		return
	}
	s.log(r, "warn", "restore staged", map[string]any{"name": name}, "", "")
	writeJSON(w, http.StatusAccepted, map[string]any{
		"message":         "Restore staged. Restart the service to apply it.",
		"restartRequired": true,
		"staged":          staging,
	})
}

// StartBackupLoop runs the auto-backup schedule in the background.
func (s *Server) StartBackupLoop(ctx context.Context) {
	go func() {
		timer := time.NewTimer(time.Minute)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		s.maybeScheduledBackup()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.maybeScheduledBackup()
			}
		}
	}()
}

func (s *Server) maybeScheduledBackup() {
	if v, _ := s.store.GetSetting("backup_enabled"); v != "1" {
		return
	}
	hours := s.settingInt("backup_interval_hours", 24)
	if hours < 1 {
		hours = 24
	}
	if last, _ := s.store.GetSetting("backup_last"); last != "" {
		if t, err := time.Parse(time.RFC3339, last); err == nil && time.Since(t) < time.Duration(hours)*time.Hour {
			return
		}
	}
	if _, err := s.buildBackup(); err != nil {
		s.store.AddLog(store.LogEntry{Level: "error", Message: "scheduled backup failed: " + err.Error(), Data: "{}"})
	}
}
