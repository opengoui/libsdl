package libsdl

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPathMemoizedAndInTemp(t *testing.T) {
	root := useTempRoot(t)
	t.Setenv(DirEnv, "")
	p1, err := Path()
	if !supported {
		if !errors.Is(err, errors.ErrUnsupported) {
			t.Fatalf("err = %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(filepath.Dir(p1)) != root {
		t.Fatalf("Path %s is not below the temp dir %s", p1, root)
	}
	p2, err := Path()
	if err != nil || p1 != p2 {
		t.Fatalf("second Path = %q, %v", p2, err)
	}
	e, err := Extract("")
	if err != nil || e != p1 {
		t.Fatalf("Extract(\"\") = %q, %v; Path = %q", e, err, p1)
	}
}

func TestExtractCustomDirAndEnv(t *testing.T) {
	if !supported {
		t.Skip("unsupported platform")
	}
	root := useTempRoot(t)
	envDir, argDir := t.TempDir(), t.TempDir()
	t.Setenv(DirEnv, envDir)

	p, err := Path()
	if err != nil || p != filepath.Join(envDir, libName) {
		t.Fatalf("Path with %s = %q, %v", DirEnv, p, err)
	}
	// The explicit argument wins over the variable.
	q, err := Extract(argDir)
	if err != nil || q != filepath.Join(argDir, libName) {
		t.Fatalf("Extract(arg) = %q, %v", q, err)
	}
	if ents, _ := os.ReadDir(root); len(ents) != 0 {
		t.Fatalf("temp dir used although a directory was given: %v", ents)
	}
	// A memoised hit only re-checks existence and size, so same-size
	// tampering is not noticed...
	orig, _ := os.ReadFile(q)
	same := append([]byte(nil), orig...)
	same[0] ^= 0xff
	os.WriteFile(q, same, 0o755)
	if _, err := Extract(argDir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(q); string(b) != string(same) {
		t.Fatal("memoised call rewrote a file of the right size")
	}
	// ...but a different size is repaired, and a trailing-slash variant
	// shares the cache entry.
	os.WriteFile(q, []byte("tampered"), 0o755)
	if _, err := Extract(argDir + string(filepath.Separator) + "."); err != nil {
		t.Fatal(err)
	}
	if !current().valid(q, false) {
		t.Fatal("file was not repaired")
	}
}

func TestExtractErrorsAreNotMemoised(t *testing.T) {
	if !supported {
		t.Skip("unsupported platform")
	}
	file := filepath.Join(t.TempDir(), "f")
	os.WriteFile(file, nil, 0o644)
	dir := filepath.Join(file, "sub")
	if _, err := Extract(dir); err == nil {
		t.Fatal("expected error")
	}
	os.Remove(file)
	if _, err := Extract(dir); err != nil {
		t.Fatalf("after fixing the cause: %v", err)
	}
}

func TestExtractMemoRevalidated(t *testing.T) {
	if !supported {
		t.Skip("unsupported platform")
	}
	d := t.TempDir()
	p1, err := Extract(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p1); err != nil {
		t.Fatal(err)
	}
	p2, err := Extract(d + string(filepath.Separator))
	if err != nil || p2 != p1 {
		t.Fatalf("Extract after removal = %q, %v; want %q", p2, err, p1)
	}
	if _, err := os.Stat(p2); err != nil {
		t.Fatalf("library was not restored: %v", err)
	}
}

func TestExtractRelativeDirFollowsChdir(t *testing.T) {
	if !supported {
		t.Skip("unsupported platform")
	}
	a, b := t.TempDir(), t.TempDir()
	t.Chdir(a)
	p1, err := Extract("rel")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(b)
	p2, err := Extract("rel")
	if err != nil || p1 == p2 || filepath.Dir(filepath.Dir(p2)) == filepath.Dir(filepath.Dir(p1)) {
		t.Fatalf("p1 = %q, p2 = %q, %v", p1, p2, err)
	}
	if _, err := os.Stat(p2); err != nil {
		t.Fatal(err)
	}
}

func TestExtractRejectsTilde(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	for _, d := range []string{"~", "~/sdl"} {
		if _, err := Extract(d); err == nil {
			t.Errorf("Extract(%q) succeeded", d)
		}
	}
	if _, err := os.Stat(filepath.Join(cwd, "~")); err == nil {
		t.Error("a directory named ~ was created")
	}
}

func TestExtractForceRevalidatesMemoizedFile(t *testing.T) {
	if !supported {
		t.Skip("unsupported platform")
	}
	dir := t.TempDir()
	path, err := Extract(dir)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !IsExtracted(dir) {
		t.Fatal("a newly extracted library was not recognized")
	}

	tampered := bytes.Clone(original)
	tampered[0] ^= 0xff
	if err := os.WriteFile(path, tampered, 0o755); err != nil {
		t.Fatal(err)
	}
	if IsExtracted(dir) {
		t.Fatal("IsExtracted accepted same-size tampering")
	}
	if _, err := Extract(dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, tampered) {
		t.Fatalf("memoized Extract changed the file: %v", err)
	}

	if _, err := ExtractForce(dir); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("ExtractForce did not restore the embedded bytes: %v", err)
	}
	if !IsExtracted(dir) {
		t.Fatal("repaired library was not recognized")
	}
}

func TestCleanCacheRemovesOnlyDefaultCache(t *testing.T) {
	if !supported {
		t.Skip("unsupported platform")
	}
	root := useTempRoot(t)
	t.Setenv(DirEnv, "")
	path, err := Extract("")
	if err != nil {
		t.Fatal(err)
	}
	defaultDir := filepath.Dir(path)
	if filepath.Dir(defaultDir) != root {
		t.Fatalf("default path %q is not below temp root %q", path, root)
	}

	customDir := t.TempDir()
	t.Setenv(DirEnv, customDir)
	customPath, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := CleanCache(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(defaultDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("default cache still exists: %v", err)
	}
	if _, err := os.Stat(customPath); err != nil {
		t.Fatalf("custom cache was removed: %v", err)
	}
	if IsExtracted(defaultDir) {
		t.Fatal("removed default cache is still reported as extracted")
	}

	t.Setenv(DirEnv, "")
	if _, err := Extract(""); err != nil {
		t.Fatalf("Extract after cleanup: %v", err)
	}
}

func TestCleanCacheRefusesSymlinkedDirectory(t *testing.T) {
	if !supported {
		t.Skip("unsupported platform")
	}
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require additional privileges on Windows")
	}
	useTempRoot(t)
	t.Setenv(DirEnv, "")
	e := current()
	victim := t.TempDir()
	if err := os.Symlink(victim, e.defaultDir()); err != nil {
		t.Fatal(err)
	}
	if err := CleanCache(); err == nil {
		t.Fatal("CleanCache removed a symlinked cache directory")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("symlink target was removed: %v", err)
	}
}
