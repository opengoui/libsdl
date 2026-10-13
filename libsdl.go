// Package libsdl embeds a gzip-compressed SDL3 shared library for the
// current GOOS/GOARCH and extracts it on demand so that a purego based
// binding can load it.
//
// Only the blob for the target platform is linked into the binary. Supported
// targets are windows, darwin and linux on amd64 and arm64; every other
// platform (including js) makes [Path] fail with an error that matches
// [errors.ErrUnsupported].
//
// Typical use with puresdl:
//
//	path, err := libsdl.Path()
//	if err != nil { return err }
//	err = sdl.Load(path)
//
// The library is written once to a per-user cache directory named after the
// SDL version and the content hash, so later runs and concurrent processes
// reuse it. See [Path] for details.
package libsdl

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// Version is the upstream SDL release the embedded libraries were taken from
// (the tag release-3.4.18 is "3.4.18"). It is maintained by tools/update.
const Version = "3.4.18"

// Library describes the embedded shared library.
type Library struct {
	// Version is the SDL release, equal to [Version].
	Version string
	// Name is the file name the library is extracted as, for example
	// "libSDL3.so.0". It is empty when the platform is unsupported.
	Name string
	// Size is the size in bytes of the decompressed library.
	Size int64
	// SHA256 is the lowercase hex SHA-256 of the decompressed library.
	SHA256 string
}

// Info describes the library embedded for the current platform. It does not
// touch the file system. On an unsupported platform it returns a Library
// with only Version set.
func Info() Library {
	if !supported {
		return Library{Version: Version}
	}
	return Library{Version: Version, Name: libName, Size: libSize, SHA256: libSHA256}
}

var (
	pathMu   sync.Mutex
	pathDone string
)

// Path returns the file system path of the embedded SDL3 shared library,
// extracting it first if needed. Pass the result to puresdl's sdl.Load.
//
// The library lives in os.UserCacheDir()/libsdl/<version>-<sha256 prefix>/
// under its platform name (libSDL3.so.0, libSDL3.dylib or SDL3.dll). An
// existing file is reused only if its size and SHA-256 match the embedded
// library; otherwise it is replaced. Files are written to a temporary name
// in the same directory and renamed into place, so concurrent processes never
// observe a partial file and never overwrite a library that another process
// has loaded (important on Windows). If the cache directory cannot be used,
// the library is extracted into a new os.MkdirTemp directory that is not
// removed automatically.
//
// Path is safe for concurrent use. The result is memoized after success.
func Path() (string, error) {
	pathMu.Lock()
	defer pathMu.Unlock()
	if pathDone != "" {
		return pathDone, nil
	}
	p, err := current().ensure()
	if err != nil {
		return "", err
	}
	pathDone = p
	return p, nil
}

// current builds the extractor for the embedded platform blob.
func current() *extractor {
	return &extractor{
		blob:    blob,
		name:    libName,
		size:    libSize,
		sum:     libSHA256,
		version: Version,
		cache:   os.UserCacheDir,
	}
}

// errUnsupported builds the error for platforms without an embedded library.
func errUnsupported() error {
	return fmt.Errorf("libsdl: no embedded SDL3 %s for %s/%s: %w",
		Version, runtime.GOOS, runtime.GOARCH, errors.ErrUnsupported)
}

// errBadAsset reports an embedded blob that does not match its recorded
// size or hash. Falling back to another directory cannot help.
var errBadAsset = errors.New("libsdl: embedded library does not match its recorded hash")

// cacheSubdir is the directory name below the user cache dir.
const cacheSubdir = "libsdl"

func dirName(version, sum string) string {
	return filepath.Join(cacheSubdir, version+"-"+sum[:16])
}
