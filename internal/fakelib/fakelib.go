// Package fakelib builds minimal but structurally valid shared library
// headers (ELF, Mach-O, PE) for tests of the asset tooling.
package fakelib

import "encoding/binary"

// Make returns a tiny shared library image for goos/goarch whose header
// passes manifest.CheckHeader. The padding makes different tags differ.
func Make(goos, goarch, pad string) []byte {
	le := binary.LittleEndian
	var b []byte
	switch goos {
	case "linux":
		b = make([]byte, 64)
		copy(b, "\x7fELF")
		b[4], b[5], b[6] = 2, 1, 1 // 64-bit, little endian, version 1
		le.PutUint16(b[16:], 3)    // ET_DYN
		m := uint16(62)            // EM_X86_64
		if goarch == "arm64" {
			m = 183 // EM_AARCH64
		}
		le.PutUint16(b[18:], m)
		le.PutUint32(b[20:], 1)
		le.PutUint16(b[52:], 64) // e_ehsize
	case "darwin":
		b = make([]byte, 32)
		le.PutUint32(b, 0xfeedfacf)
		cpu := uint32(0x01000007) // x86_64
		if goarch == "arm64" {
			cpu = 0x0100000c
		}
		le.PutUint32(b[4:], cpu)
		le.PutUint32(b[12:], 6) // MH_DYLIB
	case "windows":
		b = make([]byte, 0x80+4+20) // debug/pe reads a 96 byte DOS header
		copy(b, "MZ")
		le.PutUint32(b[0x3c:], 0x80)
		copy(b[0x80:], "PE\x00\x00")
		m := uint16(0x8664)
		if goarch == "arm64" {
			m = 0xaa64
		}
		le.PutUint16(b[0x84:], m)
		le.PutUint16(b[0x84+18:], 0x2000) // IMAGE_FILE_DLL
	default:
		panic("fakelib: unsupported os " + goos)
	}
	return append(b, pad...)
}
