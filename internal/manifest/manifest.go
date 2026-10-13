// Package manifest describes the embedded SDL3 libraries (assets/MANIFEST.json)
// and verifies them. It is shared by tools/update and the tests.
package manifest

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"
)

// Target is one supported GOOS/GOARCH pair.
type Target struct {
	OS, Arch string
	// LibName is the file name the library is extracted as.
	LibName string
	// Tag is the //go:build expression selecting the platform.
	Tag string
}

// Targets lists every platform that embeds a library, in a stable order.
var Targets = []Target{
	{"windows", "amd64", "SDL3.dll", "windows && amd64"},
	{"windows", "arm64", "SDL3.dll", "windows && arm64"},
	{"darwin", "amd64", "libSDL3.dylib", "darwin && !ios && amd64"},
	{"darwin", "arm64", "libSDL3.dylib", "darwin && !ios && arm64"},
	{"linux", "amd64", "libSDL3.so.0", "linux && !android && amd64"},
	{"linux", "arm64", "libSDL3.so.0", "linux && !android && arm64"},
}

// Origin values of [File.Origin].
const (
	OriginOfficial = "official" // extracted from a libsdl-org release asset
	OriginZig      = "zig"      // built from source with zig
)

// ManifestPath is the manifest location relative to the module root.
const ManifestPath = "assets/MANIFEST.json"

// File describes one embedded library. It deliberately holds no dates so
// that regenerating an unchanged release yields an identical manifest.
type File struct {
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Asset   string `json:"asset"`   // path of the gzip file, relative to the module root
	LibName string `json:"libName"` // extraction file name
	Origin  string `json:"origin"`  // OriginOfficial or OriginZig
	// Source is the URL the library was taken from, or a description of how
	// it was built. SourceSHA256 is the hash of the downloaded archive
	// (zip, dmg or source tarball).
	Source       string `json:"source"`
	SourceSHA256 string `json:"sourceSha256,omitempty"`
	SDLVersion   string `json:"sdlVersion"`
	Size         int64  `json:"size"`   // decompressed library
	SHA256       string `json:"sha256"` // decompressed library
	GzSize       int64  `json:"gzSize"`
	GzSHA256     string `json:"gzSha256"`
	Notes        string `json:"notes,omitempty"`
}

// Manifest is the content of assets/MANIFEST.json.
type Manifest struct {
	// Tag is the upstream release tag, for example "release-3.4.18".
	Tag string `json:"tag"`
	// Version is Tag without the "release-" prefix.
	Version string `json:"version"`
	Files   []File `json:"files"`
}

// Load reads the manifest below root.
func Load(root string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(root, ManifestPath))
	if err != nil {
		return nil, err
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", ManifestPath, err)
	}
	return &m, nil
}

// Save writes the manifest below root, sorted in [Targets] order.
func (m *Manifest) Save(root string) error {
	order := map[string]int{}
	for i, t := range Targets {
		order[t.OS+"/"+t.Arch] = i
	}
	sort.SliceStable(m.Files, func(i, j int) bool {
		return order[m.Files[i].OS+"/"+m.Files[i].Arch] < order[m.Files[j].OS+"/"+m.Files[j].Arch]
	})
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, ManifestPath), append(b, '\n'), 0o644)
}

// Find returns the entry for a platform.
func (m *Manifest) Find(goos, goarch string) *File {
	for i := range m.Files {
		if m.Files[i].OS == goos && m.Files[i].Arch == goarch {
			return &m.Files[i]
		}
	}
	return nil
}

// Set replaces or adds an entry.
func (m *Manifest) Set(f File) {
	if old := m.Find(f.OS, f.Arch); old != nil {
		*old = f
		return
	}
	m.Files = append(m.Files, f)
}

// VersionOfTag strips the "release-" prefix.
func VersionOfTag(tag string) string { return strings.TrimPrefix(tag, "release-") }

// SHA256Hex returns the lowercase hex SHA-256 of b.
func SHA256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// Gzip compresses data reproducibly (no name, no modification time).
func Gzip(data []byte) []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zw.Write(data)
	zw.Close()
	return buf.Bytes()
}

// Gunzip decompresses a gzip blob.
func Gunzip(b []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

// NewFile compresses the raw library, writes the gzip asset below root and
// returns the manifest entry (without Origin/Source fields).
func NewFile(root string, t Target, version string, raw []byte) (File, error) {
	if err := CheckHeader(t.OS, t.Arch, raw); err != nil {
		return File{}, err
	}
	gz := Gzip(raw)
	f := File{
		OS: t.OS, Arch: t.Arch, LibName: t.LibName, SDLVersion: version,
		Asset: AssetPath(t),
		Size:  int64(len(raw)), SHA256: SHA256Hex(raw),
		GzSize: int64(len(gz)), GzSHA256: SHA256Hex(gz),
	}
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		return File{}, err
	}
	return f, os.WriteFile(filepath.Join(root, f.Asset), gz, 0o644)
}

// AssetPath is the module-relative gzip asset path of a target.
func AssetPath(t Target) string { return "assets/sdl_" + t.OS + "_" + t.Arch + ".gz" }

// BinaryFileName is the generated Go file of a target.
func BinaryFileName(t Target) string { return "binary_" + t.OS + "_" + t.Arch + ".go" }

var binaryTmpl = template.Must(template.New("b").Parse(`// Code generated by tools/update; DO NOT EDIT.

//go:build {{.Tag}}

package libsdl

import _ "embed"

const (
	supported = true
	libName   = {{printf "%q" .LibName}}
	libSize   = {{.Size}}
	libSHA256 = {{printf "%q" .SHA256}}
)

//go:embed assets/sdl_{{.OS}}_{{.Arch}}.gz
var blob []byte
`))

// RenderBinary renders binary_<os>_<arch>.go for an entry.
func RenderBinary(t Target, f File) []byte {
	var b bytes.Buffer
	err := binaryTmpl.Execute(&b, map[string]any{
		"Tag": t.Tag, "OS": t.OS, "Arch": t.Arch,
		"LibName": f.LibName, "Size": f.Size, "SHA256": f.SHA256,
	})
	if err != nil {
		panic(err)
	}
	return b.Bytes()
}

var versionRe = regexp.MustCompile(`(?m)^const Version = "([^"]*)"$`)

// ReadVersion extracts the Version constant from libsdl.go source.
func ReadVersion(src []byte) (string, error) {
	m := versionRe.FindSubmatch(src)
	if m == nil {
		return "", errors.New("libsdl.go: const Version not found")
	}
	return string(m[1]), nil
}

// SetVersion rewrites the Version constant in libsdl.go source.
func SetVersion(src []byte, version string) ([]byte, error) {
	if _, err := ReadVersion(src); err != nil {
		return nil, err
	}
	return versionRe.ReplaceAll(src, []byte(`const Version = "`+version+`"`)), nil
}

// WriteGenerated writes the binary_*.go files of all manifest entries and
// the Version constant of libsdl.go below root.
func (m *Manifest) WriteGenerated(root string) error {
	for _, f := range m.Files {
		t, ok := targetOf(f.OS, f.Arch)
		if !ok {
			return fmt.Errorf("manifest: unknown target %s/%s", f.OS, f.Arch)
		}
		if err := os.WriteFile(filepath.Join(root, BinaryFileName(t)), RenderBinary(t, f), 0o644); err != nil {
			return err
		}
	}
	p := filepath.Join(root, "libsdl.go")
	src, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	out, err := SetVersion(src, m.Version)
	if err != nil {
		return err
	}
	return os.WriteFile(p, out, 0o644)
}

func targetOf(goos, goarch string) (Target, bool) {
	for _, t := range Targets {
		if t.OS == goos && t.Arch == goarch {
			return t, true
		}
	}
	return Target{}, false
}

// Check verifies the module below root without network access: the
// manifest is complete and consistent, every gzip asset matches its recorded
// hashes, decompresses to a library with the right executable header, the
// generated binary_*.go files match the manifest and libsdl.go carries the
// manifest version. All problems are reported together.
func Check(root string) error {
	m, err := Load(root)
	if err != nil {
		return err
	}
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if m.Tag == "" || m.Version != VersionOfTag(m.Tag) {
		bad("manifest: tag %q and version %q disagree", m.Tag, m.Version)
	}
	src, err := os.ReadFile(filepath.Join(root, "libsdl.go"))
	if err != nil {
		return err
	}
	if v, err := ReadVersion(src); err != nil {
		bad("%v", err)
	} else if v != m.Version {
		bad("libsdl.go Version %q != manifest version %q", v, m.Version)
	}
	if len(m.Files) != len(Targets) {
		bad("manifest has %d files, want %d", len(m.Files), len(Targets))
	}
	for _, t := range Targets {
		f := m.Find(t.OS, t.Arch)
		name := t.OS + "/" + t.Arch
		if f == nil {
			bad("%s: missing from manifest", name)
			continue
		}
		if f.SDLVersion != m.Version {
			bad("%s: sdlVersion %q != %q", name, f.SDLVersion, m.Version)
		}
		if f.Asset != AssetPath(t) || f.LibName != t.LibName {
			bad("%s: asset %q / libName %q unexpected", name, f.Asset, f.LibName)
		}
		if f.Origin != OriginOfficial && f.Origin != OriginZig {
			bad("%s: origin %q", name, f.Origin)
		}
		if f.Source == "" {
			bad("%s: empty source", name)
		}
		gz, err := os.ReadFile(filepath.Join(root, AssetPath(t)))
		if err != nil {
			bad("%s: %v", name, err)
			continue
		}
		if int64(len(gz)) != f.GzSize || SHA256Hex(gz) != f.GzSHA256 {
			bad("%s: gzip asset does not match manifest (size %d, sha256 %s)", name, len(gz), SHA256Hex(gz))
		}
		raw, err := Gunzip(gz)
		if err != nil {
			bad("%s: gunzip: %v", name, err)
			continue
		}
		if int64(len(raw)) != f.Size || SHA256Hex(raw) != f.SHA256 {
			bad("%s: library does not match manifest (size %d, sha256 %s)", name, len(raw), SHA256Hex(raw))
		}
		if err := CheckHeader(t.OS, t.Arch, raw); err != nil {
			bad("%s: %v", name, err)
		}
		have, err := os.ReadFile(filepath.Join(root, BinaryFileName(t)))
		if err != nil {
			bad("%s: %v", name, err)
		} else if !bytes.Equal(have, RenderBinary(t, *f)) {
			bad("%s: %s is not in sync with the manifest (run tools/update)", name, BinaryFileName(t))
		}
	}
	return errors.Join(errs...)
}

// tagPattern matches the release tags of libsdl-org/SDL that are accepted.
var tagPattern = regexp.MustCompile(`^release-[0-9]+\.[0-9]+\.[0-9]+$`)

// ValidTag reports whether tag has the form release-X.Y.Z.
func ValidTag(tag string) bool { return tagPattern.MatchString(tag) }
