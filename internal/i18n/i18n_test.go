package i18n

import (
	"testing"
	"time"
)

func TestCatalogsHaveSameKeys(t *testing.T) {
	en, ru := Keys("en"), Keys("ru")
	for k := range en {
		if !ru[k] {
			t.Errorf("ru.json is missing %q", k)
		}
	}
	for k := range ru {
		if !en[k] {
			t.Errorf("en.json is missing %q", k)
		}
	}
}

func TestDuration(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second:              "1 min",
		5 * time.Minute:               "5 min",
		2*time.Hour + 5*time.Minute:   "2 h 5 min",
		26*time.Hour + 10*time.Minute: "1 d 2 h",
	}
	for d, want := range cases {
		if got := Duration("en", d); got != want {
			t.Errorf("Duration(%v) = %q, want %q", d, got, want)
		}
	}
	if got := Duration("ru", 3*time.Minute); got != "3 мин" {
		t.Errorf("ru: got %q", got)
	}
}

func TestFallback(t *testing.T) {
	if got := T("de", "btn.cancel"); got != "Cancel" {
		t.Errorf("unknown language should fall back to English, got %q", got)
	}
	if got := T("en", "no.such.key"); got != "no.such.key" {
		t.Errorf("missing key should return the key, got %q", got)
	}
}

// The bot's texts are plain: no emoji.
func TestNoEmoji(t *testing.T) {
	for _, lang := range Langs {
		for key := range Keys(lang) {
			for _, r := range T(lang, key) {
				if r >= 0x1F000 || (r >= 0x2600 && r <= 0x27BF) || (r >= 0x2B00 && r <= 0x2BFF) || r == 0xFE0F {
					t.Errorf("%s %s contains emoji %q", lang, key, r)
				}
			}
		}
	}
}
