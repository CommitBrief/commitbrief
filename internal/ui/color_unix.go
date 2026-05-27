// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package ui

import "os"

func enableANSI(_ *os.File) error { return nil }
