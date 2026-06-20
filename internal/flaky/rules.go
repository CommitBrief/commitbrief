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

// reBrittleSelector matches fragile UI/E2E test selectors that break on
// unrelated DOM/layout changes. Every fragment requires a positional or
// absolute-path signal so ordinary class/data-testid/role selectors do not
// match. Scoped to JS/TS (the Cypress/Playwright/Selenium-WebDriver-JS
// surface) to keep precision high.
var reBrittleSelector = alt(
	`:nth-child\s*\(`,                      // CSS structural pseudo-class (position-dependent)
	`:nth-of-type\s*\(`,                    // CSS structural pseudo-class (position-dependent)
	`xpath\s*=\s*["'`+"`"+`]\s*/`,          // Playwright/Selenium "xpath=/..." absolute-path locator
	`\bBy\.xpath\s*\(\s*["'`+"`"+`]\s*//?`, // Selenium By.xpath('//..' or '/..') absolute path
	`\.locator\s*\(\s*["'`+"`"+`]\s*//`,    // Playwright .locator('//..') absolute XPath
	`\.eq\s*\(\s*\d`,                       // Cypress positional .eq(<index>)
	`\.nth\s*\(\s*\d`,                      // Playwright positional .nth(<index>)
	`\[\s*\d+\s*\]\s*$`,                    // trailing index predicate on an XPath string, e.g. //div[2]
)

// reTimeAssertion matches a wall-clock read used directly in an assertion,
// i.e. a non-injected clock compared against in a test. The line must carry
// BOTH a clock source AND an assertion token; a clock used only for setup or
// timeout configuration is intentionally not flagged. Keeping the two halves
// on one line keeps the rule deterministic and conservative.
var reTimeAssertion = func() *regexp.Regexp {
	clock := `(?:` + strings.Join([]string{
		`\btime\.Now\s*\(\s*\)`,                 // Go
		`\bDate\.now\s*\(\s*\)`,                 // JS / TS
		`\bnew\s+Date\s*\(\s*\)`,                // JS / TS (no-arg constructor)
		`\bdatetime\.(?:now|utcnow|today)\s*\(`, // Python
		`\bSystem\.currentTimeMillis\s*\(\s*\)`, // Java
		`\bSystem\.nanoTime\s*\(\s*\)`,          // Java
	}, "|") + `)`
	assertion := `(?:` + strings.Join([]string{
		`\bassert`,                         // Go testify, Python, Java
		`\bexpect\s*\(`,                    // JS / TS (Jest, Playwright)
		`\.(?:toBe|toEqual|toBeCloseTo)\b`, // JS / TS matchers
		`\.should\b`,                       // RSpec / Cypress / Chai
		`\b(?:assertEquals|assertThat|assertSame)\b`, // JUnit
		`\.Equal\s*\(`, // Go testify require/assert
	}, "|") + `)`
	// Either order: assertion-then-clock or clock-then-assertion on the line.
	return regexp.MustCompile(assertion + `.*` + clock + `|` + clock + `.*` + assertion)
}()

// reMockSetup matches one mock/stub setup statement. over-mock counts these
// per added line within a single test function (overMockThreshold). Each
// fragment is a distinct framework idiom across the stacks CommitBrief
// reviews; counting is conservative (one count per matching line).
var reMockSetup = alt(
	`\bwhen\s*\(.*\)\s*\.\s*then(?:Return|Throw|Answer)\b`,             // Mockito (Java/Kotlin)
	`\bMockito\.(?:mock|spy|when|verify)\s*\(`,                         // Mockito explicit
	`\bjest\.(?:mock|spyOn|fn)\s*\(`,                                   // Jest (JS/TS)
	`\.mockReturnValue\b|\.mockResolvedValue\b|\.mockImplementation\b`, // Jest mock config
	`\bsinon\.(?:stub|mock|spy|fake)\s*\(`,                             // Sinon (JS/TS)
	`\b(?:mocker\.)?patch(?:\.object)?\s*\(`,                           // Python unittest.mock / pytest-mock
	`\bMagicMock\s*\(|\bMock\s*\(`,                                     // Python Mock objects
	`\bgomock\.|\.EXPECT\s*\(\s*\)`,                                    // Go gomock
	`->\s*(?:shouldReceive|expects)\s*\(`,                              // PHP Mockery / PHPUnit
)

// overMockThreshold is the number of distinct mock-setup lines within one
// test function above which the function is flagged. Five is deliberately
// high: small fixtures legitimately stub two or three collaborators, so the
// gate only fires on the runaway "mock everything" pattern that signals the
// test is pinned to its implementation rather than its behaviour.
const overMockThreshold = 5

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
	{
		id:       "brittle-selector",
		langs:    map[string]bool{"js": true, "ts": true},
		pattern:  reBrittleSelector,
		severity: render.SeverityLow,
		titleKey: "flaky.brittle_selector.title",
		descKey:  "flaky.brittle_selector.description",
		sugKey:   "flaky.brittle_selector.suggestion",
	},
	{
		id:       "time-dependency",
		pattern:  reTimeAssertion,
		severity: render.SeverityLow,
		titleKey: "flaky.time_dependency.title",
		descKey:  "flaky.time_dependency.description",
		sugKey:   "flaky.time_dependency.suggestion",
	},
}

// overMockRule is the single file-scoped rule (ADR-0022 §3). Unlike the
// line-level rules it cannot be decided from one line: it counts mock-setup
// statements inside a test function and only fires above overMockThreshold.
// It is carried separately from rules so the per-line scan stays trivial.
var overMockRule = struct {
	id       string
	severity render.Severity
	titleKey string
	descKey  string
	sugKey   string
}{
	id:       "over-mock",
	severity: render.SeverityLow,
	titleKey: "flaky.over_mock.title",
	descKey:  "flaky.over_mock.description",
	sugKey:   "flaky.over_mock.suggestion",
}

// reTestFuncHeader matches the start of a test function across the supported
// languages, used to scope over-mock counting to a single test body. It is
// deliberately permissive on the name (any identifier) but requires the
// language's test-marker shape, so non-test helpers do not open a counting
// scope.
var reTestFuncHeader = alt(
	`\bfunc\s+(?:\([^)]*\)\s*)?Test[A-Z0-9_]`,                                     // Go: func TestX / method TestX
	`\b(?:public|private|protected)?\s*(?:void|async\s+\w+)\s+\w*[Tt]est\w*\s*\(`, // Java/C#-ish
	`\b(?:it|test|describe)\s*\(\s*["'`+"`"+`]`,                                   // JS/TS BDD blocks
	`\bdef\s+test_?\w*\s*\(`,                                                      // Python
	`\bpublic\s+function\s+test\w*\s*\(`,                                          // PHP / PHPUnit
)
