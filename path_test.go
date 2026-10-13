package libsdl

import (
	"errors"
	"os"
	"path/filepath"
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
	// Memoised per directory: a later change on disk is not noticed...
	os.WriteFile(q, []byte("tampered"), 0o755)
	if q2, err := Extract(argDir); err != nil || q2 != q {
		t.Fatalf("memoised Extract = %q, %v", q2, err)
	}
	if b, _ := os.ReadFile(q); string(b) != "tampered" {
		t.Fatal("memoised call touched the file")
	}
	// ...but a fresh directory key is extracted again, repairing the file.
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
