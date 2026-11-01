package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// CheckPath rejects symlinks in every existing path component.
func CheckPath(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for p := abs; ; p = filepath.Dir(p) {
		st, err := os.Lstat(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && st.Mode()&os.ModeSymlink != 0 {
			// macOS exposes these root-owned system aliases by default (including
			// os.TempDir). They are not user-selected redirections between Bot roots.
			systemAlias := false
			if runtime.GOOS == "darwin" && (p == "/var" || p == "/tmp" || p == "/etc") {
				target, readErr := os.Readlink(p)
				systemAlias = readErr == nil && (target == "private"+p || target == "/private"+p)
			}
			if !systemAlias {
				return fmt.Errorf("symlink path is not allowed: %s", p)
			}
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	return nil
}

func EnsurePrivateDir(path string) error {
	if path == "" {
		return errors.New("directory is empty")
	}
	if err := CheckPath(path); err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	if err := CheckPath(path); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func SyncDir(path string) error {
	return syncDir(path)
}

func WriteJSONAtomic(path string, v any, perm os.FileMode) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(path, append(b, '\n'), perm)
}

func WriteAtomic(path string, b []byte, perm os.FileMode) error {
	if err := CheckPath(path); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := EnsurePrivateDir(dir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := CheckPath(path); err != nil {
		return err
	}
	if err := renameAtomic(tmp.Name(), path); err != nil {
		return err
	}
	return SyncDir(dir)
}

// FileLock is an advisory, cross-process, nonblocking exclusive file lock.
// The lock file is retained on close: unlinking it would permit split locks.
type FileLock struct{ file *os.File }

func Lock(path string) (*FileLock, error) {
	if err := CheckPath(path); err != nil {
		return nil, err
	}
	if err := EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := lockFile(path)
	if err != nil {
		return nil, fmt.Errorf("directory is in use (%s): %w", path, err)
	}
	return &FileLock{file: f}, nil
}

func (l *FileLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	return f.Close()
}

func ReadFile(path string) ([]byte, error) {
	f, err := OpenRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// OpenRegular refuses symlinks/reparse points and non-regular files.
func OpenRegular(path string) (*os.File, error) {
	if err := CheckPath(path); err != nil {
		return nil, err
	}
	f, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !st.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("not a regular file: %s", path)
	}
	return f, nil
}
