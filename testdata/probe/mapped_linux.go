package main

import (
	"os"
	"strings"
)

func mappedFrom(path string) (bool, error) {
	b, err := os.ReadFile("/proc/self/maps")
	if err != nil {
		return false, err
	}
	return strings.Contains(string(b), path), nil
}
