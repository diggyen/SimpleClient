package i18n

import (
	"strings"
	"testing"
)

// resetLang restores the default language after a test that changes it.
func resetLang(t *testing.T) {
	t.Cleanup(func() { Set(Default) })
}

func TestDefaultIsEnglish(t *testing.T) {
	resetLang(t)
	Set(Default)
	if Current() != LangEN {
		t.Fatalf("default language = %q, want %q", Current(), LangEN)
	}
	if got := T(ScanComplete); got != "Scan complete" {
		t.Fatalf("T(ScanComplete) = %q, want English text", got)
	}
}

func TestCatalogues_Complete(t *testing.T) {
	for lang, c := range catalogues {
		for k := Key(0); k < numKeys; k++ {
			if c[k] == "" {
				t.Errorf("language %q is missing a message for key %d", lang, k)
			}
		}
	}
}

// Format verbs must match across languages, otherwise Tf would emit %!d(MISSING)
// in one language but not another.
func TestCatalogues_FormatVerbsMatch(t *testing.T) {
	base := catalogues[Default]
	for lang, c := range catalogues {
		if lang == Default {
			continue
		}
		for k := Key(0); k < numKeys; k++ {
			if want, got := strings.Count(base[k], "%"), strings.Count(c[k], "%"); want != got {
				t.Errorf("key %d: %q has %d format verbs, %q has %d", k, Default, want, lang, got)
			}
		}
	}
}

func TestSetAndT(t *testing.T) {
	resetLang(t)
	Set(LangTR)
	if Current() != LangTR {
		t.Fatalf("Current() = %q, want %q", Current(), LangTR)
	}
	if got := T(ScanComplete); got != "Tarama tamamlandı" {
		t.Fatalf("T(ScanComplete) in tr = %q", got)
	}
	if got := T(LanguageName); got != "Türkçe" {
		t.Fatalf("T(LanguageName) in tr = %q", got)
	}
}

func TestSetUnknownFallsBackToDefault(t *testing.T) {
	resetLang(t)
	Set(Lang("de"))
	if Current() != Default {
		t.Fatalf("unknown language should fall back to %q, got %q", Default, Current())
	}
}

func TestNextCycles(t *testing.T) {
	resetLang(t)
	Set(LangEN)
	if got := Next(); got != LangTR {
		t.Fatalf("Next() after en = %q, want tr", got)
	}
	if got := Next(); got != LangEN {
		t.Fatalf("Next() after tr = %q, want en", got)
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		in    string
		want  Lang
		valid bool
	}{
		{"en", LangEN, true},
		{"tr", LangTR, true},
		{"TR", LangTR, true},
		{"  tr  ", LangTR, true},
		{"tr_TR.UTF-8", LangTR, true},
		{"en-GB", LangEN, true},
		{"de", LangEN, false},
		{"", LangEN, false},
	}
	for _, tc := range cases {
		got, ok := Parse(tc.in)
		if got != tc.want || ok != tc.valid {
			t.Errorf("Parse(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.valid)
		}
	}
}

func TestTf(t *testing.T) {
	resetLang(t)
	Set(LangEN)
	if got := Tf(HostsFound, 3); got != "3 servers found" {
		t.Fatalf("Tf(HostsFound, 3) = %q", got)
	}
	Set(LangTR)
	if got := Tf(HostsFound, 3); got != "3 sunucu bulundu" {
		t.Fatalf("Tf(HostsFound, 3) in tr = %q", got)
	}
}

func TestTOutOfRange(t *testing.T) {
	if got := T(Key(-1)); got != "" {
		t.Fatalf("T(-1) = %q, want empty", got)
	}
	if got := T(numKeys); got != "" {
		t.Fatalf("T(numKeys) = %q, want empty", got)
	}
}

func TestHostCount_Plural(t *testing.T) {
	resetLang(t)

	Set(LangEN)
	if got := HostCount(1); got != "1 server found" {
		t.Errorf("HostCount(1) = %q, want the singular form", got)
	}
	if got := HostCount(0); got != "0 servers found" {
		t.Errorf("HostCount(0) = %q, want the plural form", got)
	}
	if got := HostCount(7); got != "7 servers found" {
		t.Errorf("HostCount(7) = %q, want the plural form", got)
	}

	// Turkish does not inflect the noun after a number.
	Set(LangTR)
	if got := HostCount(1); got != "1 sunucu bulundu" {
		t.Errorf("HostCount(1) in tr = %q", got)
	}
	if got := HostCount(7); got != "7 sunucu bulundu" {
		t.Errorf("HostCount(7) in tr = %q", got)
	}
}
