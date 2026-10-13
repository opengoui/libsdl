package manifest

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"fmt"
)

// CheckHeader verifies that data is a shared library for goos/goarch: an
// ELF shared object, a thin Mach-O dylib or a PE DLL with the right machine.
func CheckHeader(goos, goarch string, data []byte) error {
	r := bytes.NewReader(data)
	switch goos {
	case "linux":
		f, err := elf.NewFile(r)
		if err != nil {
			return fmt.Errorf("not an ELF file: %w", err)
		}
		defer f.Close()
		want := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[goarch]
		if f.Machine != want || f.Type != elf.ET_DYN || f.Class != elf.ELFCLASS64 {
			return fmt.Errorf("ELF %v %v %v, want 64-bit shared object for %v", f.Class, f.Type, f.Machine, want)
		}
	case "darwin":
		f, err := macho.NewFile(r) // fat binaries are rejected: slices must be thin
		if err != nil {
			return fmt.Errorf("not a thin Mach-O file: %w", err)
		}
		defer f.Close()
		want := map[string]macho.Cpu{"amd64": macho.CpuAmd64, "arm64": macho.CpuArm64}[goarch]
		if f.Cpu != want || f.Type != macho.TypeDylib {
			return fmt.Errorf("Mach-O %v %v, want dylib for %v", f.Type, f.Cpu, want)
		}
	case "windows":
		f, err := pe.NewFile(r)
		if err != nil {
			return fmt.Errorf("not a PE file: %w", err)
		}
		defer f.Close()
		want := map[string]uint16{"amd64": pe.IMAGE_FILE_MACHINE_AMD64, "arm64": pe.IMAGE_FILE_MACHINE_ARM64}[goarch]
		if f.Machine != want || f.Characteristics&pe.IMAGE_FILE_DLL == 0 {
			return fmt.Errorf("PE machine %#x characteristics %#x, want DLL %#x", f.Machine, f.Characteristics, want)
		}
	default:
		return fmt.Errorf("unsupported os %q", goos)
	}
	return nil
}
