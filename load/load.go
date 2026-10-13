// Package load releases the SDL3 library embedded in
// github.com/opengoui/libsdl to disk and loads it with puresdl.
//
// It lives in its own module so that libsdl itself stays free of
// dependencies. Unlike importing github.com/opengoui/libsdl/auto, [Load] does
// not depend on package initialisation order: it switches puresdl to the
// extracted library even when puresdl already loaded a system copy while it
// was being imported.
//
//	path, err := load.Load("/opt/myapp/lib") // or load.Load() for the temp dir
//	if err != nil { log.Fatal(err) }
package load

import (
	"errors"
	"fmt"

	"github.com/opengoui/libsdl"
	"github.com/opengoui/puresdl/sdl"
)

// Load extracts the embedded SDL3 library with [libsdl.Extract] and loads it
// with sdl.Load. At most one directory may be given; without one the
// LIBSDL_DIR environment variable and then the private default directory
// below os.TempDir() are used, exactly as for [libsdl.Path]. See
// [libsdl.Extract] for how an existing file in the directory is verified and
// replaced.
//
// It returns the path of the library. If sdl.LoadedPath does not report that
// path afterwards (some other library ended up bound), Load returns an
// error. Load must run before SDL is used: it may replace a library that
// puresdl loaded on import, and that switch is not safe concurrently with SDL
// calls. On Windows a DLL of the same file name that is already loaded in the
// process may be returned by the system loader instead of the new file; call
// Load before anything else loads SDL3.dll.
func Load(dir ...string) (path string, err error) {
	if len(dir) > 1 {
		return "", errors.New("load: at most one directory may be given")
	}
	d := ""
	if len(dir) == 1 {
		d = dir[0]
	}
	path, err = libsdl.Extract(d)
	if err != nil {
		return "", err
	}
	if err := sdl.Load(path); err != nil {
		return "", fmt.Errorf("load: loading %s: %w", path, err)
	}
	if got := sdl.LoadedPath(); got != path {
		return "", fmt.Errorf("load: %s was extracted but puresdl reports %q as the loaded library", path, got)
	}
	return path, nil
}
