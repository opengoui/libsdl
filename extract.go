package libsdl

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// extractor writes one gzip blob to disk. It is a struct so that tests can
// drive it with fake assets and cache directories.
type extractor struct {
	blob    []byte
	name    string
	size    int64
	sum     string // hex SHA-256 of the decompressed content
	version string
	cache   func() (string, error) // user cache dir
}

// ensure returns the path of a verified copy of the library.
func (e *extractor) ensure() (string, error) {
	if e.blob == nil {
		return "", errUnsupported()
	}
	base, err := e.cache()
	if err == nil {
		var p string
		if p, err = e.install(filepath.Join(base, dirName(e.version, e.sum))); err == nil {
			return p, nil
		}
		if errors.Is(err, errBadAsset) {
			return "", err
		}
	}
	// Deterministic per-user fallback so repeated runs reuse one copy
	// instead of leaking a new directory each time.
	tmp := filepath.Join(os.TempDir(), "libsdl-"+userTag()+"-"+e.version+"-"+e.sum[:16])
	p, ierr := e.install(tmp)
	if ierr != nil {
		return "", fmt.Errorf("libsdl: cache unusable (%v): %w", err, ierr)
	}
	return p, nil
}

// trustDir makes sure dir is a real directory owned by the current user and
// not writable by others, tightening the mode when it is ours.
func trustDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("libsdl: %s is not a directory", dir)
	}
	if ownedByUs(info) {
		return nil
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	if info, err = os.Lstat(dir); err != nil || !ownedByUs(info) {
		return fmt.Errorf("libsdl: %s is not private to the current user", dir)
	}
	return nil
}

// install makes dir/name a valid copy of the library and returns its path.
func (e *extractor) install(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := trustDir(dir); err != nil {
		return "", err
	}
	final := filepath.Join(dir, e.name)
	if e.valid(final) {
		return final, nil
	}
	removeStale(dir)

	f, err := os.CreateTemp(dir, e.name+".*.tmp")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	if err := e.write(f); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Chmod(0o755); err != nil && !isWindowsChmodNoop(err) {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		// Another process may have won the race; on Windows it also
		// fails when the destination is loaded. A valid file is fine.
		if e.valid(final) {
			return final, nil
		}
		return "", err
	}
	return final, nil
}

// write decompresses the blob into f and checks size and hash.
func (e *extractor) write(f *os.File) error {
	zr, err := gzip.NewReader(bytes.NewReader(e.blob))
	if err != nil {
		return fmt.Errorf("libsdl: corrupt embedded blob: %w", err)
	}
	defer zr.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(zr, e.size+1))
	if err != nil {
		return fmt.Errorf("libsdl: decompress: %w", err)
	}
	if n != e.size || hex.EncodeToString(h.Sum(nil)) != e.sum {
		return errBadAsset
	}
	return f.Sync()
}

// valid reports whether path holds exactly the expected content. The size
// check short-circuits most corrupt or truncated files without hashing.
func (e *extractor) valid(path string) bool {
	if li, err := os.Lstat(path); err != nil || !li.Mode().IsRegular() || !ownedByUs(li) {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() != e.size {
		return false
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == e.sum
}

// removeStale deletes temporary files left behind by crashed processes.
func removeStale(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, ent := range ents {
		if !strings.HasSuffix(ent.Name(), ".tmp") {
			continue
		}
		if info, err := ent.Info(); err == nil && time.Since(info.ModTime()) > time.Hour {
			os.Remove(filepath.Join(dir, ent.Name()))
		}
	}
}

// isWindowsChmodNoop tolerates platforms where Chmod is unsupported.
func isWindowsChmodNoop(err error) bool { return errors.Is(err, errors.ErrUnsupported) }
