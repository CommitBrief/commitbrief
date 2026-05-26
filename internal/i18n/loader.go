package i18n

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const DefaultLang = "en"

//go:embed messages.*.yml
var messageFS embed.FS

func Load(lang string) (*Catalog, error) {
	if lang == "" {
		lang = DefaultLang
	}
	name := fmt.Sprintf("messages.%s.yml", lang)
	data, err := messageFS.ReadFile(name)
	if err != nil {
		if lang == DefaultLang {
			return nil, fmt.Errorf("i18n: load %s: %w", name, err)
		}
		return Load(DefaultLang)
	}
	var m map[string]string
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("i18n: parse %s: %w", name, err)
	}
	return &Catalog{code: lang, messages: m}, nil
}

func Available() ([]string, error) {
	entries, err := fs.ReadDir(messageFS, ".")
	if err != nil {
		return nil, err
	}
	var langs []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "messages.") || !strings.HasSuffix(name, ".yml") {
			continue
		}
		code := strings.TrimSuffix(strings.TrimPrefix(name, "messages."), ".yml")
		langs = append(langs, code)
	}
	sort.Strings(langs)
	return langs, nil
}
