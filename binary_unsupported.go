//go:build !((windows || (darwin && !ios) || (linux && !android)) && (amd64 || arm64))

package libsdl

const (
	supported = false
	libName   = ""
	libSize   = 0
	libSHA256 = "0000000000000000"
)

var blob []byte
