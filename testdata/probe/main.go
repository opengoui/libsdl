// Command probe is built by TestLoadWithPuresdl in a scratch module that
// depends on libsdl and puresdl. With -path it prints libsdl.Path(); without
// it verifies that SDL3_LIBRARY (set by the caller to that path) is the
// library puresdl really loaded and that SDL reports the pinned version.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opengoui/libsdl"
	"github.com/opengoui/puresdl/sdl"
)

func main() {
	printPath := flag.Bool("path", false, "print libsdl.Path() and exit")
	flag.Parse()
	path, err := libsdl.Path()
	if err != nil {
		fail("libsdl.Path: %v", err)
	}
	if *printPath {
		fmt.Print(path)
		return
	}
	want, _ := filepath.EvalSymlinks(path)
	if env := os.Getenv(sdl.LibraryEnv); env != path {
		fail("%s=%q, want %q", sdl.LibraryEnv, env, path)
	}
	if err := sdl.LoadError(); err != nil {
		fail("puresdl did not load: %v", err)
	}
	// puresdl silently falls back to system copies, so prove which file is mapped.
	if ok, err := mappedFrom(want); err != nil {
		fmt.Println("warning: cannot verify mapping:", err)
	} else if !ok {
		fail("SDL is loaded, but not from %s", want)
	}
	v := sdl.GetVersion()
	got := fmt.Sprintf("%d.%d.%d", v/1000000, v/1000%1000, v%1000)
	if got != libsdl.Version {
		fail("SDL_GetVersion = %s, libsdl.Version = %s", got, libsdl.Version)
	}
	if n := sdl.GetNumVideoDrivers(); n < 1 {
		fail("no video drivers")
	}
	if !strings.Contains(sdl.GetRevision(), libsdl.Version) {
		fmt.Printf("note: revision %q does not mention %s\n", sdl.GetRevision(), libsdl.Version)
	}
	fmt.Printf("OK version=%s revision=%s lib=%s\n", got, sdl.GetRevision(), want)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "FAIL: "+format+"\n", args...)
	os.Exit(1)
}
