// Package i18n translates backend-side text (CLI output, notifications).
// The web UI does its own translation from API error codes.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

//go:embed locales/*.json
var localeFS embed.FS

const Fallback = "en"

var (
	once    sync.Once
	catalog map[string]map[string]string
)

func load() {
	catalog = map[string]map[string]string{}
	entries, _ := localeFS.ReadDir("locales")
	for _, e := range entries {
		raw, err := localeFS.ReadFile("locales/" + e.Name())
		if err != nil {
			continue
		}
		m := map[string]string{}
		if json.Unmarshal(raw, &m) == nil {
			catalog[strings.TrimSuffix(e.Name(), ".json")] = m
		}
	}
	// zh-TW shares the Traditional Chinese catalog unless it ships its own.
	if _, ok := catalog["zh-TW"]; !ok {
		catalog["zh-TW"] = catalog["zh-HK"]
	}
}

// T returns the message for key in lang, formatted with args.
func T(lang, key string, args ...any) string {
	once.Do(load)
	msg, ok := catalog[lang][key]
	if !ok {
		if msg, ok = catalog[Fallback][key]; !ok {
			msg = key
		}
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}

// Translator binds a language for repeated lookups.
type Translator string

func (l Translator) T(key string, args ...any) string { return T(string(l), key, args...) }
