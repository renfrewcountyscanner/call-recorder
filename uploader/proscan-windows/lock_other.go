//go:build !windows

package main

import "os"

func openExclusive(path string) (*os.File, bool, error) {
	file, err := os.Open(path)
	return file, err == nil, err
}
