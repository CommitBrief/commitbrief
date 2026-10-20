// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/leaks"
	"github.com/CommitBrief/commitbrief/internal/render"
)

// The commit walk's own ceiling is invisible to the user unless the report
// says so: "no credentials found" over a repo whose older history was never
// scanned is misleading. These tests drive the report writers directly because
// the 10000-commit ceiling cannot be reached from a CLI fixture.

func leaksWalkApp(t *testing.T, code string) *appContext {
	t.Helper()
	cat, err := i18n.Load(code)
	if err != nil {
		t.Fatal(err)
	}
	return &appContext{Catalog: cat}
}

func TestLeaksWalkTruncatedNoticeInHumanReport(t *testing.T) {
	global = globalFlags{color: "never"}
	for _, code := range []string{"en", "tr"} {
		app := leaksWalkApp(t, code)
		if !app.Catalog.Has("leaks.walk_truncated") {
			t.Fatalf("%s catalog lacks leaks.walk_truncated", code)
		}
		want := strconv.Itoa(git.WalkLimit())

		var buf bytes.Buffer
		if err := writeLeaksReport(&buf, app, leaks.Result{WalkTruncated: true}, nil); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), "⚠") || !strings.Contains(buf.String(), want) {
			t.Errorf("%s: expected the walk-truncation notice naming %s commits, got:\n%s", code, want, buf.String())
		}

		buf.Reset()
		if err := writeLeaksReport(&buf, app, leaks.Result{}, nil); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(buf.String(), want) {
			t.Errorf("%s: a complete walk must not print the notice, got:\n%s", code, buf.String())
		}
	}
}

func TestLeaksWalkTruncatedNoticeSilencedByQuiet(t *testing.T) {
	global = globalFlags{color: "never", quiet: true}
	t.Cleanup(func() { global = globalFlags{color: "never"} })

	var buf bytes.Buffer
	app := leaksWalkApp(t, "en")
	if err := writeLeaksReport(&buf, app, leaks.Result{WalkTruncated: true}, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "⚠") {
		t.Errorf("--quiet must silence the notice, got:\n%s", buf.String())
	}
}

func leaksWalkJSONMeta(t *testing.T, res leaks.Result) map[string]any {
	t.Helper()
	global = globalFlags{color: "never"}
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	app := leaksWalkApp(t, "en")
	if err := emitLeaksJSON(cmd, app, []render.Finding{}, res); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Schema int            `json:"schema"`
		Meta   map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, buf.String())
	}
	if doc.Schema != 1 {
		t.Errorf("schema = %d, want 1", doc.Schema)
	}
	return doc.Meta
}

func TestLeaksWalkTruncatedJSONMeta(t *testing.T) {
	meta := leaksWalkJSONMeta(t, leaks.Result{WalkTruncated: true})
	if meta["walk_truncated"] != true {
		t.Errorf("meta.walk_truncated = %v, want true", meta["walk_truncated"])
	}

	// Additive-optional: a complete walk keeps the v1 meta shape.
	meta = leaksWalkJSONMeta(t, leaks.Result{})
	if _, present := meta["walk_truncated"]; present {
		t.Errorf("meta.walk_truncated must be omitted when false, got %v", meta["walk_truncated"])
	}
}
