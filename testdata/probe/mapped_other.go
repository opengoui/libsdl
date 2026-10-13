//go:build !linux && !darwin

package main

import "errors"

func mappedFrom(string) (bool, error) { return false, errors.New("not implemented on this OS") }
