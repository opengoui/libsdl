// Package auto points puresdl at the embedded SDL3 library.
//
// Import it for its side effect, before the puresdl package:
//
//	import (
//		_ "github.com/opengoui/libsdl/auto"
//		"github.com/opengoui/puresdl/sdl"
//	)
//
// puresdl loads a library in its own init and ignores later Load calls, so
// SDL3_LIBRARY has to be set before that init runs. This package sets it to
// [libsdl.Path] when the variable is not already set; Path honours the
// LIBSDL_DIR environment variable. Go initialises packages in import-path
// order, so github.com/opengoui/libsdl/auto runs before
// github.com/opengoui/puresdl/sdl only because it sorts first; renaming the
// import path of either breaks that. Prefer the nested module's load.Load,
// which does not depend on init order. If extraction fails the
// environment is left untouched and puresdl falls back to the system library.
package auto

import (
	"os"

	"github.com/opengoui/libsdl"
)

func init() {
	if os.Getenv("SDL3_LIBRARY") != "" {
		return
	}
	if p, err := libsdl.Path(); err == nil {
		os.Setenv("SDL3_LIBRARY", p)
	}
}
