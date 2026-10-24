// Package i18n holds the bot's English and Russian strings.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"time"
)

//go:embed en.json ru.json
var files embed.FS

var catalogs = map[string]map[string]string{}

func init() {
	for _, lang := range []string{"en", "ru"} {
		b, err := files.ReadFile(lang + ".json")
		if err != nil {
			panic(err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(b, &m); err != nil {
			panic(fmt.Sprintf("i18n %s: %v", lang, err))
		}
		catalogs[lang] = m
	}
}

// Langs lists supported languages.
var Langs = []string{"en", "ru"}

// Normalize maps anything unsupported to English.
func Normalize(lang string) string {
	if lang == "ru" {
		return "ru"
	}
	return "en"
}

// T returns the formatted string for key, falling back to English and then to the key itself.
func T(lang, key string, args ...any) string {
	s, ok := catalogs[Normalize(lang)][key]
	if !ok {
		if s, ok = catalogs["en"][key]; !ok {
			return key
		}
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// Duration renders d as "2 h 5 min" in the given language, at minute precision.
func Duration(lang string, d time.Duration) string {
	m := int(d.Round(time.Minute) / time.Minute)
	if m < 1 {
		m = 1
	}
	days, hours, mins := m/1440, m%1440/60, m%60
	switch {
	case days > 0:
		return fmt.Sprintf("%d %s %d %s", days, T(lang, "dur.d"), hours, T(lang, "dur.h"))
	case hours > 0:
		return fmt.Sprintf("%d %s %d %s", hours, T(lang, "dur.h"), mins, T(lang, "dur.m"))
	default:
		return fmt.Sprintf("%d %s", mins, T(lang, "dur.m"))
	}
}

// Keys returns the key set of a language, for tests.
func Keys(lang string) map[string]bool {
	out := map[string]bool{}
	for k := range catalogs[lang] {
		out[k] = true
	}
	return out
}
