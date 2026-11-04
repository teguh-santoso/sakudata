package tenant

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// MaxUploadSize caps a single uploaded file (10 MB, matches the Caddy default).
const MaxUploadSize = 10 << 20

// FileStore persists record attachments. Implementations: local disk, S3.
type FileStore interface {
	Put(db, col, id, field, name string, r io.Reader) (stored string, size int64, err error)
	Get(db, col, id, field, stored string) (io.ReadCloser, int64, error)
	Delete(db, col, id, field, stored string) error
	DropDB(db string) error
	DropPrefix(db, col string) error
}

var fileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// SanitizeFileName rejects anything that is not a plain file name:
// no directories, no separators, no traversal tricks.
func SanitizeFileName(name string) (string, error) {
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, "\\") {
		return "", fmt.Errorf("invalid file name %q", name)
	}
	if !fileNameRe.MatchString(name) {
		return "", fmt.Errorf("invalid file name %q", name)
	}
	return name, nil
}

// StoredName prefixes a random id so uploads never collide.
func StoredName(name string) string { return newID() + "_" + name }

// LocalStore keeps files under dir/files/<db>/<col>/<id>/.
type LocalStore struct{ dir string }

func NewLocalStore(dir string) *LocalStore { return &LocalStore{dir: dir} }

func (s *LocalStore) objectPath(db, col, id, stored string) (string, error) {
	for _, p := range []string{db, col, id, stored} {
		if p == "" || strings.ContainsAny(p, "/\\") {
			return "", fmt.Errorf("invalid path segment")
		}
	}
	if _, err := SanitizeFileName(stored); err != nil {
		return "", err
	}
	return filepath.Join(s.dir, "files", db, col, id, stored), nil
}

func (s *LocalStore) Put(db, col, id, field, name string, r io.Reader) (string, int64, error) {
	safe, err := SanitizeFileName(name)
	if err != nil {
		return "", 0, err
	}
	stored := StoredName(safe)
	p, err := s.objectPath(db, col, id, stored)
	if err != nil {
		return "", 0, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", 0, err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	n, err := io.CopyN(f, r, MaxUploadSize+1)
	if err != nil && err != io.EOF {
		os.Remove(p)
		return "", 0, err
	}
	if n > MaxUploadSize {
		os.Remove(p)
		return "", 0, fmt.Errorf("file exceeds %d bytes", MaxUploadSize)
	}
	return stored, n, nil
}

func (s *LocalStore) Get(db, col, id, field, stored string) (io.ReadCloser, int64, error) {
	p, err := s.objectPath(db, col, id, stored)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, fi.Size(), nil
}

func (s *LocalStore) Delete(db, col, id, field, stored string) error {
	if stored == "" {
		return nil
	}
	p, err := s.objectPath(db, col, id, stored)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// DropDB removes every file stored for a database.
func (s *LocalStore) DropDB(db string) error {
	if db == "" || strings.ContainsAny(db, "/\\") {
		return fmt.Errorf("invalid database name")
	}
	if err := os.RemoveAll(filepath.Join(s.dir, "files", db)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// DropPrefix removes every file stored for one collection.
func (s *LocalStore) DropPrefix(db, col string) error {
	if !ValidName(db) || !ValidName(col) {
		return fmt.Errorf("invalid database or collection name")
	}
	if err := os.RemoveAll(filepath.Join(s.dir, "files", db, col)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
