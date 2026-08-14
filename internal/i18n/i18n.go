// Package i18n provides the UI message catalogue for SimpleClient.
//
// English is the source language and the default at startup; Turkish is
// available as an option. The active language can be set from the -lang flag
// (or the "lang=" kernel command line parameter) and toggled at runtime with
// F2 from the discovery screen.
//
// Messages are addressed by typed Key constants rather than strings, so a
// missing translation is a compile-time array-length mismatch instead of a
// silent empty label at runtime. TestCatalogues_Complete enforces that every
// language defines every key.
package i18n

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// Lang is an ISO 639-1 language code supported by the UI.
type Lang string

const (
	// LangEN is English — the source language and the startup default.
	LangEN Lang = "en"
	// LangTR is Turkish.
	LangTR Lang = "tr"
)

// Default is the language used when none is configured.
const Default = LangEN

// order defines the cycle sequence used by Next (F2 toggling).
var order = []Lang{LangEN, LangTR}

// Key identifies a UI message. Keys are dense and contiguous so catalogues can
// be plain arrays indexed by Key.
type Key int

const (
	HostsFound Key = iota // "%d servers found"
	Scanning              // "Scanning..."
	ScanComplete
	NoServersFound
	KeyHints
	ModalTitle
	LabelUsername
	LabelPassword
	LabelDomain
	ButtonConnect
	ButtonCancel
	Connecting
	Disconnected
	ErrTimeout
	ErrRefused
	ErrAuthFailed
	LanguageName // Native name of the active language, e.g. "Türkçe"

	numKeys // must stay last
)

// catalogue holds one message per Key.
type catalogue [numKeys]string

var catalogues = map[Lang]*catalogue{
	LangEN: {
		HostsFound:     "%d servers found",
		Scanning:       "Scanning...",
		ScanComplete:   "Scan complete",
		NoServersFound: "No servers found",
		KeyHints:       "↑↓ Select   Enter Connect   F5 Refresh   F2 Language",
		ModalTitle:     "Connection Details",
		LabelUsername:  "Username:",
		LabelPassword:  "Password:",
		LabelDomain:    "Domain:",
		ButtonConnect:  "Connect",
		ButtonCancel:   "Cancel",
		Connecting:     "Connecting... %s",
		Disconnected:   "Disconnected",
		ErrTimeout:     "Timeout: server unreachable",
		ErrRefused:     "Connection refused",
		ErrAuthFailed:  "Authentication failed",
		LanguageName:   "English",
	},
	LangTR: {
		HostsFound:     "%d sunucu bulundu",
		Scanning:       "Taranıyor...",
		ScanComplete:   "Tarama tamamlandı",
		NoServersFound: "Sunucu bulunamadı",
		KeyHints:       "↑↓ Seç   Enter Bağlan   F5 Yenile   F2 Dil",
		ModalTitle:     "Bağlantı Bilgileri",
		LabelUsername:  "Kullanıcı:",
		LabelPassword:  "Parola:",
		LabelDomain:    "Etki alanı:",
		ButtonConnect:  "Bağlan",
		ButtonCancel:   "İptal",
		Connecting:     "Bağlanıyor... %s",
		Disconnected:   "Bağlantı kesildi",
		ErrTimeout:     "Zaman aşımı: sunucuya ulaşılamıyor",
		ErrRefused:     "Bağlantı reddedildi",
		ErrAuthFailed:  "Kimlik doğrulama başarısız",
		LanguageName:   "Türkçe",
	},
}

// active holds the catalogue currently in use. It is read from the render loop
// and written from the input handler and the RDP connect goroutine, so access
// goes through atomic.Pointer rather than a plain variable.
var active atomic.Pointer[catalogue]

// activeLang mirrors active so Current can report the code without a reverse lookup.
var activeLang atomic.Pointer[Lang]

func init() {
	Set(Default)
}

// Set switches the active language. Unknown languages fall back to Default.
func Set(l Lang) {
	c, ok := catalogues[l]
	if !ok {
		l, c = Default, catalogues[Default]
	}
	active.Store(c)
	activeLang.Store(&l)
}

// Current returns the active language code.
func Current() Lang {
	if p := activeLang.Load(); p != nil {
		return *p
	}
	return Default
}

// Next switches to the next language in the cycle and returns it. Used by the
// F2 runtime toggle.
func Next() Lang {
	cur := Current()
	for i, l := range order {
		if l == cur {
			next := order[(i+1)%len(order)]
			Set(next)
			return next
		}
	}
	Set(Default)
	return Default
}

// Available returns the supported languages in cycle order.
func Available() []Lang {
	out := make([]Lang, len(order))
	copy(out, order)
	return out
}

// Parse resolves a user-supplied language string (e.g. "tr", "TR", "tr_TR.UTF-8")
// to a supported Lang. It reports whether the input matched a supported language.
func Parse(s string) (Lang, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return Default, false
	}
	// Accept locale forms such as "tr_TR.UTF-8" or "en-GB" by taking the prefix.
	if i := strings.IndexAny(s, "_-."); i > 0 {
		s = s[:i]
	}
	for _, l := range order {
		if string(l) == s {
			return l, true
		}
	}
	return Default, false
}

// T returns the message for key in the active language.
func T(key Key) string {
	if key < 0 || key >= numKeys {
		return ""
	}
	return active.Load()[key]
}

// Tf returns the message for key formatted with args.
func Tf(key Key, args ...any) string {
	return fmt.Sprintf(T(key), args...)
}
