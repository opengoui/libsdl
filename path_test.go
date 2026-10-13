package libsdl

import (
	"errors"
	"testing"
)

func TestPathMemoized(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // linux
	t.Setenv("HOME", t.TempDir())           // darwin (Library/Caches)
	t.Setenv("LocalAppData", t.TempDir())   // windows
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
	p2, err := Path()
	if err != nil || p1 != p2 {
		t.Fatalf("second Path = %q, %v", p2, err)
	}
}
