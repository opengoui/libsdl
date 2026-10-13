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
	"time"
)

func fakeExtractor(t *testing.T, content string) *extractor {
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
	}
}

// useTempRoot points os.TempDir at a fresh directory and returns it.
func useTempRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	t.Setenv("TMP", root)
	t.Setenv("TEMP", root)
	return root
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
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

func skipWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions or symlinks")
	}
}

func TestEmbeddedAssetMatchesRecordedHash(t *testing.T) {
	if !supported {
		t.Skip("unsupported platform")
	}
	p, err := current().ensure(t.TempDir()) // ensure verifies size and hash while writing
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

func TestBadAssetIsReported(t *testing.T) {
	for _, dir := range []string{t.TempDir(), ""} {
		useTempRoot(t)
		e := fakeExtractor(t, "content")
		e.sum = strings.Repeat("0", 64)
		if _, err := e.ensure(dir); !errors.Is(err, errBadAsset) {
			t.Fatalf("dir %q: err = %v, want errBadAsset", dir, err)
		}
		e = fakeExtractor(t, "content")
		e.size++
		if _, err := e.ensure(dir); !errors.Is(err, errBadAsset) {
			t.Fatalf("dir %q size: err = %v", dir, err)
		}
		e = fakeExtractor(t, "content")
		e.blob = []byte("not gzip")
		if _, err := e.ensure(dir); err == nil {
			t.Fatal("expected gzip error")
		}
	}
}

func TestUnsupported(t *testing.T) {
	for _, dir := range []string{"", t.TempDir()} {
		if _, err := (&extractor{}).ensure(dir); !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("err = %v", err)
		}
	}
}

// ---- custom directory ----

func TestCustomDirWritesMissingFile(t *testing.T) {
	content := strings.Repeat("sdl", 1000)
	e := fakeExtractor(t, content)
	dir := filepath.Join(t.TempDir(), "a", "b") // parents are created
	p, err := e.ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(dir, "libFake.so.0") || readFile(t, p) != content {
		t.Fatalf("path %s or content wrong", p)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o755 {
			t.Fatalf("mode = %v, want 0755", st.Mode().Perm())
		}
	}
	assertNoTemps(t, dir)
}

func TestCustomDirRelativeBecomesAbsolute(t *testing.T) {
	t.Chdir(t.TempDir())
	p, err := fakeExtractor(t, "rel").ensure("lib")
	if err != nil || !filepath.IsAbs(p) {
		t.Fatalf("ensure = %q, %v", p, err)
	}
}

func TestCustomDirSameHashIsUntouched(t *testing.T) {
	e := fakeExtractor(t, strings.Repeat("sdl", 500))
	dir := t.TempDir()
	p, err := e.ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	past := timeAgo(48)
	os.Chtimes(p, past, past)
	st1, _ := os.Stat(p)
	for range 2 {
		p2, err := e.ensure(dir)
		if err != nil || p2 != p {
			t.Fatalf("ensure = %s, %v", p2, err)
		}
		st2, _ := os.Stat(p2)
		if !os.SameFile(st1, st2) || !st1.ModTime().Equal(st2.ModTime()) {
			t.Fatal("valid file was rewritten")
		}
	}
	assertNoTemps(t, dir)
}

func TestCustomDirRewritesWrongContent(t *testing.T) {
	good := strings.Repeat("sdl", 500)
	for name, bad := range map[string][]byte{
		"truncated":    []byte(good[:len(good)/2]),
		"same size":    bytes.Repeat([]byte{'x'}, len(good)),
		"empty":        nil,
		"longer":       []byte(good + "extra"),
		"other lib":    []byte("a completely different library"),
		"last byte":    append([]byte(good[:len(good)-1]), 'X'),
		"prefix valid": []byte(good[:len(good)-1]),
	} {
		t.Run(name, func(t *testing.T) {
			e := fakeExtractor(t, good)
			dir := t.TempDir()
			final := filepath.Join(dir, e.name)
			if err := os.WriteFile(final, bad, 0o600); err != nil {
				t.Fatal(err)
			}
			if e.valid(final, false) {
				t.Fatal("bad file considered valid")
			}
			p, err := e.ensure(dir)
			if err != nil || p != final {
				t.Fatalf("ensure = %s, %v", p, err)
			}
			if readFile(t, p) != good || !e.valid(p, false) {
				t.Fatal("not rewritten")
			}
			assertNoTemps(t, dir)
		})
	}
}

func TestCustomDirReplacesSymlinkWithoutWritingThrough(t *testing.T) {
	skipWindows(t)
	good := strings.Repeat("sdl", 500)
	for name, targetContent := range map[string]string{
		"to different file": "precious user data",
		"to valid copy":     good, // right hash, but still a link
	} {
		t.Run(name, func(t *testing.T) {
			e := fakeExtractor(t, good)
			dir := t.TempDir()
			target := filepath.Join(t.TempDir(), "elsewhere")
			if err := os.WriteFile(target, []byte(targetContent), 0o600); err != nil {
				t.Fatal(err)
			}
			final := filepath.Join(dir, e.name)
			if err := os.Symlink(target, final); err != nil {
				t.Fatal(err)
			}
			p, err := e.ensure(dir)
			if err != nil {
				t.Fatal(err)
			}
			li, _ := os.Lstat(p)
			if !li.Mode().IsRegular() {
				t.Fatalf("target is still %v", li.Mode())
			}
			if readFile(t, p) != good {
				t.Fatal("content not written")
			}
			if readFile(t, target) != targetContent {
				t.Fatal("symlink target was modified")
			}
		})
	}
}

func TestCustomDirDanglingSymlinkIsReplaced(t *testing.T) {
	skipWindows(t)
	e := fakeExtractor(t, "dangling")
	dir := t.TempDir()
	final := filepath.Join(dir, e.name)
	if err := os.Symlink(filepath.Join(dir, "nowhere"), final); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ensure(dir); err != nil {
		t.Fatal(err)
	}
	if li, _ := os.Lstat(final); !li.Mode().IsRegular() {
		t.Fatalf("mode %v", li.Mode())
	}
	if _, err := os.Stat(filepath.Join(dir, "nowhere")); err == nil {
		t.Fatal("wrote through the dangling symlink")
	}
}

func TestCustomDirSymlinkedDirectoryIsAllowed(t *testing.T) {
	skipWindows(t)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	e := fakeExtractor(t, "via link")
	if _, err := e.ensure(link); err != nil {
		t.Fatal(err)
	}
	if readFile(t, filepath.Join(real, e.name)) != "via link" {
		t.Fatal("file not in the real directory")
	}
}

func TestCustomDirDirectoryAtTargetIsError(t *testing.T) {
	e := fakeExtractor(t, "content")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, e.name, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := e.ensure(dir)
	if err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("err = %v, want a clear directory error", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, e.name, "sub")); serr != nil {
		t.Fatal("directory contents were removed")
	}
	assertNoTemps(t, dir)
}

func TestCustomDirUnusableIsErrorWithoutFallback(t *testing.T) {
	root := useTempRoot(t)
	e := fakeExtractor(t, "content")

	// A regular file where the directory should be.
	file := filepath.Join(t.TempDir(), "file")
	os.WriteFile(file, nil, 0o644)
	for _, dir := range []string{file, filepath.Join(file, "sub")} {
		if _, err := e.ensure(dir); err == nil {
			t.Fatalf("ensure(%s) succeeded", dir)
		}
	}

	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		ro := t.TempDir()
		if err := os.Chmod(ro, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(ro, 0o700) })
		for _, dir := range []string{ro, filepath.Join(ro, "new")} {
			if _, err := e.ensure(dir); err == nil {
				t.Fatalf("ensure(%s) in a read-only directory succeeded", dir)
			}
		}
	}
	if ents, _ := os.ReadDir(root); len(ents) != 0 {
		t.Fatalf("fell back to the temp dir: %v", ents)
	}
}

func TestCustomDirDoesNotApplyStrictChecks(t *testing.T) {
	skipWindows(t)
	content := "shared install"
	e := fakeExtractor(t, content)
	dir := t.TempDir()
	os.Chmod(dir, 0o777)
	final := filepath.Join(dir, e.name)
	os.WriteFile(final, []byte(content), 0o666)
	os.Chmod(final, 0o666)
	st1, _ := os.Stat(final)
	p, err := e.ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	st2, _ := os.Stat(p)
	if !os.SameFile(st1, st2) || st2.Mode().Perm() != 0o666 {
		t.Fatalf("shared file was changed: %v", st2.Mode())
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o777 {
		t.Fatalf("shared dir mode changed to %v", st.Mode())
	}
}

func TestStaleTempFilesRemoved(t *testing.T) {
	for _, dir := range []string{t.TempDir(), ""} {
		useTempRoot(t)
		e := fakeExtractor(t, "content")
		d := dir
		if d == "" {
			d = e.defaultDir()
			os.MkdirAll(d, 0o700)
		}
		old := filepath.Join(d, "libFake.so.0.123.tmp")
		fresh := filepath.Join(d, "libFake.so.0.456.tmp")
		foreign := filepath.Join(d, "user-data.tmp")
		for _, f := range []string{old, fresh, foreign} {
			os.WriteFile(f, nil, 0o600)
		}
		past := timeAgo(2)
		os.Chtimes(old, past, past)
		os.Chtimes(foreign, past, past)
		if _, err := e.ensure(dir); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(old); err == nil {
			t.Error("stale temp file survived")
		}
		if _, err := os.Stat(fresh); err != nil {
			t.Error("fresh temp file (maybe in use) was removed")
		}
		if _, err := os.Stat(foreign); err != nil {
			t.Error("a file that is not ours was removed")
		}
	}
}

// TestWindowsInUse covers the Windows rule that a loaded DLL cannot be
// replaced. An open handle (Go opens without FILE_SHARE_DELETE) stands in
// for a loaded module.
func TestWindowsInUse(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows only")
	}
	e := fakeExtractor(t, "right content")
	dir := t.TempDir()
	final := filepath.Join(dir, e.name)

	os.WriteFile(final, []byte("right content"), 0o644)
	f, err := os.Open(final)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.ensure(dir); err != nil {
		t.Fatalf("same hash while in use: %v", err)
	}
	f.Close()

	os.WriteFile(final, []byte("wrong content"), 0o644)
	f, err = os.Open(final)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_, err = e.ensure(dir)
	if err == nil || !strings.Contains(err.Error(), "running process") {
		t.Fatalf("err = %v, want an in-use error", err)
	}
	if readFile(t, final) != "wrong content" {
		t.Fatal("file in use was modified")
	}
	assertNoTemps(t, dir)
}

// ---- default directory (os.TempDir) ----

func TestDefaultDirInTempAndReused(t *testing.T) {
	root := useTempRoot(t)
	content := strings.Repeat("sdl", 1000)
	e := fakeExtractor(t, content)
	p1, err := e.ensure("")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "libsdl-"+userTag()+"-9.9.9-"+e.sum[:16], "libFake.so.0")
	if p1 != want {
		t.Fatalf("path = %s, want %s", p1, want)
	}
	if readFile(t, p1) != content {
		t.Fatal("content mismatch")
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(filepath.Dir(p1)); st.Mode().Perm() != 0o700 {
			t.Fatalf("dir mode = %v, want 0700", st.Mode().Perm())
		}
		if st, _ := os.Stat(p1); st.Mode().Perm() != 0o755 {
			t.Fatalf("file mode = %v, want 0755", st.Mode().Perm())
		}
	}
	past := timeAgo(24)
	os.Chtimes(p1, past, past)
	st1, _ := os.Stat(p1)
	p2, err := e.ensure("")
	if err != nil || p2 != p1 {
		t.Fatalf("second ensure = %s, %v", p2, err)
	}
	st2, _ := os.Stat(p2)
	if !os.SameFile(st1, st2) || !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatal("valid file was rewritten")
	}
	assertNoTemps(t, filepath.Dir(p1))
}

func TestDefaultDirRepairsCorruptFile(t *testing.T) {
	useTempRoot(t)
	good := strings.Repeat("sdl", 500)
	e := fakeExtractor(t, good)
	p, err := e.ensure("")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(p, []byte(good[:10]), 0o755)
	p2, err := e.ensure("")
	if err != nil || p2 != p || readFile(t, p) != good {
		t.Fatalf("not repaired: %s, %v", p2, err)
	}
}

func TestDefaultDirPermissiveIsTightened(t *testing.T) {
	skipWindows(t)
	useTempRoot(t)
	e := fakeExtractor(t, "content")
	dir := e.defaultDir()
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	os.Chmod(dir, 0o777)
	final := filepath.Join(dir, e.name)
	os.WriteFile(final, []byte("content"), 0o666)
	os.Chmod(final, 0o666)
	p, err := e.ensure("")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, p} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm()&0o022 != 0 {
			t.Errorf("%s is group/other writable: %v", path, st.Mode())
		}
	}
}

func TestDefaultDirHostileEntriesAreRejected(t *testing.T) {
	skipWindows(t)
	t.Run("symlink instead of directory", func(t *testing.T) {
		useTempRoot(t)
		e := fakeExtractor(t, "content")
		victim := t.TempDir()
		if err := os.Symlink(victim, e.defaultDir()); err != nil {
			t.Fatal(err)
		}
		if _, err := e.ensure(""); err == nil {
			t.Fatal("accepted a symlinked default directory")
		}
		if ents, _ := os.ReadDir(victim); len(ents) != 0 {
			t.Fatalf("wrote into the symlink target: %v", ents)
		}
	})
	t.Run("regular file instead of directory", func(t *testing.T) {
		useTempRoot(t)
		e := fakeExtractor(t, "content")
		os.WriteFile(e.defaultDir(), nil, 0o600)
		if _, err := e.ensure(""); err == nil {
			t.Fatal("accepted a file as the default directory")
		}
	})
	t.Run("symlink instead of library", func(t *testing.T) {
		useTempRoot(t)
		e := fakeExtractor(t, "content")
		os.MkdirAll(e.defaultDir(), 0o700)
		target := filepath.Join(t.TempDir(), "t")
		os.WriteFile(target, []byte("content"), 0o755)
		os.Symlink(target, filepath.Join(e.defaultDir(), e.name))
		p, err := e.ensure("")
		if err != nil {
			t.Fatal(err)
		}
		if li, _ := os.Lstat(p); !li.Mode().IsRegular() {
			t.Fatalf("library is %v", li.Mode())
		}
	})
	t.Run("directory owned by another user", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("needs root to hand a directory to another user")
		}
		useTempRoot(t)
		e := fakeExtractor(t, "content")
		dir := e.defaultDir()
		os.MkdirAll(dir, 0o777)
		if err := os.Chown(dir, 65534, 65534); err != nil {
			t.Skip(err)
		}
		os.Chmod(dir, 0o777)
		// As root, chmod succeeds but the owner stays foreign.
		if _, err := e.ensure(""); err == nil {
			t.Fatal("accepted a directory owned by another user")
		}
	})
}

// ---- concurrency ----

func TestConcurrentGoroutines(t *testing.T) {
	for name, dir := range map[string]string{"custom": t.TempDir(), "default": ""} {
		t.Run(name, func(t *testing.T) {
			useTempRoot(t)
			e := fakeExtractor(t, strings.Repeat("c", 1<<20))
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
					paths[i], errs[i] = e.ensure(dir)
				}()
			}
			close(start)
			wg.Wait()
			for i := range n {
				if errs[i] != nil || paths[i] != paths[0] {
					t.Fatalf("worker %d: %q, %v", i, paths[i], errs[i])
				}
			}
			if !e.valid(paths[0], name == "default") {
				t.Fatal("final file invalid")
			}
			assertNoTemps(t, filepath.Dir(paths[0]))
		})
	}
}

// TestConcurrentProcesses starts several copies of this test binary at the
// same moment against one directory.
func TestConcurrentProcesses(t *testing.T) {
	if os.Getenv("LIBSDL_TEST_CHILD") != "" {
		t.Skip("child process")
	}
	for _, mode := range []string{"custom", "default"} {
		t.Run(mode, func(t *testing.T) {
			root := useTempRoot(t)
			dir := t.TempDir()
			const n = 6
			cmds := make([]*exec.Cmd, n)
			outs := make([]*bytes.Buffer, n)
			for i := range n {
				outs[i] = new(bytes.Buffer)
				cmds[i] = exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
				cmds[i].Env = append(os.Environ(), "LIBSDL_TEST_CHILD="+mode, "LIBSDL_TEST_DIR="+dir, "TMPDIR="+root)
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
						p = strings.TrimSpace(rest)
					}
				}
				if p == "" || (first != "" && p != first) {
					t.Fatalf("child %d path %q (first %q)\n%s", i, p, first, outs[i])
				}
				first = p
			}
			e := fakeExtractor(t, strings.Repeat("p", 4<<20))
			if !e.valid(first, mode == "default") {
				t.Fatal("final file invalid")
			}
			assertNoTemps(t, filepath.Dir(first))
		})
	}
}

func TestHelperProcess(t *testing.T) {
	mode := os.Getenv("LIBSDL_TEST_CHILD")
	if mode == "" {
		t.Skip("helper")
	}
	dir := ""
	if mode == "custom" {
		dir = os.Getenv("LIBSDL_TEST_DIR")
	}
	e := fakeExtractor(t, strings.Repeat("p", 4<<20))
	p, err := e.ensure(dir)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("PATH=" + p)
}

func timeAgo(hours int) time.Time { return time.Now().Add(-time.Duration(hours) * time.Hour) }
