// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"fmt"
	"strings"
)

// ChecksumsFile is the name goreleaser gives the checksum manifest
// attached to every release (.goreleaser.yaml → checksum.name_template).
const ChecksumsFile = "checksums.txt"

// AssetName mirrors the archive name_template in .goreleaser.yaml:
//
//	{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ arch }}
//
// where .Version carries no leading "v", amd64 renders as x86_64, 386 as
// i386, and Windows archives are .zip while everything else is .tar.gz.
// If that template ever changes, this function changes with it.
func AssetName(version, goos, goarch string) string {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	arch := goarch
	switch goarch {
	case "amd64":
		arch = "x86_64"
	case "386":
		arch = "i386"
	}
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("commitbrief_%s_%s_%s%s", v, goos, arch, ext)
}

// BinaryEntryName is the archive entry holding the executable itself.
// goreleaser places it at the archive root next to LICENSE/README.
func BinaryEntryName(goos string) string {
	if goos == "windows" {
		return "commitbrief.exe"
	}
	return "commitbrief"
}

// ParseChecksums reads a "<sha256>  <filename>" manifest into a
// filename → hex-sum map. A leading '*' on the filename marks binary
// mode in the sha256sum format and is not part of the name.
func ParseChecksums(data []byte) map[string]string {
	sums := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		sums[strings.TrimPrefix(fields[1], "*")] = fields[0]
	}
	return sums
}
