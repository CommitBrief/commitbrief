// SPDX-License-Identifier: GPL-3.0-or-later

package leaks

import "bytes"

// DefaultMaxFileBytes caps how large a file may be before the scanner passes
// over it. The patterns all target short, structured tokens; a file this size
// is a database dump or a bundled asset, and running eight regexes over every
// line of it costs far more than it can plausibly find.
const DefaultMaxFileBytes = 5 << 20 // 5 MiB

// binarySniffBytes is how much of a file is inspected for the NUL byte that
// marks it as non-text. This is the same heuristic git itself uses, and 8 KiB
// is enough to catch every real binary format's header.
const binarySniffBytes = 8 << 10

// isBinary reports whether content looks like a binary blob.
//
// The repo had no content-based binary detection before this: diff.FileDiff's
// Binary flag is parsed out of git's own "Binary files …" sentinel, which is
// unavailable when reading a file straight off disk. A NUL byte in the first
// 8 KiB is the standard, cheap test — valid UTF-8 text never contains one.
func isBinary(content []byte) bool {
	head := content
	if len(head) > binarySniffBytes {
		head = head[:binarySniffBytes]
	}
	return bytes.IndexByte(head, 0) >= 0
}

// maxFileBytes resolves the effective size cap.
func maxFileBytes(opts Options) int64 {
	if opts.MaxFileBytes > 0 {
		return opts.MaxFileBytes
	}
	return DefaultMaxFileBytes
}
