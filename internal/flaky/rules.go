// SPDX-License-Identifier: GPL-3.0-or-later

package flaky

import (
	"regexp"
	"strings"

	"github.com/CommitBrief/commitbrief/internal/render"
)

// rule is one deterministic anti-pattern. pattern is matched against the raw
// text of an added line (the diff marker is already stripped by the parser).
// langs nil means "every language"; otherwise the rule only applies to the
// listed identifiers from detectLang. The *Key fields are i18n catalog keys
// (present in messages.en.yml and messages.tr.yml).
type rule struct {
	id       string
	langs    map[string]bool
	pattern  *regexp.Regexp
	severity render.Severity
	titleKey string
	descKey  string
	sugKey   string
}

func (r rule) appliesTo(lang string) bool {
	if r.langs == nil {
		return true
	}
	return r.langs[lang]
}

// alt joins regex fragments into a single anchored alternation. Keeping the
// fragments as a list documents each language token on its own line.
func alt(parts ...string) *regexp.Regexp {
	return regexp.MustCompile(strings.Join(parts, "|"))
}

// reHardSleep matches fixed sleeps/waits used for synchronization. Each
// fragment is a distinct language idiom; numeric-argument guards (\d) keep the
// generic sleep( forms from matching alias-style calls.
var reHardSleep = alt(
	`\btime\.[Ss]leep\s*\(`,   // Go, Python (time.sleep)
	`\basyncio\.sleep\s*\(`,   // Python async
	`\bThread\.[Ss]leep\s*\(`, // Java / Kotlin / C#
	`\bTask\.Delay\s*\(`,      // C#
	`\.waitForTimeout\s*\(`,   // Playwright / Puppeteer
	`\bcy\.wait\s*\(\s*\d`,    // Cypress fixed numeric wait (not cy.wait('@alias'))
	`\busleep\s*\(`,           // PHP / C
	`\bsleep\s*\(\s*\d`,       // PHP / Ruby / Python (from-import)
)

// reUnseededRandom matches unseeded random sources whose values differ run to
// run, making assertions non-deterministic.
var reUnseededRandom = alt(
	`\bMath\.random\s*\(`, // JS / TS / Java
	`\brandom\.(?:random|randint|randrange|choice|shuffle|uniform|sample)\s*\(`, // Python
	`\brand\.(?:Intn|Int|Int31|Int63|Float32|Float64|Perm|Shuffle)\s*\(`,        // Go math/rand global
)

// rules is the registry, evaluated in order per added line. Additions are
// additive (ADR-0022 §3); keep severities conservative.
var rules = []rule{
	{
		id:       "hard-sleep",
		pattern:  reHardSleep,
		severity: render.SeverityMedium,
		titleKey: "flaky.hard_sleep.title",
		descKey:  "flaky.hard_sleep.description",
		sugKey:   "flaky.hard_sleep.suggestion",
	},
	{
		id:       "unseeded-random",
		pattern:  reUnseededRandom,
		severity: render.SeverityLow,
		titleKey: "flaky.unseeded_random.title",
		descKey:  "flaky.unseeded_random.description",
		sugKey:   "flaky.unseeded_random.suggestion",
	},
}
