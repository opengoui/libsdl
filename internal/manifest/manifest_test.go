package manifest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opengoui/libsdl/internal/fakelib"
	"github.com/opengoui/libsdl/internal/manifest"
)

// repoRoot is the module root, two levels above this package.
const repoRoot = "../.."

func TestRepositoryAssetsMatchManifest(t *testing.T) {
	if err := manifest.Check(repoRoot); err != nil {
		t.Fatal(err)
	}
}

func TestCheckHeaderAcceptsAndRejects(t *testing.T) {
	for _, tg := range manifest.Targets {
		if err := manifest.CheckHeader(tg.OS, tg.Arch, fakelib.Make(tg.OS, tg.Arch, "x")); err != nil {
			t.Errorf("%s/%s: %v", tg.OS, tg.Arch, err)
		}
		other := map[string]string{"amd64": "arm64", "arm64": "amd64"}[tg.Arch]
		if err := manifest.CheckHeader(tg.OS, tg.Arch, fakelib.Make(tg.OS, other, "x")); err == nil {
			t.Errorf("%s/%s: wrong architecture accepted", tg.OS, tg.Arch)
		}
	}
	if err := manifest.CheckHeader("linux", "amd64", fakelib.Make("darwin", "amd64", "")); err == nil {
		t.Error("Mach-O accepted as ELF")
	}
	if err := manifest.CheckHeader("windows", "amd64", []byte("MZ junk")); err == nil {
		t.Error("junk accepted as PE")
	}
}

func TestVersionConst(t *testing.T) {
	src := []byte("package libsdl\n\nconst Version = \"1.2.3\"\n")
	v, err := manifest.ReadVersion(src)
	if err != nil || v != "1.2.3" {
		t.Fatalf("ReadVersion = %q, %v", v, err)
	}
	out, err := manifest.SetVersion(src, "4.5.6")
	if err != nil || !strings.Contains(string(out), `const Version = "4.5.6"`) {
		t.Fatalf("SetVersion = %q, %v", out, err)
	}
	if _, err := manifest.SetVersion([]byte("package libsdl"), "1"); err == nil {
		t.Error("missing constant not reported")
	}
}

// copyModule copies the files Check reads into a temp module root.
func copyModule(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	files, _ := filepath.Glob(filepath.Join(repoRoot, "binary_*.go"))
	files = append(files, filepath.Join(repoRoot, "libsdl.go"))
	assets, _ := filepath.Glob(filepath.Join(repoRoot, "assets", "*"))
	files = append(files, assets...)
	for _, f := range files {
		rel, _ := filepath.Rel(repoRoot, f)
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dst, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, rel), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

func TestCheckDetectsTampering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, root string)
		want   string
	}{
		{"corrupt gzip asset", func(t *testing.T, root string) {
			p := filepath.Join(root, "assets/sdl_linux_arm64.gz")
			b, _ := os.ReadFile(p)
			b[len(b)/2] ^= 0xff
			os.WriteFile(p, b, 0o644)
		}, "linux/arm64"},
		{"missing asset", func(t *testing.T, root string) {
			os.Remove(filepath.Join(root, "assets/sdl_windows_amd64.gz"))
		}, "windows/amd64"},
		{"stale binary file", func(t *testing.T, root string) {
			p := filepath.Join(root, "binary_darwin_arm64.go")
			b, _ := os.ReadFile(p)
			os.WriteFile(p, []byte(strings.Replace(string(b), "libSize   = ", "libSize   = 1", 1)), 0o644)
		}, "darwin/arm64"},
		{"version const drift", func(t *testing.T, root string) {
			p := filepath.Join(root, "libsdl.go")
			b, _ := os.ReadFile(p)
			os.WriteFile(p, []byte(strings.Replace(string(b), "const Version = \"", "const Version = \"0.", 1)), 0o644)
		}, "libsdl.go Version"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := copyModule(t)
			if err := manifest.Check(root); err != nil {
				t.Fatalf("pristine copy fails: %v", err)
			}
			tc.mutate(t, root)
			err := manifest.Check(root)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Check = %v, want an error mentioning %q", err, tc.want)
			}
		})
	}
}
