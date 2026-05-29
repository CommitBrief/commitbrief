// SPDX-License-Identifier: GPL-3.0-or-later

package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

const SchemaVersion = 1

type ComputeArgs struct {
	Diff         string
	SystemPrompt string
	Provider     string
	Model        string
	Lang         string

	// WithContext marks a --with-context run (ADR-0017). A context run and
	// a diff-only run on the same diff must not alias, so when true a
	// marker is folded into the key. When false NOTHING extra is written,
	// keeping diff-only keys byte-identical to pre-ADR-0017 entries — no
	// mass cache invalidation on upgrade.
	WithContext bool
}

// Compute returns the deterministic SHA-256 key (lowercase hex) for the
// cache lookup. The schema version is folded in so a future bump
// invalidates every existing entry without touching disk.
func Compute(args ComputeArgs) string {
	h := sha256.New()
	h.Write([]byte(args.Diff))
	h.Write([]byte("::"))
	h.Write([]byte(args.SystemPrompt))
	h.Write([]byte("::"))
	h.Write([]byte(args.Provider))
	h.Write([]byte(":"))
	h.Write([]byte(args.Model))
	h.Write([]byte(":"))
	h.Write([]byte(args.Lang))
	h.Write([]byte(":"))
	h.Write([]byte(strconv.Itoa(SchemaVersion)))
	// Append the context marker only when set, so non-context keys are
	// unchanged from before ADR-0017 (see WithContext doc).
	if args.WithContext {
		h.Write([]byte(":ctx"))
	}
	return hex.EncodeToString(h.Sum(nil))
}
