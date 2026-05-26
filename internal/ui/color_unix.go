//go:build !windows

package ui

import "os"

func enableANSI(_ *os.File) error { return nil }
