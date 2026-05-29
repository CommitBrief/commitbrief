// SPDX-License-Identifier: GPL-3.0-or-later

package cache

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// enforceSizeLimit caps the on-disk cache directory at maxBytes by
// removing entries oldest-first until the summed size fits. "Oldest" is
// each entry's CreatedAt, falling back to the file mtime when the entry
// is corrupt or pre-dates the timestamp field. It is the automatic
// counterpart to `cache prune`: cheap, no access-time tracking, and
// deliberately conservative.
//
// keep is the base filename (e.g. "<sha>.json") of an entry that must
// never be evicted — callers pass the just-written entry so a fresh Put
// can never delete its own result. Its bytes still count toward the
// total, so when a single entry alone exceeds maxBytes every other entry
// is evicted and that one survives over-budget (the alternative — wiping
// the result the user just paid for — is worse).
//
// maxBytes <= 0 is treated as "no limit" and returns immediately. A
// missing directory is not an error. Returns (entries removed, bytes
// freed, error); remove failures stop the sweep and return what was
// freed so far.
func enforceSizeLimit(dir string, maxBytes int64, keep string) (removed int, freed int64, err error) {
	if maxBytes <= 0 {
		return 0, 0, nil
	}

	type candidate struct {
		path      string
		size      int64
		createdAt time.Time
		protected bool
	}

	var (
		candidates []candidate
		total      int64
	)

	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if os.IsNotExist(werr) && path == dir {
				return filepath.SkipAll
			}
			return werr
		}
		if d.IsDir() {
			return nil
		}
		// Only count finished entries. In-flight temp files (writeAtomic)
		// and any non-entry files are ignored so a concurrent Put isn't
		// double-counted or deleted mid-rename.
		if filepath.Ext(path) != ".json" {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		ts := info.ModTime()
		if raw, rerr := os.ReadFile(path); rerr == nil {
			var e Entry
			if json.Unmarshal(raw, &e) == nil && !e.CreatedAt.IsZero() {
				ts = e.CreatedAt
			}
		}
		total += info.Size()
		candidates = append(candidates, candidate{
			path:      path,
			size:      info.Size(),
			createdAt: ts,
			protected: filepath.Base(path) == keep,
		})
		return nil
	})
	if walkErr != nil {
		return 0, 0, walkErr
	}

	if total <= maxBytes {
		return 0, 0, nil
	}

	// Oldest first; protected entry sinks to the end so it's only removed
	// if it were the last candidate — which the loop below never does.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].protected != candidates[j].protected {
			return !candidates[i].protected
		}
		return candidates[i].createdAt.Before(candidates[j].createdAt)
	})

	for _, cand := range candidates {
		if total <= maxBytes {
			break
		}
		if cand.protected {
			// Reached the protected entry with the total still over
			// budget: nothing more we may delete. Stop.
			break
		}
		if rerr := os.Remove(cand.path); rerr != nil {
			return removed, freed, rerr
		}
		removed++
		freed += cand.size
		total -= cand.size
	}

	return removed, freed, nil
}
