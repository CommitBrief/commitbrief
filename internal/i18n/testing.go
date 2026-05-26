package i18n

import (
	"sort"
	"testing"
)

func MustHave(t *testing.T) {
	t.Helper()

	langs, err := Available()
	if err != nil {
		t.Fatalf("MustHave: list locales: %v", err)
	}
	if len(langs) < 2 {
		t.Fatalf("MustHave: expected at least 2 locales, found %d (%v)", len(langs), langs)
	}

	catalogs := make(map[string]*Catalog, len(langs))
	keysOf := make(map[string]map[string]struct{}, len(langs))
	for _, code := range langs {
		c, err := Load(code)
		if err != nil {
			t.Fatalf("MustHave: load %s: %v", code, err)
		}
		catalogs[code] = c
		set := make(map[string]struct{}, len(c.messages))
		for k := range c.messages {
			set[k] = struct{}{}
		}
		keysOf[code] = set
	}

	union := make(map[string]struct{})
	for _, set := range keysOf {
		for k := range set {
			union[k] = struct{}{}
		}
	}

	for _, code := range langs {
		var missing []string
		for k := range union {
			if _, ok := keysOf[code][k]; !ok {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			t.Errorf("locale %q missing %d key(s): %v", code, len(missing), missing)
		}
	}
}
