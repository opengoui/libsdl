package libsdl_test

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/opengoui/libsdl"
	"github.com/opengoui/libsdl/internal/manifest"
)

// The probe sources live under testdata so that "./..." ignores them: they
// need puresdl, which libsdl itself does not depend on.
//
//go:embed testdata/probe/*.go
var probeSrc embed.FS

// buildProbe builds the probe program for goos/goarch in a scratch module
// that points at this checkout and at the puresdl checkout in puresdlDir.
func buildProbe(t *testing.T, puresdlDir, goos, goarch string) string {
	t.Helper()
	self, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ents, _ := probeSrc.ReadDir("testdata/probe")
	for _, e := range ents {
		b, _ := probeSrc.ReadFile("testdata/probe/" + e.Name())
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// purego is required at the version puresdl uses.
	pmod, err := os.ReadFile(filepath.Join(puresdlDir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	purego := "v0.11.1"
	for _, l := range strings.Split(string(pmod), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == "github.com/ebitengine/purego" {
			purego = f[1]
		}
	}
	gomod := fmt.Sprintf(`module probe

go 1.27.2

require (
	github.com/opengoui/libsdl v0.0.0
	github.com/opengoui/puresdl v0.0.0
	github.com/ebitengine/purego %s
)

replace github.com/opengoui/libsdl => %s

replace github.com/opengoui/puresdl => %s
`, purego, filepath.ToSlash(self), filepath.ToSlash(puresdlDir))
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	if sum, err := os.ReadFile(filepath.Join(puresdlDir, "go.sum")); err == nil {
		os.WriteFile(filepath.Join(dir, "go.sum"), sum, 0o644)
	}
	exe := filepath.Join(dir, "probe")
	if goos == "windows" {
		exe += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", exe, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch, "GOFLAGS=-mod=mod", "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building probe for %s/%s: %v\n%s", goos, goarch, err, out)
	}
	return exe
}

func puresdlDir(t *testing.T) string {
	t.Helper()
	d := os.Getenv("LIBSDL_PURESDL_DIR")
	if d == "" {
		t.Skip("set LIBSDL_PURESDL_DIR to a puresdl checkout to run the integration test")
	}
	d, err := filepath.Abs(d)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestLoadWithPuresdl extracts the embedded library of the host platform
// into a fresh cache directory, loads it through puresdl, and compares
// SDL_GetVersion with libsdl.Version. It runs in a subprocess because
// puresdl loads a library when it is imported.
func TestLoadWithPuresdl(t *testing.T) {
	dir := puresdlDir(t)
	if libsdl.Info().Name == "" {
		t.Skip("no embedded library for this platform")
	}
	exe := buildProbe(t, dir, runtime.GOOS, runtime.GOARCH)

	env := append(os.Environ(),
		"HOME="+t.TempDir(), "XDG_CACHE_HOME="+t.TempDir(), "LocalAppData="+t.TempDir(),
		"SDL_VIDEODRIVER=dummy", "SDL_AUDIODRIVER=dummy")
	pathCmd := exec.Command(exe, "-path")
	pathCmd.Env = env
	out, err := pathCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe -path: %v\n%s", err, out)
	}
	path := string(out)
	if !strings.Contains(path, libsdl.Version) {
		t.Errorf("cache path %q does not contain version %s", path, libsdl.Version)
	}

	cmd := exec.Command(exe)
	cmd.Env = append(env, "SDL3_LIBRARY="+path)
	out, err = cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !strings.HasPrefix(string(out), "OK version="+libsdl.Version+" ") {
		t.Fatalf("unexpected probe output %q", out)
	}
}

// TestLoadLinuxInDocker runs the linux/amd64 and linux/arm64 libraries in
// containers (the foreign architecture through emulation). It needs
// LIBSDL_DOCKER=1 in addition to LIBSDL_PURESDL_DIR.
func TestLoadLinuxInDocker(t *testing.T) {
	dir := puresdlDir(t)
	if os.Getenv("LIBSDL_DOCKER") != "1" {
		t.Skip("set LIBSDL_DOCKER=1 to run the linux libraries in docker")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not found")
	}
	m, err := manifest.Load(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			exe := buildProbe(t, dir, "linux", arch)
			script := `export SDL3_LIBRARY=$(/p/probe -path) && /p/probe`
			cmd := exec.Command("docker", "run", "--rm", "--platform", "linux/"+arch,
				"-v", filepath.Dir(exe)+":/p:ro", "-e", "HOME=/tmp", "-e", "SDL_VIDEODRIVER=dummy",
				"-e", "SDL_AUDIODRIVER=dummy", "debian:stable-slim", "sh", "-c", script)
			out, err := cmd.CombinedOutput()
			t.Logf("%s", out)
			if err != nil {
				t.Fatalf("docker run: %v", err)
			}
			if !strings.HasPrefix(string(out), "OK version="+m.Version+" ") {
				t.Fatalf("unexpected output %q", out)
			}
		})
	}
}

// TestLoadDarwinAmd64UnderRosetta runs the darwin/amd64 library through
// Rosetta on an Apple Silicon host. It needs LIBSDL_ROSETTA=1.
func TestLoadDarwinAmd64UnderRosetta(t *testing.T) {
	dir := puresdlDir(t)
	if os.Getenv("LIBSDL_ROSETTA") != "1" || runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("set LIBSDL_ROSETTA=1 on an Apple Silicon Mac")
	}
	exe := buildProbe(t, dir, "darwin", "amd64")
	env := append(os.Environ(), "HOME="+t.TempDir(), "SDL_VIDEODRIVER=dummy", "SDL_AUDIODRIVER=dummy")
	pathCmd := exec.Command("arch", "-x86_64", exe, "-path")
	pathCmd.Env = env
	out, err := pathCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probe -path: %v\n%s", err, out)
	}
	cmd := exec.Command("arch", "-x86_64", exe)
	cmd.Env = append(env, "SDL3_LIBRARY="+string(out))
	out, err = cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil || !strings.HasPrefix(string(out), "OK version="+libsdl.Version+" ") {
		t.Fatalf("probe: %v, output %q", err, out)
	}
}
