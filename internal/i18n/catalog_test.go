package i18n

import (
	"strings"
	"testing"
)

func TestLoadEnglish(t *testing.T) {
	c, err := Load("en")
	if err != nil {
		t.Fatalf("Load(en): %v", err)
	}
	if c.Code() != "en" {
		t.Errorf("Code() = %q, want %q", c.Code(), "en")
	}
	if got := c.T("setup.welcome"); !strings.Contains(got, "CommitBrief") {
		t.Errorf("T(setup.welcome) = %q, want substring %q", got, "CommitBrief")
	}
}

func TestLoadTurkish(t *testing.T) {
	c, err := Load("tr")
	if err != nil {
		t.Fatalf("Load(tr): %v", err)
	}
	if c.Code() != "tr" {
		t.Errorf("Code() = %q, want %q", c.Code(), "tr")
	}
	if got := c.T("setup.welcome"); !strings.Contains(got, "CommitBrief") {
		t.Errorf("T(setup.welcome) = %q, want substring %q", got, "CommitBrief")
	}
}

func TestLoadEmptyDefaultsToEnglish(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): %v", err)
	}
	if c.Code() != "en" {
		t.Errorf("empty lang fell through to %q, want %q", c.Code(), "en")
	}
}

func TestLoadUnknownLangFallsBackToEnglish(t *testing.T) {
	c, err := Load("xx")
	if err != nil {
		t.Fatalf("Load(xx): %v", err)
	}
	if c.Code() != "en" {
		t.Errorf("unknown lang fell through to %q, want %q", c.Code(), "en")
	}
}

func TestTArgsFormat(t *testing.T) {
	c, err := Load("en")
	if err != nil {
		t.Fatalf("Load(en): %v", err)
	}
	got := c.T("cli.error.unknown_provider", "claude")
	if !strings.Contains(got, "claude") {
		t.Errorf("T with args missed substitution: %q", got)
	}
}

func TestTMissingKeyReturnsKey(t *testing.T) {
	c, err := Load("en")
	if err != nil {
		t.Fatalf("Load(en): %v", err)
	}
	const missing = "does.not.exist.anywhere"
	if got := c.T(missing); got != missing {
		t.Errorf("T(missing) = %q, want raw key %q (debuggable fallback)", got, missing)
	}
}

func TestHas(t *testing.T) {
	c, _ := Load("en")
	if !c.Has("setup.welcome") {
		t.Error("Has(setup.welcome) = false, want true")
	}
	if c.Has("does.not.exist") {
		t.Error("Has(does.not.exist) = true, want false")
	}
}

func TestAvailable(t *testing.T) {
	langs, err := Available()
	if err != nil {
		t.Fatalf("Available: %v", err)
	}
	want := map[string]bool{"en": true, "tr": true}
	for _, code := range langs {
		delete(want, code)
	}
	if len(want) > 0 {
		t.Errorf("Available missing locales: %v (got %v)", want, langs)
	}
}

func TestKeyParity(t *testing.T) { MustHave(t) }
