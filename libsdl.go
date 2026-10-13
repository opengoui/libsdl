// Package libsdl embeds a gzip-compressed SDL3 shared library for the
// current GOOS/GOARCH and releases it to disk on demand so that a purego
// based binding can load it.
//
// Only the blob for the target platform is linked into the binary. Supported
// targets are windows, darwin and linux on amd64 and arm64; every other
// platform (including js) makes [Extract] and [Path] fail with an error that
// matches [errors.ErrUnsupported].
//
// This package has no dependencies. To release and load in one call use the
// nested module github.com/opengoui/libsdl/load:
//
//	path, err := load.Load("/opt/myapp/lib") // or load.Load() for the temp dir
//
// or release the file yourself and hand the path to puresdl:
//
//	path, err := libsdl.Extract("/opt/myapp/lib")
//	if err != nil { return err }
//	err = sdl.Load(path)
//
// With a custom directory the library is reused only if the file there has
// the embedded size and SHA-256, and is rewritten otherwise. Without one it
// goes to a private directory below os.TempDir(). See [Extract].
package libsdl

import (
	"errors"
	"fmt"
	"os"
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

// DirEnv is the environment variable that selects the directory used when
// [Extract] is called with an empty dir (and therefore by [Path]).
const DirEnv = "LIBSDL_DIR"

var (
	extractMu   sync.Mutex
	extractDone = map[string]string{} // absolute directory ("" = temp default) -> path
)

// Extract releases the embedded SDL3 shared library into dir and returns the
// path of the file, named like the platform library (libSDL3.so.0,
// libSDL3.dylib or SDL3.dll). Pass the result to puresdl's sdl.Load.
//
// Custom directory. A non-empty dir (or, when dir is empty, the directory in
// the LIBSDL_DIR environment variable) is created with mode 0755 if needed.
// If dir/<name> already is a regular file whose size and SHA-256 equal the
// embedded library it is reused untouched; a missing, truncated, modified or
// otherwise different file, or a symlink, is replaced by a regular file.
// Replacement writes a temporary file in dir and renames it into place, so
// readers never see a partial file and a symlink is replaced, never written
// through. A directory at the target path is an error. A directory that
// cannot be created or written is an error: there is no fallback to another
// location. Ownership and permissions of a user-chosen directory are not
// checked, so that shared install locations (next to the executable, say)
// work; the hash check protects against corruption, not against a hostile
// process that can write to the directory. Choose a directory only you can
// write to. On Windows a DLL that is loaded cannot be replaced: if the file
// differs and is in use, Extract returns an error saying so. A file with the
// right hash is reused even while loaded.
//
// Default directory. With neither dir nor LIBSDL_DIR, the library goes to
// os.TempDir()/libsdl-<uid>-<version>-<sha256 prefix>/. The name is
// deterministic, so repeated runs and concurrent processes share one copy.
// The directory must be a real directory owned by the current user (it is
// chmod 0700 when it is ours and too open), and an existing file is reused
// only if it is a regular file owned by the user and not writable by others
// with a matching size and hash; a directory owned by someone else is an
// error. Default directories are not removed automatically.
//
// An explicit dir always wins over LIBSDL_DIR. A relative dir is made
// absolute against the current working directory at the time of the call. A
// leading "~" is not expanded and is rejected with an error; pass an absolute
// path (for example built from os.UserHomeDir). Extract is safe for
// concurrent use, also across processes, and remembers the result of each
// successful call per absolute directory. A remembered result is used only
// while the file still exists with the embedded size; otherwise the file is
// verified and rewritten again.
func Extract(dir string) (string, error) {
	if dir == "" {
		dir = os.Getenv(DirEnv)
	}
	e := current()
	key, err := e.cacheKey(dir)
	if err != nil {
		return "", err
	}
	extractMu.Lock()
	defer extractMu.Unlock()
	if p, ok := extractDone[key]; ok && e.stillThere(p) {
		return p, nil
	}
	p, err := e.ensure(dir)
	if err != nil {
		return "", err
	}
	extractDone[key] = p
	return p, nil
}

// Path is Extract(""): it releases the library into the directory named by
// LIBSDL_DIR, or the private default directory below os.TempDir() when the
// variable is unset, and returns its path.
func Path() (string, error) { return Extract("") }

// current builds the extractor for the embedded platform blob.
func current() *extractor {
	return &extractor{
		blob:    blob,
		name:    libName,
		size:    libSize,
		sum:     libSHA256,
		version: Version,
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
