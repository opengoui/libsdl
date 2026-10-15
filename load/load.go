// Package load releases the SDL3 library embedded in
// github.com/opengoui/libsdl to disk and loads it with puresdl.
//
// It lives in its own module so that libsdl itself stays free of
// dependencies. [Load] does not depend on package initialisation order: it
// switches puresdl to the extracted library even when puresdl already loaded
// a system copy while it was being imported.
//
//	path, err := load.Load("/opt/myapp/lib") // or load.Load() for the temp dir
//	if err != nil { log.Fatal(err) }
package load

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/opengoui/libsdl"
	"github.com/opengoui/puresdl/sdl"
)

// Load extracts the embedded SDL3 library with [libsdl.Extract] and loads it
// with sdl.Load. At most one directory may be given; without one the
// LIBSDL_DIR environment variable and then the private default directory
// below os.TempDir() are used, exactly as for [libsdl.Path]. On Linux, if the
// default directory is unwritable or its mount is noexec, Load retries in
// XDG_RUNTIME_DIR and the user cache directory. See [libsdl.Extract] for how
// an existing file there is verified and replaced.
//
// With no directory override, an already loaded system SDL library is reused
// when its version exactly matches the embedded release. Otherwise Load binds
// the extracted library. It returns the path of the library. If sdl.LoadedPath
// does not report that path afterwards (some other library ended up bound),
// Load returns an error. Load must run before SDL is used: it may replace a
// library that puresdl loaded on import, and that switch is not safe
// concurrently with SDL calls. On Windows a DLL of the same file name that
// is already loaded in the process may be returned by the system loader
// instead of the new file; call Load before anything else loads SDL3.dll.
func Load(dir ...string) (path string, err error) {
	if len(dir) > 1 {
		return "", errors.New("load: at most one directory may be given")
	}
	d := ""
	if len(dir) == 1 {
		d = dir[0]
	}
	useDefault := d == "" && os.Getenv(libsdl.DirEnv) == ""
	if useDefault && loadedMatchingSystemLibrary() {
		return sdl.LoadedPath(), nil
	}
	path, err = libsdl.Extract(d)
	if !useDefault || runtime.GOOS != "linux" {
		if err != nil {
			return "", err
		}
		if err := bind(path); err != nil {
			return "", err
		}
		return path, nil
	}
	if err != nil {
		if !permissionError(err) {
			return "", err
		}
		return loadFallback(err, fallbackDirs(), libsdl.Extract, bind)
	}
	if err := bind(path); err != nil {
		if !permissionError(err) {
			return "", err
		}
		return loadFallback(err, fallbackDirs(), libsdl.Extract, bind)
	}
	return path, nil
}

func loadFallback(initialErr error, dirs []string, extract func(string) (string, error), bind func(string) error) (string, error) {
	failures := []error{initialErr}
	for _, dir := range dirs {
		candidate, err := extract(dir)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if err := bind(candidate); err == nil {
			return candidate, nil
		} else if !permissionError(err) {
			return "", err
		} else {
			failures = append(failures, err)
		}
	}
	return "", fmt.Errorf("load: SDL could not be extracted or mapped from the default or fallback directories; check permissions, executable mounts, and noexec settings: %w", errors.Join(failures...))
}

func bind(path string) error {
	if err := sdl.Load(path); err != nil {
		if runtime.GOOS == "linux" && permissionError(err) {
			return fmt.Errorf("load: loading %s failed; the filesystem may be mounted noexec, or set %s to an executable directory: %w", path, libsdl.DirEnv, err)
		}
		return fmt.Errorf("load: loading %s: %w", path, err)
	}
	if got := sdl.LoadedPath(); got != path {
		return fmt.Errorf("load: %s was extracted but puresdl reports %q as the loaded library", path, got)
	}
	return nil
}

func loadedMatchingSystemLibrary() bool {
	path := sdl.LoadedPath()
	if sdl.LoadError() != nil || path == "" || loadedEmbeddedLibrary(path) {
		return false
	}
	compiled := fmt.Sprintf("%d.%d.%d", sdl.VERSIONNUM_MAJOR(sdl.VERSION), sdl.VERSIONNUM_MINOR(sdl.VERSION), sdl.VERSIONNUM_MICRO(sdl.VERSION))
	return compiled == libsdl.Version && int(sdl.GetVersion()) == sdl.VERSION
}

func loadedEmbeddedLibrary(path string) bool {
	info := libsdl.Info()
	if info.Name == "" || info.Size <= 0 || info.SHA256 == "" {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() != info.Size {
		return false
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == info.SHA256
}

func permissionError(err error) bool {
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "permission denied") || strings.Contains(msg, "operation not permitted") ||
		strings.Contains(msg, "eacces") || strings.Contains(msg, "eperm")
}

func fallbackDirs() []string {
	info := libsdl.Info()
	if len(info.SHA256) < 16 {
		return nil
	}
	leaf := fmt.Sprintf("libsdl-%s-%s", info.Version, info.SHA256[:16])
	var dirs []string
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		dirs = append(dirs, filepath.Join(runtimeDir, leaf))
	}
	if cacheDir, err := os.UserCacheDir(); err == nil && cacheDir != "" {
		dirs = append(dirs, filepath.Join(cacheDir, "libsdl", leaf))
	}
	seen := make(map[string]bool, len(dirs))
	out := dirs[:0]
	for _, dir := range dirs {
		abs, err := filepath.Abs(dir)
		if err != nil || seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	return out
}
