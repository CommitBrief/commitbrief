// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/cache"
)

// pruneFlags collects the flag values for `cache prune`. They're
// declared at function scope rather than package level so that each
// invocation of the cobra command starts from clean defaults — which
// matters under `go test` where the cobra tree is reconstructed per
// test case.
type pruneFlags struct {
	keepLast  int
	olderThan string
	provider  string
	model     string
}

const (
	defaultKeepLast  = 500
	defaultOlderThan = "7d"
)

func newCachePruneCmd() *cobra.Command {
	var pf pruneFlags
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Drop old/excess cache entries (keep newest N + entries within age window)",
		Long: "Without flags, defaults to `--keep-last 500 --older-than 7d`. " +
			"Entries survive only when they sit inside BOTH windows: among the newest " +
			"keep-last AND not older than older-than. `--provider` / `--model` narrow " +
			"the candidate pool so the rules only touch entries matching those keys; " +
			"other providers' caches stay untouched.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := resolveContext(true)
			if err != nil {
				return err
			}
			olderThan, err := parseAgeDuration(pf.olderThan)
			if err != nil {
				return fmt.Errorf("cache prune: --older-than: %w", err)
			}
			if pf.keepLast < 0 {
				return fmt.Errorf("cache prune: --keep-last must be >= 0, got %d", pf.keepLast)
			}
			cacheDir := filepath.Join(app.RepoRoot, ".commitbrief", "cache")

			now := time.Now()
			result, err := pruneCacheDir(cacheDir, pruneCriteria{
				keepLast:  pf.keepLast,
				olderThan: olderThan,
				provider:  pf.provider,
				model:     pf.model,
				now:       now,
			})
			if err != nil {
				return fmt.Errorf("cache prune: %w", err)
			}

			w := cmd.OutOrStdout()
			_, _ = fmt.Fprintln(w, app.Catalog.T(
				"cache.prune.summary",
				result.removed, formatBytes(result.bytes), result.surviving))
			return nil
		},
	}
	f := cmd.Flags()
	f.IntVar(&pf.keepLast, "keep-last", defaultKeepLast,
		"keep only the N newest entries (per the provider/model filter); 0 means keep nothing")
	f.StringVar(&pf.olderThan, "older-than", defaultOlderThan,
		"delete entries older than this age. Units: d (day), w (week), m (month, 30d), y (year, 365d)")
	f.StringVar(&pf.provider, "provider", "",
		"only consider entries written for this provider (e.g. anthropic, openai, gemini, ollama)")
	f.StringVar(&pf.model, "model", "",
		"only consider entries written for this model (matched against the model recorded in each entry)")
	return cmd
}

// ageDurationRE matches our limited duration syntax `<int><unit>`
// where unit is one of d/w/m/y. We deliberately don't reuse Go's
// `time.ParseDuration` because it accepts h/m/s/ms but not d/w/m/y,
// and overloading `m` for both month and minute would be confusing.
var ageDurationRE = regexp.MustCompile(`^(\d+)([dwmy])$`)

// parseAgeDuration converts a string like "7d" / "2w" / "3m" / "1y"
// into a time.Duration. Used by `cache prune --older-than`.
//
// Calendar-aware durations are out of scope — we approximate:
//
//	d = 24h, w = 7d, m = 30d, y = 365d
//
// Cache TTL doesn't need calendar precision; close enough that a
// human asking "delete anything older than a month" gets what they
// expect. Errors out on any other format (h/m/s, bare integers,
// decimal counts) so silent off-by-one is impossible.
func parseAgeDuration(s string) (time.Duration, error) {
	m := ageDurationRE.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid duration %q (expected <int>[d|w|m|y], e.g. 7d, 2w, 3m, 1y)", s)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, fmt.Errorf("invalid duration count %q: %w", m[1], err)
	}
	day := 24 * time.Hour
	switch m[2] {
	case "d":
		return time.Duration(n) * day, nil
	case "w":
		return time.Duration(n) * 7 * day, nil
	case "m":
		return time.Duration(n) * 30 * day, nil
	case "y":
		return time.Duration(n) * 365 * day, nil
	}
	return 0, fmt.Errorf("invalid duration unit %q", m[2])
}

type pruneCriteria struct {
	keepLast  int
	olderThan time.Duration
	provider  string
	model     string
	now       time.Time
}

type pruneResult struct {
	removed   int
	surviving int
	bytes     int64
}

// pruneCacheDir is the pure I/O side of `cache prune`. Walks dir,
// decodes each entry, applies the provider/model narrowing, sorts
// the candidates newest-first, then deletes those that fall outside
// EITHER the keep-last window or the older-than age window. Files
// that don't match the provider/model filter (when set) are left
// untouched — see the subcommand Long help.
//
// Returns (removed count, surviving count, bytes freed) so the
// caller can format a single user-visible summary line.
func pruneCacheDir(dir string, c pruneCriteria) (pruneResult, error) {
	type candidate struct {
		path  string
		size  int64
		mtime time.Time
		entry cache.Entry
	}

	var (
		candidates []candidate
		untouched  int
		zero       pruneResult
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
		if filepath.Ext(path) != ".json" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var e cache.Entry
		if err := json.Unmarshal(raw, &e); err != nil {
			// Corrupt entries are also candidates for prune — but we
			// can't tell their provider/model. Skip them when filters
			// are active; otherwise delete on age grounds via mtime
			// fallback so the dir doesn't accumulate junk.
			if c.provider != "" || c.model != "" {
				untouched++
				return nil
			}
			info, ierr := d.Info()
			if ierr != nil {
				return ierr
			}
			candidates = append(candidates, candidate{
				path:  path,
				size:  info.Size(),
				mtime: info.ModTime(),
			})
			return nil
		}
		// Provider/model narrowing: out-of-scope entries are left as-is.
		if c.provider != "" && e.Key.Provider != c.provider {
			untouched++
			return nil
		}
		if c.model != "" && e.Key.Model != c.model {
			untouched++
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		// CreatedAt is what we want; fall back to mtime when the entry
		// somehow lost its timestamp.
		ts := e.CreatedAt
		if ts.IsZero() {
			ts = info.ModTime()
		}
		candidates = append(candidates, candidate{
			path:  path,
			size:  info.Size(),
			mtime: ts,
			entry: e,
		})
		return nil
	})
	if walkErr != nil {
		return zero, walkErr
	}

	// Newest first so index-based keep-last is easy.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].mtime.After(candidates[j].mtime)
	})

	var removed int
	var bytesFreed int64
	for i, cand := range candidates {
		age := c.now.Sub(cand.mtime)
		insideKeepLast := i < c.keepLast
		insideAgeWindow := age <= c.olderThan
		if insideKeepLast && insideAgeWindow {
			continue
		}
		if err := os.Remove(cand.path); err != nil {
			return zero, fmt.Errorf("remove %s: %w", cand.path, err)
		}
		removed++
		bytesFreed += cand.size
	}

	return pruneResult{
		removed:   removed,
		surviving: len(candidates) - removed + untouched,
		bytes:     bytesFreed,
	}, nil
}
