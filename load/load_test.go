package load

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/opengoui/libsdl"
	"github.com/opengoui/puresdl/sdl"
)

func skipUnsupported(t *testing.T) {
	t.Helper()
	if libsdl.Info().Name == "" {
		t.Skip("no embedded library for this platform")
	}
}

// checkLoaded verifies that path is the bound library and that it is the
// pinned SDL release.
func checkLoaded(t *testing.T, path string) {
	t.Helper()
	if got := sdl.LoadedPath(); got != path {
		t.Fatalf("LoadedPath = %q, want %q", got, path)
	}
	if err := sdl.LoadError(); err != nil {
		t.Fatal(err)
	}
	v := sdl.GetVersion()
	if got := formatVersion(v); got != libsdl.Version {
		t.Fatalf("SDL_GetVersion = %s, libsdl.Version = %s", got, libsdl.Version)
	}
}

func formatVersion(v int32) string {
	return fmt.Sprintf("%d.%d.%d", v/1000000, v/1000%1000, v%1000)
}

func TestLoadCustomAndDefaultDirs(t *testing.T) {
	skipUnsupported(t)
	t.Setenv(libsdl.DirEnv, "")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)

	custom := filepath.Join(t.TempDir(), "custom")
	p, err := Load(custom)
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(custom, libsdl.Info().Name) {
		t.Fatalf("path = %s", p)
	}
	checkLoaded(t, p)

	// No directory: the temp dir, and puresdl switches to that copy.
	d, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(filepath.Dir(d)) != tmp {
		t.Fatalf("default path %s is not below %s", d, tmp)
	}
	checkLoaded(t, d)

	// Same directory again: nothing changes.
	if again, err := Load(); err != nil || again != d {
		t.Fatalf("second Load() = %q, %v", again, err)
	}
	checkLoaded(t, d)
}

func TestLoadErrorsKeepPreviousLibrary(t *testing.T) {
	skipUnsupported(t)
	first, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load("a", "b"); err == nil {
		t.Error("two directories accepted")
	}
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if p, err := Load(filepath.Join(blocker, "sub")); err == nil || p != "" {
		t.Errorf("Load into an unusable dir = %q, %v", p, err)
	}
	checkLoaded(t, first)
}

func TestLoadUnsupportedPlatform(t *testing.T) {
	if libsdl.Info().Name != "" {
		t.Skip("platform is supported")
	}
	if _, err := Load(t.TempDir()); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
}
