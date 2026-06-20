// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/i18n"
)

const archTestJSON = `{
  "layers": {"domain": ["internal/domain"], "db": ["internal/db"]},
  "rules":  {"domain": [], "db": ["domain"]}
}`

// archTestApp wires an appContext rooted at repoRoot with a real catalog and
// the default config (review.architecture = true).
func archTestApp(t *testing.T, repoRoot string) *appContext {
	t.Helper()
	cat, err := i18n.Load("en")
	if err != nil {
		t.Fatal(err)
	}
	return &appContext{RepoRoot: repoRoot, Config: config.Default(), Catalog: cat}
}

func writeArchFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveArchContextEnabledFindsFile(t *testing.T) {
	resetGlobalFlags(t)
	dir := t.TempDir()
	writeArchFile(t, dir, "architecture.json", archTestJSON)
	app := archTestApp(t, dir)

	ctx, err := resolveArchContext(app, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(ctx, "must NOT import") {
		t.Errorf("architecture context should describe forbidden edges; got:\n%s", ctx)
	}
}

func TestResolveArchContextDisabledByFlag(t *testing.T) {
	resetGlobalFlags(t)
	global.noArchitecture = true
	dir := t.TempDir()
	writeArchFile(t, dir, "architecture.json", archTestJSON)
	app := archTestApp(t, dir)

	ctx, err := resolveArchContext(app, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx != "" {
		t.Errorf("--no-architecture must suppress the context even when the file exists; got:\n%s", ctx)
	}
}

func TestResolveArchContextDisabledByConfig(t *testing.T) {
	resetGlobalFlags(t)
	dir := t.TempDir()
	writeArchFile(t, dir, "architecture.json", archTestJSON)
	app := archTestApp(t, dir)
	app.Config.Review.Architecture = false

	ctx, err := resolveArchContext(app, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx != "" {
		t.Errorf("review.architecture=false must suppress the context; got:\n%s", ctx)
	}
}

func TestResolveArchContextAutoMissIsNoOp(t *testing.T) {
	resetGlobalFlags(t)
	dir := t.TempDir() // no architecture.json
	app := archTestApp(t, dir)

	ctx, err := resolveArchContext(app, false)
	if err != nil {
		t.Fatalf("auto-discovery miss must not error; got %v", err)
	}
	if ctx != "" {
		t.Errorf("no file → empty context; got:\n%s", ctx)
	}
}

func TestResolveArchContextConfiguredMissingErrors(t *testing.T) {
	resetGlobalFlags(t)
	dir := t.TempDir()
	app := archTestApp(t, dir)
	app.Config.Review.ArchitectureFile = "does-not-exist.json"

	if _, err := resolveArchContext(app, false); err == nil {
		t.Error("an explicitly configured but missing file must error")
	}
}

func TestResolveArchContextMalformedFileNoError(t *testing.T) {
	resetGlobalFlags(t)
	dir := t.TempDir()
	writeArchFile(t, dir, "architecture.json", "{ not valid json")
	app := archTestApp(t, dir)

	ctx, err := resolveArchContext(app, false)
	if err != nil {
		t.Fatalf("malformed file must be a no-op, not an error; got %v", err)
	}
	if ctx != "" {
		t.Errorf("malformed file → empty context (never break a review); got:\n%s", ctx)
	}
}

func TestResolveArchContextCustomFile(t *testing.T) {
	resetGlobalFlags(t)
	dir := t.TempDir()
	writeArchFile(t, dir, "boundaries.json", archTestJSON)
	app := archTestApp(t, dir)
	app.Config.Review.ArchitectureFile = "boundaries.json"

	ctx, err := resolveArchContext(app, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx == "" {
		t.Error("a configured custom architecture file should be loaded")
	}
}
