// Command update refreshes the embedded SDL3 libraries from an official
// libsdl-org/SDL release.
//
// Usage, from the module root:
//
//	go run ./tools/update -latest                  # newest release, all platforms
//	go run ./tools/update -version release-3.4.18  # a specific release
//	go run ./tools/update -newer                   # print the latest tag if newer than the pinned one
//	go run ./tools/update -check                   # offline verification against assets/MANIFEST.json
//
// Windows and macOS libraries are extracted from the official release assets
// (digest verified against the value GitHub publishes). The macOS step needs
// hdiutil, lipo and codesign and therefore a macOS host. Linux libraries are
// built from source with zig by tools/linux/build.sh (needs zig, cmake, ninja
// and docker) or taken from a previous build with -linux-dir.
//
// -os restricts the platforms (windows, darwin, linux) so that CI can split
// the work over runners. The manifest, the gzip assets, binary_*.go and the
// Version constant are rewritten; a partial update leaves a mixed-version
// manifest that -check rejects until the remaining platforms are updated.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opengoui/libsdl/internal/manifest"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "update:", err)
		os.Exit(1)
	}
}

type options struct {
	root, version, osList, linuxDir, work string
	latest, check, newer                  bool
}

func run(args []string) error {
	var o options
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.StringVar(&o.root, "root", ".", "module root")
	fs.StringVar(&o.version, "version", "", `release to pin, "3.4.18" or "release-3.4.18"`)
	fs.BoolVar(&o.latest, "latest", false, "use the latest release")
	fs.BoolVar(&o.check, "check", false, "verify assets against MANIFEST.json offline and exit")
	fs.BoolVar(&o.newer, "newer", false, "print the latest release tag if it is newer than the pinned one")
	fs.StringVar(&o.osList, "os", "windows,darwin,linux", "comma separated platforms to update")
	fs.StringVar(&o.linuxDir, "linux-dir", "", "use SDL3_linux_{amd64,arm64}.so from this directory instead of building")
	fs.StringVar(&o.work, "work", "", "scratch directory (default <root>/.work)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if o.work == "" {
		o.work = filepath.Join(o.root, ".work")
	}
	switch {
	case o.check:
		if err := manifest.Check(o.root); err != nil {
			return err
		}
		fmt.Println("ok: assets match", manifest.ManifestPath)
		return nil
	case o.newer:
		return printNewer(o)
	}
	return update(o)
}

func printNewer(o options) error {
	tag, err := latestTag()
	if err != nil {
		return err
	}
	m, err := manifest.Load(o.root)
	if err != nil {
		return err
	}
	if newerVersion(manifest.VersionOfTag(tag), m.Version) {
		fmt.Println(tag)
	}
	return nil
}

func update(o options) error {
	tag := o.version
	switch {
	case o.latest == (tag != ""):
		return errors.New("give exactly one of -version and -latest")
	case o.latest:
		var err error
		if tag, err = latestTag(); err != nil {
			return err
		}
	}
	tag = "release-" + manifest.VersionOfTag(tag)
	if !manifest.ValidTag(tag) {
		return fmt.Errorf("invalid release tag %q (want release-X.Y.Z)", tag)
	}
	version := manifest.VersionOfTag(tag)
	fmt.Println("pinning", tag)

	platforms := map[string]bool{}
	for _, p := range strings.Split(o.osList, ",") {
		p = strings.TrimSpace(p)
		if p != "windows" && p != "darwin" && p != "linux" {
			return fmt.Errorf("unknown -os entry %q", p)
		}
		platforms[p] = true
	}
	rel, err := fetchRelease(tag)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(o.work, 0o755); err != nil {
		return err
	}

	m, err := manifest.Load(o.root)
	if errors.Is(err, os.ErrNotExist) {
		m, err = &manifest.Manifest{}, nil
	}
	if err != nil {
		return err
	}
	m.Tag, m.Version = tag, version

	var files []manifest.File
	if platforms["windows"] {
		fs, err := windowsLibs(o, rel, version)
		if err != nil {
			return fmt.Errorf("windows: %w", err)
		}
		files = append(files, fs...)
	}
	if platforms["darwin"] {
		fs, err := darwinLibs(o, rel, version)
		if err != nil {
			return fmt.Errorf("darwin: %w", err)
		}
		files = append(files, fs...)
	}
	if platforms["linux"] {
		fs, err := linuxLibs(o, tag, version)
		if err != nil {
			return fmt.Errorf("linux: %w", err)
		}
		files = append(files, fs...)
	}
	for _, f := range files {
		m.Set(f)
	}
	if err := m.Save(o.root); err != nil {
		return err
	}
	if err := m.WriteGenerated(o.root); err != nil {
		return err
	}
	if err := writeLicense(o, rel, tag); err != nil {
		return err
	}
	for _, f := range m.Files {
		if f.SDLVersion != version {
			fmt.Printf("warning: %s/%s is still SDL %s; update the remaining platforms\n", f.OS, f.Arch, f.SDLVersion)
		}
	}
	for _, f := range files {
		fmt.Printf("%s/%s  %d bytes  gz %d  sha256 %s\n", f.OS, f.Arch, f.Size, f.GzSize, f.SHA256)
	}
	return nil
}

func targetsOf(goos string) []manifest.Target {
	var ts []manifest.Target
	for _, t := range manifest.Targets {
		if t.OS == goos {
			ts = append(ts, t)
		}
	}
	return ts
}
