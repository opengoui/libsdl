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
	"runtime"
	"strings"
	"time"
)

// extractor writes one gzip blob to disk. It is a struct so that tests can
// drive it with fake assets.
type extractor struct {
	blob    []byte
	name    string
	size    int64
	sum     string // hex SHA-256 of the decompressed content
	version string
}

// defaultDir is the deterministic per-user directory below the OS temp dir.
func (e *extractor) defaultDir() string {
	return filepath.Join(os.TempDir(), "libsdl-"+userTag()+"-"+e.version+"-"+e.sum[:16])
}

// ensure returns the path of a verified copy of the library. An empty dir
// selects the private default directory below os.TempDir(), where the
// ownership and permission checks apply. Any other dir is a user-chosen
// location: it is created if needed, its ownership is not examined, and a
// failure to use it is returned, never worked around.
func (e *extractor) ensure(dir string) (string, error) {
	if e.blob == nil {
		return "", errUnsupported()
	}
	if dir == "" {
		return e.install(e.defaultDir(), true)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("libsdl: custom directory %q: %w", dir, err)
	}
	return e.install(abs, false)
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
// strict selects the private-directory rules of the default location; a
// custom directory only has to be writable, and its target a regular file.
func (e *extractor) install(dir string, strict bool) (string, error) {
	mode := os.FileMode(0o755)
	if strict {
		mode = 0o700
	}
	if err := os.MkdirAll(dir, mode); err != nil {
		return "", fmt.Errorf("libsdl: cannot create directory: %w", err)
	}
	if strict {
		if err := trustDir(dir); err != nil {
			return "", err
		}
	} else if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("libsdl: %s is not a directory", dir)
	}
	final := filepath.Join(dir, e.name)
	if li, err := os.Lstat(final); err == nil && li.IsDir() {
		return "", fmt.Errorf("libsdl: %s is a directory, expected the library file", final)
	}
	if e.valid(final, strict) {
		return final, nil
	}
	e.removeStale(dir)

	f, err := os.CreateTemp(dir, e.name+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("libsdl: directory is not writable: %w", err)
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
	// rename replaces a symlink at final instead of writing through it.
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		// Another process may have won the race; on Windows it also
		// fails when the destination is loaded. A valid file is fine.
		if e.valid(final, strict) {
			return final, nil
		}
		if runtime.GOOS == "windows" {
			return "", fmt.Errorf("libsdl: cannot replace %s, it differs from the embedded library and is probably loaded by a running process (close it or choose another directory): %w", final, err)
		}
		return "", fmt.Errorf("libsdl: cannot replace %s: %w", final, err)
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
// check short-circuits most corrupt or truncated files without hashing. The
// entry must be a regular file, not a symlink. When strict is set it must
// also be owned by the current user and not writable by others.
func (e *extractor) valid(path string, strict bool) bool {
	li, err := os.Lstat(path)
	if err != nil || !li.Mode().IsRegular() || (strict && !ownedByUs(li)) {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() != e.size || !os.SameFile(li, st) {
		return false
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == e.sum
}

// removeStale deletes this library's temporary files left behind by crashed
// processes. Other files in dir are never touched.
func (e *extractor) removeStale(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, ent := range ents {
		if !strings.HasPrefix(ent.Name(), e.name+".") || !strings.HasSuffix(ent.Name(), ".tmp") {
			continue
		}
		if info, err := ent.Info(); err == nil && time.Since(info.ModTime()) > time.Hour {
			os.Remove(filepath.Join(dir, ent.Name()))
		}
	}
}

// isWindowsChmodNoop tolerates platforms where Chmod is unsupported.
func isWindowsChmodNoop(err error) bool { return errors.Is(err, errors.ErrUnsupported) }
