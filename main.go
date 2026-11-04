// Command sakudata is a multi-database SQLite backend with an admin API.
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"sakudata/internal/api"
	"sakudata/internal/backup"
	"sakudata/internal/store"
	"sakudata/internal/tenant"
	"sakudata/internal/ui"
)

const version = "0.1.0"

func main() {
	addr := flag.String("addr", "127.0.0.1:8090", "listen address")
	dataDir := flag.String("data", "./data", "data directory")
	restore := flag.String("restore", "", "restore a .tar.gz backup into -data and exit")
	flag.Parse()

	if err := os.MkdirAll(filepath.Join(*dataDir, "dbs"), 0o755); err != nil {
		log.Fatalf("data dir: %v", err)
	}
	if *restore != "" {
		if err := stageRestore(*dataDir, *restore); err != nil {
			log.Fatalf("restore: %v", err)
		}
		if err := applyPendingRestore(*dataDir); err != nil {
			log.Fatalf("restore: %v", err)
		}
		log.Printf("restore applied into %s", *dataDir)
		return
	}
	if err := applyPendingRestore(*dataDir); err != nil {
		log.Fatalf("pending restore: %v", err)
	}
	st, err := store.Open(filepath.Join(*dataDir, "system.db"))
	if err != nil {
		log.Fatalf("open system db: %v", err)
	}
	defer st.Close()

	secret, err := loadOrCreateSecret(st)
	if err != nil {
		log.Fatalf("secret: %v", err)
	}

	srv := api.NewServer(st, tenant.NewManager(*dataDir), secret, *dataDir)
	mux := srv.Routes()
	mux.Handle("/_/", http.StripPrefix("/_", ui.Handler()))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/_/", http.StatusFound)
	})

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Wrap(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv.StartBackupLoop(ctx)
	go func() {
		log.Printf("sakudata %s listening on %s (data=%s)", version, *addr, *dataDir)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("shutting down...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

// stageRestore extracts an archive into data/restore-pending and marks it ready.
func stageRestore(dataDir, archive string) error {
	pending := filepath.Join(dataDir, "restore-pending")
	if err := os.RemoveAll(pending); err != nil {
		return err
	}
	if err := backup.Extract(archive, pending); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(pending, "system.db")); err != nil {
		return fmt.Errorf("archive does not contain system.db")
	}
	return os.WriteFile(filepath.Join(pending, ".ready"), []byte(time.Now().UTC().Format(time.RFC3339)), 0o644)
}

// applyPendingRestore swaps a staged restore into the live data directory. The
// previous data is kept under restore-old-<timestamp> for rollback.
func applyPendingRestore(dataDir string) error {
	pending := filepath.Join(dataDir, "restore-pending")
	if _, err := os.Stat(filepath.Join(pending, ".ready")); err != nil {
		return nil
	}
	log.Printf("applying pending restore from %s", pending)
	oldDir := filepath.Join(dataDir, "restore-old-"+time.Now().UTC().Format("20060102-150405"))
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		return err
	}
	for _, name := range []string{"system.db", "system.db-wal", "system.db-shm", "dbs", "files"} {
		src := filepath.Join(dataDir, name)
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, filepath.Join(oldDir, name)); err != nil {
				return err
			}
		}
	}
	for _, name := range []string{"system.db", "dbs", "files"} {
		src := filepath.Join(pending, name)
		if _, err := os.Stat(src); err == nil {
			if err := os.Rename(src, filepath.Join(dataDir, name)); err != nil {
				return err
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "dbs"), 0o755); err != nil {
		return err
	}
	return os.RemoveAll(pending)
}

func loadOrCreateSecret(st *store.Store) ([]byte, error) {
	if s, err := st.GetSetting("jwt_secret"); err == nil && s != "" {
		return hex.DecodeString(s)
	} else if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := st.SetSetting("jwt_secret", hex.EncodeToString(b)); err != nil {
		return nil, err
	}
	return b, nil
}
