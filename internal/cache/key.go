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
	return hex.EncodeToString(h.Sum(nil))
}
