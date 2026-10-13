package libsdl_test

import (
	"runtime"
	"testing"

	"github.com/opengoui/libsdl"
	"github.com/opengoui/libsdl/internal/manifest"
)

func TestInfoMatchesManifest(t *testing.T) {
	m, err := manifest.Load(".")
	if err != nil {
		t.Fatal(err)
	}
	if libsdl.Version != m.Version {
		t.Fatalf("Version = %s, manifest %s", libsdl.Version, m.Version)
	}
	f := m.Find(runtime.GOOS, runtime.GOARCH)
	info := libsdl.Info()
	if f == nil {
		if info.Name != "" || info.Version != libsdl.Version {
			t.Fatalf("unsupported platform Info = %+v", info)
		}
		return
	}
	want := libsdl.Library{Version: m.Version, Name: f.LibName, Size: f.Size, SHA256: f.SHA256}
	if info != want {
		t.Fatalf("Info = %+v, want %+v", info, want)
	}
}
