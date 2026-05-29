// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/cache"
)

func newCacheStatsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stats",
		Short: "Show cache entry count, size, age range, and per-provider breakdown",
		Long: "Summarizes the repo-local response cache at <repoRoot>/.commitbrief/cache/: " +
			"total entries and bytes, the oldest/newest entry timestamps, the configured " +
			"size limit (cache.max_size_mb), and a per-provider/model breakdown. Read-only — " +
			"use `cache prune` / `cache clear` to reclaim space.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := resolveContext(true)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()

			cacheDir := filepath.Join(app.RepoRoot, ".commitbrief", "cache")
			st, err := collectCacheStats(cacheDir)
			if err != nil {
				return fmt.Errorf("cache stats: scan %s: %w", cacheDir, err)
			}

			if st.count == 0 {
				_, _ = fmt.Fprintln(w, app.Catalog.T("cache.stats.empty", cacheDir))
				return nil
			}

			_, _ = fmt.Fprintln(w, app.Catalog.T(
				"cache.stats.summary", st.count, formatBytes(st.bytes), cacheDir))
			_, _ = fmt.Fprintln(w, app.Catalog.T(
				"cache.stats.range",
				st.oldest.UTC().Format(time.RFC3339),
				st.newest.UTC().Format(time.RFC3339)))

			if mb := app.Config.Cache.MaxSizeMB; mb > 0 {
				_, _ = fmt.Fprintln(w, app.Catalog.T(
					"cache.stats.limit.bounded", formatBytes(int64(mb)*1024*1024), mb))
			} else {
				_, _ = fmt.Fprintln(w, app.Catalog.T("cache.stats.limit.unlimited"))
			}

			// Per-provider/model breakdown is debug-grade tabular output and
			// stays English, consistent with the dry-run / compress tables.
			rows := st.breakdownRows()
			if len(rows) > 0 {
				_, _ = fmt.Fprintln(w)
				_, _ = fmt.Fprintln(w, "By provider/model:")
				for _, r := range rows {
					_, _ = fmt.Fprintf(w, "  %-12s %-28s %4d  %s\n",
						r.provider, r.model, r.count, formatBytes(r.bytes))
				}
			}
			return nil
		},
	}
}

type cacheStatsResult struct {
	count  int
	bytes  int64
	oldest time.Time
	newest time.Time
	// keyed by "provider\x00model" so the two never collide for models
	// that share a name across providers.
	byKey map[string]*breakdownRow
}

type breakdownRow struct {
	provider string
	model    string
	count    int
	bytes    int64
}

// collectCacheStats walks the cache directory once, aggregating counts,
// byte totals, the created-at range, and a per-provider/model breakdown.
// Corrupt entries are still counted (under provider/model "?") and their
// mtime feeds the age range so the totals match what's on disk. A
// missing directory yields a zero-value cacheStats with no error.
func collectCacheStats(dir string) (cacheStatsResult, error) {
	st := cacheStatsResult{byKey: map[string]*breakdownRow{}}

	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if os.IsNotExist(werr) && path == dir {
				return filepath.SkipAll
			}
			return werr
		}
		if d.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}

		provider, model := "?", "?"
		ts := info.ModTime()
		if raw, rerr := os.ReadFile(path); rerr == nil {
			var e cache.Entry
			if json.Unmarshal(raw, &e) == nil {
				if e.Key.Provider != "" {
					provider = e.Key.Provider
				}
				if e.Key.Model != "" {
					model = e.Key.Model
				}
				if !e.CreatedAt.IsZero() {
					ts = e.CreatedAt
				}
			}
		}

		st.count++
		st.bytes += info.Size()
		if st.oldest.IsZero() || ts.Before(st.oldest) {
			st.oldest = ts
		}
		if ts.After(st.newest) {
			st.newest = ts
		}

		mapKey := provider + "\x00" + model
		row := st.byKey[mapKey]
		if row == nil {
			row = &breakdownRow{provider: provider, model: model}
			st.byKey[mapKey] = row
		}
		row.count++
		row.bytes += info.Size()
		return nil
	})
	if walkErr != nil {
		return cacheStatsResult{}, walkErr
	}
	return st, nil
}

// breakdownRows returns the per-provider/model rows sorted by entry count
// (descending), then provider, then model — a stable, deterministic order
// for the table and for tests.
func (s cacheStatsResult) breakdownRows() []breakdownRow {
	rows := make([]breakdownRow, 0, len(s.byKey))
	for _, r := range s.byKey {
		rows = append(rows, *r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].count != rows[j].count {
			return rows[i].count > rows[j].count
		}
		if rows[i].provider != rows[j].provider {
			return rows[i].provider < rows[j].provider
		}
		return rows[i].model < rows[j].model
	})
	return rows
}
