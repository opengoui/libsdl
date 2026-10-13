package main

import "github.com/ebitengine/purego"

func mappedFrom(path string) (bool, error) {
	h, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW)
	if err != nil {
		return false, err
	}
	var count func() uint32
	var name func(uint32) string
	purego.RegisterLibFunc(&count, h, "_dyld_image_count")
	purego.RegisterLibFunc(&name, h, "_dyld_get_image_name")
	for i := uint32(0); i < count(); i++ {
		if name(i) == path {
			return true, nil
		}
	}
	return false, nil
}
