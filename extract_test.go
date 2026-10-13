package libsdl

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func fakeExtractor(t *testing.T, content string, cache string) *extractor {
	t.Helper()
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte(content))
	zw.Close()
	sum := sha256.Sum256([]byte(content))
	return &extractor{
		blob:    gz.Bytes(),
		name:    "libFake.so.0",
		size:    int64(len(content)),
		sum:     hex.EncodeToString(sum[:]),
		version: "9.9.9",
		cache:   func() (string, error) { return cache, nil },
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEmbeddedAssetMatchesRecordedHash(t *testing.T) {
	if !supported {
		t.Skip("unsupported platform")
	}
	e := current()
	e.cache = func() (string, error) { return t.TempDir(), nil }
	p, err := e.ensure() // ensure verifies size and hash while writing
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != libName {
		t.Fatalf("name = %s, want %s", filepath.Base(p), libName)
	}
	info := Info()
	if info.Name != libName || info.Size != libSize || info.SHA256 != libSHA256 || info.Version != Version {
		t.Fatalf("Info = %+v", info)
	}
}

func TestExtractAndIdempotent(t *testing.T) {
	cache := t.TempDir()
	e := fakeExtractor(t, strings.Repeat("sdl", 1000), cache)
	p1, err := e.ensure()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cache, "libsdl", "9.9.9-"+e.sum[:16], "libFake.so.0"); p1 != want {
		t.Fatalf("path = %s, want %s", p1, want)
	}
	if readFile(t, p1) != strings.Repeat("sdl", 1000) {
		t.Fatal("content mismatch")
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(p1); st.Mode().Perm()&0o100 == 0 {
			t.Fatalf("not executable: %v", st.Mode())
		}
	}
	st1, _ := os.Stat(p1)
	p2, err := e.ensure()
	if err != nil || p2 != p1 {
		t.Fatalf("second ensure = %s, %v", p2, err)
	}
	st2, _ := os.Stat(p2)
	if !os.SameFile(st1, st2) || !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatal("valid cache file was rewritten")
	}
	assertNoTemps(t, filepath.Dir(p1))
}

func assertNoTemps(t *testing.T, dir string) {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}
}

func TestCorruptCacheIsRepaired(t *testing.T) {
	for name, corrupt := range map[string]func(path, good string) error{
		"truncated":   func(p, g string) error { return os.WriteFile(p, []byte(g[:len(g)/2]), 0o755) },
		"same size":   func(p, g string) error { return os.WriteFile(p, bytes.Repeat([]byte{'x'}, len(g)), 0o755) },
		"empty":       func(p, g string) error { return os.WriteFile(p, nil, 0o755) },
		"is a folder": func(p, g string) error { return os.MkdirAll(filepath.Join(p, "sub"), 0o755) },
	} {
		t.Run(name, func(t *testing.T) {
			if name == "is a folder" && runtime.GOOS == "windows" {
				t.Skip()
			}
			good := strings.Repeat("sdl", 500)
			e := fakeExtractor(t, good, t.TempDir())
			p, err := e.ensure()
			if err != nil {
				t.Fatal(err)
			}
			os.RemoveAll(p)
			if err := corrupt(p, good); err != nil {
				t.Fatal(err)
			}
			if e.valid(p) {
				t.Fatal("corrupt file considered valid")
			}
			p2, err := e.ensure()
			if err != nil {
				t.Fatal(err)
			}
			if name == "is a folder" {
				// Unrepairable in place: must fall back to a temp dir.
				if p2 == p || readFile(t, p2) != good {
					t.Fatalf("no fallback: %s", p2)
				}
				return
			}
			if p2 != p || readFile(t, p2) != good {
				t.Fatal("not repaired")
			}
		})
	}
}

func TestReadOnlyCacheFallsBack(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("cannot make a directory unwritable")
	}
	ro := t.TempDir()
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ro, 0o700) })
	tmpRoot := t.TempDir()
	t.Setenv("TMPDIR", tmpRoot)

	e := fakeExtractor(t, "fallback-content", ro)
	p, err := e.ensure()
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(p, ro) {
		t.Fatalf("path %s is inside the read-only cache", p)
	}
	if !strings.HasPrefix(p, tmpRoot) || readFile(t, p) != "fallback-content" {
		t.Fatalf("unexpected fallback %s", p)
	}
}

func TestCacheDirErrorFallsBack(t *testing.T) {
	e := fakeExtractor(t, "x-content", "")
	e.cache = func() (string, error) { return "", errors.New("no home") }
	t.Setenv("TMPDIR", t.TempDir())
	p, err := e.ensure()
	if err != nil || readFile(t, p) != "x-content" {
		t.Fatalf("ensure = %q, %v", p, err)
	}
}

func TestBadAssetIsReported(t *testing.T) {
	e := fakeExtractor(t, "content", t.TempDir())
	e.sum = strings.Repeat("0", 64)
	if _, err := e.ensure(); !errors.Is(err, errBadAsset) {
		t.Fatalf("err = %v, want errBadAsset", err)
	}
	e = fakeExtractor(t, "content", t.TempDir())
	e.size++
	if _, err := e.ensure(); !errors.Is(err, errBadAsset) {
		t.Fatalf("size: err = %v", err)
	}
	e = fakeExtractor(t, "content", t.TempDir())
	e.blob = []byte("not gzip")
	if _, err := e.ensure(); err == nil {
		t.Fatal("expected gzip error")
	}
}

func TestUnsupported(t *testing.T) {
	e := &extractor{cache: os.UserCacheDir}
	if _, err := e.ensure(); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestStaleTempFilesRemoved(t *testing.T) {
	cache := t.TempDir()
	e := fakeExtractor(t, "content", cache)
	dir := filepath.Join(cache, dirName(e.version, e.sum))
	os.MkdirAll(dir, 0o700)
	old := filepath.Join(dir, "libFake.so.0.123.tmp")
	fresh := filepath.Join(dir, "libFake.so.0.456.tmp")
	os.WriteFile(old, nil, 0o600)
	os.WriteFile(fresh, nil, 0o600)
	past := timeAgo(2)
	os.Chtimes(old, past, past)
	if _, err := e.ensure(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("stale temp file survived")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh temp file (maybe in use) was removed")
	}
}

func TestConcurrentGoroutines(t *testing.T) {
	e := fakeExtractor(t, strings.Repeat("c", 1<<20), t.TempDir())
	const n = 16
	paths := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			paths[i], errs[i] = e.ensure()
		}()
	}
	close(start)
	wg.Wait()
	for i := range n {
		if errs[i] != nil || paths[i] != paths[0] {
			t.Fatalf("worker %d: %q, %v", i, paths[i], errs[i])
		}
	}
	if !e.valid(paths[0]) {
		t.Fatal("final file invalid")
	}
	assertNoTemps(t, filepath.Dir(paths[0]))
}

// TestConcurrentProcesses starts several copies of this test binary at the
// same moment against one cache directory.
func TestConcurrentProcesses(t *testing.T) {
	if os.Getenv("LIBSDL_TEST_CHILD") != "" {
		t.Skip("child process")
	}
	cache := t.TempDir()
	const n = 6
	cmds := make([]*exec.Cmd, n)
	outs := make([]*bytes.Buffer, n)
	for i := range n {
		outs[i] = new(bytes.Buffer)
		cmds[i] = exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
		cmds[i].Env = append(os.Environ(), "LIBSDL_TEST_CHILD="+cache)
		cmds[i].Stdout, cmds[i].Stderr = outs[i], outs[i]
	}
	for _, c := range cmds {
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
	}
	var first string
	for i, c := range cmds {
		if err := c.Wait(); err != nil {
			t.Fatalf("child %d: %v\n%s", i, err, outs[i])
		}
		var p string
		for _, l := range strings.Split(outs[i].String(), "\n") {
			if rest, ok := strings.CutPrefix(l, "PATH="); ok {
				p = rest
			}
		}
		if p == "" || (first != "" && p != first) {
			t.Fatalf("child %d path %q (first %q)\n%s", i, p, first, outs[i])
		}
		first = p
	}
	e := fakeExtractor(t, strings.Repeat("p", 4<<20), cache)
	if !e.valid(first) {
		t.Fatal("final file invalid")
	}
	assertNoTemps(t, filepath.Dir(first))
}

func TestHelperProcess(t *testing.T) {
	cache := os.Getenv("LIBSDL_TEST_CHILD")
	if cache == "" {
		t.Skip("helper")
	}
	e := fakeExtractor(t, strings.Repeat("p", 4<<20), cache)
	p, err := e.ensure()
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("PATH=" + p)
}
