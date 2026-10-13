package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/opengoui/libsdl/internal/manifest"
)

// windowsLibs extracts SDL3.dll from the official win32-x64 / win32-arm64 zips.
func windowsLibs(o options, rel *release, version string) ([]manifest.File, error) {
	zipArch := map[string]string{"amd64": "x64", "arm64": "arm64"}
	var out []manifest.File
	for _, t := range targetsOf("windows") {
		name := fmt.Sprintf("SDL3-%s-win32-%s.zip", version, zipArch[t.Arch])
		data, url, sum, err := rel.download(rel.Tag, name)
		if err != nil {
			return nil, err
		}
		raw, err := zipMember(data, "SDL3.dll")
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		f, err := manifest.NewFile(o.root, t, version, raw)
		if err != nil {
			return nil, err
		}
		f.Origin, f.Source, f.SourceSHA256 = manifest.OriginOfficial, url, sum
		f.Notes = "SDL3.dll as shipped in the release zip (not Authenticode signed upstream)"
		out = append(out, f)
	}
	return out, nil
}

func zipMember(data []byte, name string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("%s not found", name)
}

const frameworkBinary = "SDL3.xcframework/macos-arm64_x86_64/SDL3.framework/Versions/A/SDL3"

// darwinLibs mounts the official dmg, thins the framework binary into one
// dylib per architecture and re-signs each slice ad hoc.
func darwinLibs(o options, rel *release, version string) ([]manifest.File, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("needs hdiutil, lipo and codesign: run on macOS (host is %s)", runtime.GOOS)
	}
	name := fmt.Sprintf("SDL3-%s.dmg", version)
	data, url, sum, err := rel.download(rel.Tag, name)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(o.work, "dmg-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	dmg, mnt := filepath.Join(dir, name), filepath.Join(dir, "mnt")
	if err := os.WriteFile(dmg, data, 0o644); err != nil {
		return nil, err
	}
	if err := os.Mkdir(mnt, 0o755); err != nil {
		return nil, err
	}
	if out, err := exec.Command("hdiutil", "attach", "-nobrowse", "-readonly", "-mountpoint", mnt, dmg).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("hdiutil attach: %v\n%s", err, out)
	}
	defer func() {
		if exec.Command("hdiutil", "detach", mnt, "-quiet").Run() != nil {
			exec.Command("hdiutil", "detach", "-force", mnt, "-quiet").Run()
		}
	}()
	fw := filepath.Join(mnt, frameworkBinary)
	if _, err := os.Stat(fw); err != nil {
		return nil, fmt.Errorf("framework binary missing in dmg: %w", err)
	}
	var out []manifest.File
	for _, t := range targetsOf("darwin") {
		slice := filepath.Join(dir, "SDL3_"+t.Arch+".dylib")
		lipoArch := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[t.Arch]
		if b, err := exec.Command("lipo", "-thin", lipoArch, fw, "-output", slice).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("lipo: %v\n%s", err, b)
		}
		// Unsigned arm64 code is refused by the kernel; ad hoc signing also
		// makes the page hashes valid for a standalone dylib.
		if b, err := exec.Command("codesign", "--force", "-s", "-", slice).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("codesign: %v\n%s", err, b)
		}
		raw, err := os.ReadFile(slice)
		if err != nil {
			return nil, err
		}
		f, err := manifest.NewFile(o.root, t, version, raw)
		if err != nil {
			return nil, err
		}
		f.Origin, f.Source, f.SourceSHA256 = manifest.OriginOfficial, url, sum
		f.Notes = "lipo -thin " + lipoArch + " of " + frameworkBinary + ", re-signed ad hoc (codesign --force -s -)"
		out = append(out, f)
	}
	return out, nil
}

var srcShaRe = regexp.MustCompile(`source tarball sha256 ([0-9a-f]{64})`)

// linuxLibs reads libraries built by tools/linux/build.sh, running the
// script first unless -linux-dir names a previous output directory.
func linuxLibs(o options, tag, version string) ([]manifest.File, error) {
	dir := o.linuxDir
	if dir == "" {
		dir = filepath.Join(o.work, "linux-out")
		sh := filepath.Join(o.root, "tools", "linux", "build.sh")
		cmd := exec.Command("bash", sh, tag, dir)
		cmd.Env = append(os.Environ(), "WORK="+filepath.Join(o.work, "linux-build"))
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("build.sh: %w", err)
		}
	}
	srcSum := ""
	if rep, err := os.ReadFile(filepath.Join(dir, "build-report.txt")); err == nil {
		if m := srcShaRe.FindSubmatch(rep); m != nil {
			srcSum = string(m[1])
		}
		if !strings.Contains(string(rep), "SDL "+tag+" ") {
			return nil, fmt.Errorf("%s/build-report.txt was not produced for %s", dir, tag)
		}
	}
	var out []manifest.File
	for _, t := range targetsOf("linux") {
		raw, err := os.ReadFile(filepath.Join(dir, "SDL3_linux_"+t.Arch+".so"))
		if err != nil {
			return nil, err
		}
		f, err := manifest.NewFile(o.root, t, version, raw)
		if err != nil {
			return nil, err
		}
		f.Origin = manifest.OriginZig
		f.Source = "built with zig from https://github.com/libsdl-org/SDL/archive/refs/tags/" + tag + ".tar.gz by tools/linux/build.sh"
		f.SourceSHA256 = srcSum
		f.Notes = "glibc 2.17 baseline, SONAME libSDL3.so.0, all backends dlopen'd, stripped"
		out = append(out, f)
	}
	return out, nil
}

// writeLicense stores the zlib license text of the release as LICENSE-SDL,
// taking it from the win32-x64 zip when that is available.
func writeLicense(o options, rel *release, tag string) error {
	version := manifest.VersionOfTag(tag)
	name := fmt.Sprintf("SDL3-%s-win32-x64.zip", version)
	data, _, _, err := rel.download(tag, name)
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, "LICENSE.txt") {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()
			b, err := io.ReadAll(rc)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(o.root, "LICENSE-SDL"), b, 0o644)
		}
	}
	return fmt.Errorf("%s: LICENSE.txt not found", name)
}
