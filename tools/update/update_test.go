package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opengoui/libsdl/internal/fakelib"
	"github.com/opengoui/libsdl/internal/manifest"
)

func TestNewerVersion(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"3.4.18", "3.4.17", true},
		{"3.4.9", "3.4.10", false},
		{"3.5.0", "3.4.99", true},
		{"3.4.18", "3.4.18", false},
		{"3.4", "3.4.0", false},
		{"4.0.0", "3.9.9", true},
	}
	for _, tc := range tests {
		if got := newerVersion(tc.a, tc.b); got != tc.want {
			t.Errorf("newerVersion(%s, %s) = %v", tc.a, tc.b, got)
		}
	}
}

func makeZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, _ := zw.Create(name)
		w.Write(data)
	}
	zw.Close()
	return buf.Bytes()
}

// fakeGitHub serves a release with Windows zips. The digest of the arm64 zip
// is wrong when badDigest is set.
func fakeGitHub(t *testing.T, tag string, badDigest bool) {
	t.Helper()
	ver := manifest.VersionOfTag(tag)
	zips := map[string][]byte{
		"SDL3-" + ver + "-win32-x64.zip": makeZip(t, map[string][]byte{
			"SDL3.dll": fakelib.Make("windows", "amd64", ver), "LICENSE.txt": []byte("zlib license\n")}),
		"SDL3-" + ver + "-win32-arm64.zip": makeZip(t, map[string][]byte{
			"SDL3.dll": fakelib.Make("windows", "arm64", ver), "LICENSE.txt": []byte("zlib license\n")}),
	}
	var rel release
	rel.Tag = tag
	for name, b := range zips {
		d := manifest.SHA256Hex(b)
		if badDigest && strings.Contains(name, "arm64") {
			d = strings.Repeat("0", 64)
		}
		rel.Assets = append(rel.Assets, asset{Name: name, Digest: "sha256:" + d})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/releases/tags/"+tag, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			http.Error(w, "no user agent", http.StatusForbidden)
			return
		}
		json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/api/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/dl/"+tag+"/", func(w http.ResponseWriter, r *http.Request) {
		w.Write(zips[filepath.Base(r.URL.Path)])
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	oa, od := apiBase, downloadBase
	apiBase, downloadBase = srv.URL+"/api", srv.URL+"/dl"
	t.Cleanup(func() { apiBase, downloadBase = oa, od })
}

func newRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	src := "package libsdl\n\nconst Version = \"0.0.1\"\n"
	if err := os.WriteFile(filepath.Join(root, "libsdl.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeLinuxOut(t *testing.T, tag string) string {
	t.Helper()
	dir := t.TempDir()
	for _, a := range []string{"amd64", "arm64"} {
		os.WriteFile(filepath.Join(dir, "SDL3_linux_"+a+".so"), fakelib.Make("linux", a, tag), 0o644)
	}
	rep := "SDL " + tag + "  source tarball sha256 " + strings.Repeat("ab", 32) + "\n"
	os.WriteFile(filepath.Join(dir, "build-report.txt"), []byte(rep), 0o644)
	return dir
}

func TestUpdateWindowsAndLinuxThenCheck(t *testing.T) {
	const tag = "release-9.8.7"
	fakeGitHub(t, tag, false)
	root := newRoot(t)
	linux := writeLinuxOut(t, tag)

	if err := run([]string{"-root", root, "-version", "9.8.7", "-os", "windows,linux", "-linux-dir", linux}); err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if m.Tag != tag || m.Version != "9.8.7" || len(m.Files) != 4 {
		t.Fatalf("manifest = %+v", m)
	}
	w := m.Find("windows", "arm64")
	if w == nil || w.Origin != manifest.OriginOfficial || !strings.HasSuffix(w.Source, "/SDL3-9.8.7-win32-arm64.zip") || len(w.SourceSHA256) != 64 {
		t.Fatalf("windows/arm64 entry = %+v", w)
	}
	l := m.Find("linux", "amd64")
	if l == nil || l.Origin != manifest.OriginZig || l.SourceSHA256 != strings.Repeat("ab", 32) {
		t.Fatalf("linux/amd64 entry = %+v", l)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "LICENSE-SDL")); string(b) != "zlib license\n" {
		t.Fatalf("LICENSE-SDL = %q", b)
	}
	if v, _ := os.ReadFile(filepath.Join(root, "libsdl.go")); !strings.Contains(string(v), `"9.8.7"`) {
		t.Fatalf("libsdl.go = %s", v)
	}
	// darwin is still missing, so the check must fail ...
	if err := run([]string{"-root", root, "-check"}); err == nil || !strings.Contains(err.Error(), "darwin") {
		t.Fatalf("check on partial update = %v", err)
	}
	// ... and pass once the remaining platforms are added.
	for _, tg := range targetsOf("darwin") {
		f, err := manifest.NewFile(root, tg, "9.8.7", fakelib.Make("darwin", tg.Arch, "9.8.7"))
		if err != nil {
			t.Fatal(err)
		}
		f.Origin, f.Source = manifest.OriginOfficial, "test"
		m.Set(f)
	}
	if err := m.Save(root); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteGenerated(root); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-root", root, "-check"}); err != nil {
		t.Fatal(err)
	}
}

func TestDigestMismatchIsRejected(t *testing.T) {
	fakeGitHub(t, "release-9.8.7", true)
	root := newRoot(t)
	err := run([]string{"-root", root, "-version", "release-9.8.7", "-os", "windows"})
	if err == nil || !strings.Contains(err.Error(), "published digest") {
		t.Fatalf("err = %v", err)
	}
	if _, serr := os.Stat(filepath.Join(root, manifest.ManifestPath)); serr == nil {
		t.Error("manifest written despite digest mismatch")
	}
}

func TestLinuxDirForOtherReleaseIsRejected(t *testing.T) {
	fakeGitHub(t, "release-9.8.7", false)
	root := newRoot(t)
	err := run([]string{"-root", root, "-version", "9.8.7", "-os", "linux", "-linux-dir", writeLinuxOut(t, "release-1.1.1")})
	if err == nil || !strings.Contains(err.Error(), "was not produced for") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewerAndFlagErrors(t *testing.T) {
	fakeGitHub(t, "release-9.8.7", false)
	root := newRoot(t)
	if err := run([]string{"-root", root}); err == nil {
		t.Error("no -version/-latest accepted")
	}
	if err := run([]string{"-root", root, "-latest", "-version", "1"}); err == nil {
		t.Error("both -version and -latest accepted")
	}
	if err := run([]string{"-root", root, "-version", "1", "-os", "plan9"}); err == nil {
		t.Error("unknown -os accepted")
	}
	if tag, err := latestTag(); err != nil || tag != "release-9.8.7" {
		t.Fatalf("latestTag = %q, %v", tag, err)
	}
}
