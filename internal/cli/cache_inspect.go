// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/cache"
)

func newCacheInspectCmd() *cobra.Command {
	var showContent bool
	cmd := &cobra.Command{
		Use:   "inspect <key>",
		Short: "Show metadata for a single cache entry by key",
		Long: "Dumps one cached entry's metadata (provider, model, language, timestamps, " +
			"freshness, token counts, on-disk size) given its cache key. The key is the " +
			"SHA-256 shown by `--verbose` / `dry-run` (the .json suffix is optional). The " +
			"cached review body is omitted unless --show-content is passed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := resolveContext(true)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()

			// Accept the key with or without the on-disk .json suffix.
			key := strings.TrimSuffix(args[0], ".json")
			cacheDir := filepath.Join(app.RepoRoot, ".commitbrief", "cache")
			path := filepath.Join(cacheDir, key+".json")

			raw, err := os.ReadFile(path)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					_, _ = fmt.Fprintln(w, app.Catalog.T("cache.inspect.notfound", key, cacheDir))
					return nil
				}
				return fmt.Errorf("cache inspect: read %s: %w", path, err)
			}

			var e cache.Entry
			if err := json.Unmarshal(raw, &e); err != nil {
				return fmt.Errorf("cache inspect: entry %s is corrupt: %w", key, err)
			}

			// Metadata dump is debug-grade tabular output and stays English,
			// consistent with the dry-run / compress tables and `--verbose`.
			now := time.Now()
			fresh := "fresh"
			if e.ExpiredAt(now) {
				fresh = "expired"
			}
			format := e.Result.Format
			if format == "" {
				format = cache.FormatJSON // empty == json, per ADR-0008 §4
			}

			_, _ = fmt.Fprintf(w, "Key:       %s\n", key)
			_, _ = fmt.Fprintf(w, "Provider:  %s\n", orDash(e.Key.Provider))
			_, _ = fmt.Fprintf(w, "Model:     %s\n", orDash(e.Key.Model))
			_, _ = fmt.Fprintf(w, "Lang:      %s\n", orDash(e.Key.Lang))
			_, _ = fmt.Fprintf(w, "Created:   %s\n", e.CreatedAt.UTC().Format(time.RFC3339))
			if e.TTL > 0 {
				expiry := e.CreatedAt.Add(time.Duration(e.TTL) * time.Second).UTC()
				_, _ = fmt.Fprintf(w, "TTL:       %ds (expires %s, %s)\n",
					e.TTL, expiry.Format(time.RFC3339), fresh)
			} else {
				_, _ = fmt.Fprintf(w, "TTL:       0 (never expires)\n")
			}
			_, _ = fmt.Fprintf(w, "Format:    %s\n", format)
			_, _ = fmt.Fprintf(w, "Size:      %s\n", formatBytes(int64(len(raw))))
			_, _ = fmt.Fprintf(w, "Tokens:    input=%d output=%d cached=%d\n",
				e.Result.Tokens.Input, e.Result.Tokens.Output, e.Result.Tokens.Cached)
			_, _ = fmt.Fprintf(w, "Diff hash: %s\n", orDash(e.Key.DiffHash))

			if showContent {
				_, _ = fmt.Fprintln(w, "\n--- content ---")
				_, _ = fmt.Fprintln(w, e.Result.Content)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&showContent, "show-content", false,
		"also print the cached review body (omitted by default)")
	return cmd
}

// orDash renders an empty metadata field as a dash so the column lines
// up and a missing value is visually obvious rather than blank.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
