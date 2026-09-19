// SPDX-License-Identifier: GPL-3.0-or-later

package meta

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/arch"
	"github.com/CommitBrief/commitbrief/internal/config"
)

// indexConfigKeys is the same lookup shape TestConfigKeysUsePlaceholdersForFreeFormMaps
// builds inline; a second copy here would drift, so this test computes its
// own small index instead of depending on that one's unexported helper.
func indexConfigKeys(t *testing.T) map[string]ConfigKey {
	t.Helper()
	out := map[string]ConfigKey{}
	for _, k := range ConfigKeys() {
		out[k.Path] = k
	}
	return out
}

// review.architecture_file has no literal default in config.Default() — it
// must stay "" so arch.Discover's auto-discovery path (an unconfigured repo
// probing repoRoot/architecture.json) stays a silent no-op instead of
// becoming an explicitly-configured path whose absence is a hard error (see
// internal/arch/arch.go's resolvePath). The inventory must still report the
// documented effective default (internal/config/config.go: "default
// architecture.json"), or a generated docs table built from it contradicts
// the doc comment right next to the field.
func TestConfigKeysReportsArchitectureFileEffectiveDefault(t *testing.T) {
	if got := config.Default().Review.ArchitectureFile; got != "" {
		t.Fatalf("config.Default().Review.ArchitectureFile = %q, want empty — "+
			"a non-empty value here makes arch.Discover treat it as explicitly "+
			"configured, turning a missing architecture.json into a hard error", got)
	}

	k, ok := indexConfigKeys(t)["review.architecture_file"]
	if !ok {
		t.Fatal("missing config key \"review.architecture_file\"")
	}
	if !k.HasDefault {
		t.Error("review.architecture_file: HasDefault = false, want true")
	}
	if k.Default != arch.DefaultFilename {
		t.Errorf("review.architecture_file: Default = %q, want %q (arch.DefaultFilename)", k.Default, arch.DefaultFilename)
	}
}

// command.default's real default IS the empty string (an unset value keeps
// the built-in bare-invocation behavior). HasDefault must say so even though
// Default itself renders empty, or a JSON consumer cannot tell this apart
// from a key with no default at all — the exact ambiguity this field exists
// to remove.
func TestConfigKeysDistinguishesEmptyDefaultFromNoDefault(t *testing.T) {
	idx := indexConfigKeys(t)

	cmdDefault, ok := idx["command.default"]
	if !ok {
		t.Fatal("missing config key \"command.default\"")
	}
	if !cmdDefault.HasDefault {
		t.Error("command.default: HasDefault = false, want true (its real default is the empty string)")
	}
	if cmdDefault.Default != "" {
		t.Errorf("command.default: Default = %q, want \"\"", cmdDefault.Default)
	}

	// A struct/object node (e.g. the "review" section itself) has no default
	// concept at all — that must render as HasDefault=false, not as an
	// empty-string default.
	reviewSection, ok := idx["review"]
	if !ok {
		t.Fatal("missing config key \"review\"")
	}
	if reviewSection.HasDefault {
		t.Errorf("review: HasDefault = true, want false (an object node has no scalar default)")
	}

	// A free-form key (below a user-chosen map key) has no default either.
	providerModel, ok := idx["providers.<name>.model"]
	if !ok {
		t.Fatal("missing config key \"providers.<name>.model\"")
	}
	if providerModel.HasDefault {
		t.Errorf("providers.<name>.model: HasDefault = true, want false (free-form leaves have no default)")
	}

	// The two real leaves must be distinguishable in the serialized JSON:
	// command.default carries an explicit has_default with no default key,
	// review carries neither.
	cmdJSON, err := json.Marshal(cmdDefault)
	if err != nil {
		t.Fatalf("marshal command.default: %v", err)
	}
	if got := string(cmdJSON); !strings.Contains(got, `"has_default":true`) {
		t.Errorf("command.default JSON = %s, want it to carry has_default:true", got)
	}

	reviewJSON, err := json.Marshal(reviewSection)
	if err != nil {
		t.Fatalf("marshal review: %v", err)
	}
	if got := string(reviewJSON); strings.Contains(got, "has_default") {
		t.Errorf("review JSON = %s, must not carry has_default at all", got)
	}
}
