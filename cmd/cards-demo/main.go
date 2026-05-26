package main

import (
	"os"
	"time"
	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/provider"
)
func main() {
	p := render.Payload{
		Findings: []render.Finding{
			{Severity: render.SeverityCritical, File: "internal/auth/session.go", Line: 142,
				Title: "SQL fragment built from request input",
				Description: "String concatenation feeds db.Query() directly, bypassing the prepared statement path used elsewhere in this package.",
				Language: "go",
				Snippet: "  func validateToken(tok string) error {\n- q := \"SELECT * FROM sessions WHERE token = '\" + tok + \"'\"\n+ q := \"SELECT * FROM sessions WHERE token = $1\"\n  rows, err := db.Query(ctx, q, tok)\n  if err != nil {"},
			{Severity: render.SeverityHigh, File: "internal/db/migrate.go", Line: 73,
				Title: "NOT NULL column added without default",
				Description: "Migration will fail on any populated table."},
			{Severity: render.SeverityMedium, File: "internal/api/handler.go", Line: 201,
				Title: "Race on shared map access",
				Description: "Concurrent goroutines mutate this map without a mutex."},
			{Severity: render.SeverityLow, File: "internal/util/log.go", Line: 12,
				Title: "Magic number 30 should be a constant",
				Description: "Used in two callers; promote for clarity."},
			{Severity: render.SeverityInfo, File: "internal/cli/root.go", Line: 7,
				Title: "Unused import",
				Description: "context is no longer referenced."},
		},
		Meta: render.Meta{Provider: "anthropic", Model: "claude-sonnet-4-6",
			Usage: provider.Usage{InputTokens: 4231, OutputTokens: 1503},
			Cost: 0.0319, Latency: 4200 * time.Millisecond,
			Files: 5, LinesAdded: 42, LinesRemoved: 11, RulesLoaded: true},
	}
	_ = render.Cards(os.Stdout, p)
}
