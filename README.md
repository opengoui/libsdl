# libsdl

`github.com/opengoui/libsdl` embeds gzip-compressed shared libraries from the official SDL3 release in a Go module using `go:embed`. It selects the library for the current `GOOS/GOARCH`, extracts it to disk when needed (to a custom directory or the system temporary directory), and returns its path for a purego-based binding such as the sibling module [puresdl](../puresdl) to load. No cgo or preinstalled SDL3 is required. The root module has no dependencies.

Pinned SDL version: **3.4.18** (upstream tag `release-3.4.18`); see `libsdl.Version` and `assets/MANIFEST.json`.

## Usage

### 1. `load.Load` (recommended)

The nested module `github.com/opengoui/libsdl/load` (in `load/`) has its own `go.mod` and depends on libsdl and puresdl:

```go
import "github.com/opengoui/libsdl/load"

path, err := load.Load("/opt/myapp/lib") // Use a custom directory
path, err := load.Load()                 // Uses LIBSDL_DIR, or the system temp directory
```

`Load` first extracts the library with `libsdl.Extract`, then calls `sdl.Load(path)` from puresdl. It returns an error if `sdl.LoadedPath()` does not match the extracted path (meaning a different library was actually bound). puresdl may have loaded a system library, such as one installed by Homebrew, during package initialization. `sdl.Load(path)` switches to the new path and rebinds all functions, so this does not depend on package initialization order. Call `Load` before using SDL. On macOS, if another SDL3 copy has already been mapped, both copies remain in the process and the Objective-C runtime prints a `Class ... is implemented in both ...` warning. To avoid this, use method 2 or set `SDL3_LIBRARY` before starting the process. On Windows, if an `SDL3.dll` with the same name is already loaded, the system loader may return that module; call `Load` before anything else loads SDL.

The `replace` directives in `load/go.mod` for `github.com/opengoui/libsdl => ../` and `github.com/opengoui/puresdl => ../../puresdl` are for local development with all three repositories checked out side by side. Remove them and use tagged versions when publishing (including a `load/vX.Y.Z` tag). Go ignores `replace` directives in dependency modules. Before publication, a consuming module must therefore either add its own `replace` directives for `libsdl`, `libsdl/load`, and `puresdl` pointing to local checkouts, or use a `go.work` file; otherwise `go get github.com/opengoui/libsdl/load` cannot resolve `v0.0.0`.

### 2. `Extract` manually, then call `sdl.Load`

```go
path, err := libsdl.Extract("/opt/myapp/lib") // With an empty dir, uses LIBSDL_DIR or the temp directory; equivalent to libsdl.Path()
if err != nil { return err }
err = sdl.Load(path)                          // Requires a puresdl version that supports switching (see below)
```

On unsupported platforms, `errors.Is(err, errors.ErrUnsupported)` is true. `libsdl.Info()` performs no I/O and returns the current platform library's name, size, and SHA-256 hash.

### Extraction behavior

API: `Extract(dir string) (string, error)` (safe for concurrent use; remembers one successful result per absolute directory and verifies that the file still exists with the expected size before reusing it; failures are not cached), `Path()` (equivalent to `Extract("")`), `Info()`, `Version`, `Library`, and `DirEnv = "LIBSDL_DIR"`.

The library filenames are `libSDL3.so.0`, `libSDL3.dylib`, and `SDL3.dll`.

**Custom directory** (the `Extract` argument, or `LIBSDL_DIR` when `dir == ""`; an explicit argument takes precedence):

- If the directory does not exist, it is created with `MkdirAll(0755)`. Failure to create or write to it returns an error; extraction does not fall back to another location. Relative paths are converted to absolute paths. Paths beginning with `~` are not expanded and are rejected.
- An existing target `<dir>/<library name>` is reused untouched if it is a regular file whose size and SHA-256 match the embedded library (its inode and mtime are preserved). A missing, truncated, modified, or symlink target is replaced. Replacement writes a temporary file in the same directory with mode `0755`, then renames it into place. Readers never see a partial file, and a symlink is replaced rather than followed. A directory at the target path is an error. Failed extraction leaves no temporary file. On every extraction, including when reusing an existing file, stale `*.tmp` files belonging to this library and older than one hour are cleaned up; other files are left alone.
- Ownership and permissions are not checked for a user-selected directory, so a shared install location (for example, next to an executable) is valid. The hash check protects against corruption, not a malicious process that can write to the directory. Choose a directory only you can write to.
- On Windows, a loaded DLL cannot be replaced. If its contents differ while it is in use, `rename` fails and returns an error explaining that a running process may have loaded it; extraction does not wait or delete the file. A matching file is reused even if it is loaded by this or another process.

**Default directory** (when no directory is specified and `LIBSDL_DIR` is unset):

- `os.TempDir()/libsdl-<uid>-<version>-<first 16 characters of sha256>/<library name>` (`<uid>` is `user` on Windows, where the temp directory is already per-user). The deterministic name lets repeated runs and concurrent processes reuse the same copy.
- The directory must be a real directory (symlinks are rejected) owned by the current user. If its permissions are too broad, they are changed to `0700`. An existing library file must be a regular file owned by the current user, not writable by other users, and match the expected size and hash; otherwise it is replaced. A directory owned by someone else causes an error. `os.UserCacheDir` is not used.
- The temporary directory is not cleaned up automatically. Each version leaves at most one directory of a few megabytes.

An embedded library whose data does not match its recorded hash causes an error.

### Switching libraries with puresdl `Load`

`sdl.Load(path)` with a non-empty path opens the new library, rebinds all functions, and updates `sdl.LoadedPath()` even if another library is already loaded. The same path is a no-op. If the path cannot be opened or the file is not SDL3 (it does not provide `SDL_GetVersion`), `Load` returns an error and the old library remains usable. Do not switch libraries concurrently with SDL calls.

## Platforms and sources

| Platform | Source | Notes |
|---|---|---|
| windows/amd64, windows/arm64 | `SDL3-<ver>-win32-{x64,arm64}.zip` from the **official release**, containing `SDL3.dll` | The zip is checked against the asset digest published by GitHub; the PE machine type is verified. The upstream DLL has no Authenticode signature and depends only on system DLLs. |
| darwin/amd64, darwin/arm64 | `SDL3-<ver>.dmg` from the **official release** (`hdiutil` mounts it, `lipo -thin` extracts the macOS slice from the xcframework, and `codesign --force -s -` applies an ad hoc signature) | Minimum OS: macOS 11.0 for arm64 and 10.13 for x86_64 (Go itself requires macOS 12 or later). An ad hoc signature is sufficient for `dlopen`; the arm64 slice must remain signed or the kernel will refuse to load it. |
| linux/amd64, linux/arm64 | **Built from source with zig** (the official project does not publish Linux binaries) | glibc minimum: **2.17**; SONAME: `libSDL3.so.0`. The only `NEEDED` libraries are libc, libm, libdl, and libpthread. All backends (X11, Wayland, KMSDRM, libdecor, ALSA, PulseAudio, PipeWire, JACK, sndio, udev, dbus, and others) are loaded with `dlopen` at runtime. The library is stripped and has no RPATH. |

Other platforms (`linux/386`, `js/wasm`, `android`, `ios`, `freebsd`, and others) can compile, but `Extract()` and `Path()` return errors matching `errors.ErrUnsupported`.

Linux build details are in `tools/linux/build.sh`. It uses zig 0.17 as a cross-compiler with `-target <arch>-linux-gnu.2.17`. Headers and `.pc` files come from `-dev` packages installed in a digest-pinned `debian:trixie-slim` Docker image (set `DEBIAN_SNAPSHOT` to freeze apt if desired). `wayland-scanner` and GNU strip also run in that image. SDL source is fetched from `archive/refs/tags/<tag>.tar.gz`; its SHA-256 is recorded in `MANIFEST.json`. Rebuilding with the same inputs produces byte-identical output. The library's `SDL_GetRevision()` is `SDL-release-<ver> (libsdl zig build)`, which distinguishes it from an official build.

Not verified: a real X11 or Wayland session, or audio devices (only the dummy driver and driver-list discovery have been tested).

## File layout

```text
libsdl.go                 Package docs, Version, Library/Info/Extract/Path
extract.go                Extraction and verification (atomic writes, concurrency-safe)
load/                     Nested module: Load(dir...) = Extract + puresdl sdl.Load
binary_<os>_<arch>.go     Six generated files; go:embed the matching assets/sdl_<os>_<arch>.gz
binary_unsupported.go     Placeholder for other platforms
assets/sdl_*.gz           Six compressed libraries
assets/MANIFEST.json      Source, SDL version, original/compressed SHA-256 and size for each library (no dates, reproducible)
LICENSE-SDL               SDL zlib license from the release
tools/update              Update program (Go)
tools/linux/build.sh      Zig build script for Linux libraries
internal/manifest         Manifest reading, writing, and validation (shared by tools/update and tests)
testdata/probe            Probe program for integration tests; depends on puresdl and is not part of this module's build
```

## Updating to a new version

Prerequisites for a full update:

- A macOS host (`hdiutil`, `lipo`, and `codesign`), zig 0.17, `cmake`, `ninja`, `docker`, `curl`/`tar`, and Go.
- For Windows-only updates, Go and network access are sufficient. `GITHUB_TOKEN` is optional and raises the API rate limit.

```sh
go run ./tools/update -latest                    # Pin the latest stable release (all six libraries)
go run ./tools/update -version release-3.4.18    # Or specify a version ("3.4.18" also works)
go run ./tools/update -newer                     # Print the tag if the latest release is newer (for CI)
go run ./tools/update -check                     # Validate assets and MANIFEST.json offline
```

Use `-os windows,darwin,linux` to update only selected platforms (CI runs macOS and Linux in separate jobs), or `-linux-dir <directory>` to use existing `SDL3_linux_{amd64,arm64}.so` files and `build-report.txt` without rebuilding. A partial update leaves mixed versions in the manifest, and `-check` reports an error until every platform is updated.

The updater rewrites `assets/*.gz`, `assets/MANIFEST.json`, `binary_*.go`, the `Version` in `libsdl.go`, and `LICENSE-SDL`. After updating, run `go test ./... -count=1` and commit the changes.

`.github/workflows/update.yml` checks for new versions weekly and on demand. The macOS job extracts Windows and macOS libraries; the Linux job builds with zig; a final job combines the results and opens a pull request.

## Verification

```sh
go run ./tools/update -check      # Offline: gzip/raw hashes, executable headers (ELF/Mach-O/PE and architecture), generated files, and Version synchronization
go test ./... -count=1            # Unit tests (root module; excludes load/)
(cd load && CGO_ENABLED=0 go test ./... -count=1)   # Nested module: load the real library and verify sdl.LoadedPath and version
# Integration test: load the embedded library with puresdl and compare SDL_GetVersion with libsdl.Version
LIBSDL_PURESDL_DIR=/path/to/puresdl go test . -run TestLoad -v
# Optional: run linux/amd64 and linux/arm64 in Docker, or darwin/amd64 under Rosetta on Apple Silicon
LIBSDL_DOCKER=1 LIBSDL_ROSETTA=1 LIBSDL_PURESDL_DIR=/path/to/puresdl go test . -run TestLoad -v
```

## Known limitations

- Windows libraries have only been checked statically (PE header and digest) in the author's environment; the Windows CI matrix runs integration tests.
- The result of re-signing macOS libraries depends on the `codesign` version, so regenerating the darwin assets may produce a different hash. Use `MANIFEST.json` as the source of truth.
- Go's gzip compression is about 3% to 5% less effective than `gzip -9`, in exchange for reproducibility and no external dependencies.
- Each SDL upgrade adds about 7 MB of binary data to git history.

## License

The libsdl project code is licensed under the MIT License; see [`LICENSE`](LICENSE). The embedded SDL libraries are licensed under the zlib License; see [`LICENSE-SDL`](LICENSE-SDL).
