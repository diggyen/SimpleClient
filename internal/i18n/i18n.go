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
	HostsFound    Key = iota // "%d servers found"
	HostsFoundOne            // singular form; see HostCount
	Scanning                 // "Scanning..."
	ScanComplete
	NoServersFound
	SelectServer
	ColumnServer
	ColumnAddress
	ColumnLatency
	NoServersHint

	// Key hints are held as one label per action rather than a single
	// pre-joined sentence, so the footer can draw each key as its own cap.
	HintSelect
	HintConnect
	HintRefresh
	HintLanguage
	HintKeyboard
	HintSound
	BadgeSound
	BadgeKeyboard
	BadgeLanguage
	BadgeVideo // what decodes the sessions' video, after the version
	VideoCPU

	Tagline // sits under the wordmark; keep it short enough to fit at 640px
	ModalTitle
	LabelServer
	LabelUsername
	LabelPassword
	LabelDomain
	HintDomain // the toggle that shows the optional domain field
	ButtonConnect
	ButtonCancel
	Connecting
	Disconnected
	ErrTimeout
	ErrRefused
	ErrAuthFailed
	ErrBadAddress
	LanguageName // Native name of the active language, e.g. "Türkçe"

	// Installing to disk, offered when running from a USB stick or CD.
	HintAddress // F7: connect to a server typed in by hand
	HintInstall
	InstallTitle
	InstallPick
	InstallNoDisk
	InstallErase  // "%s" is the disk
	InstallTable  // "%s" is GPT or MBR
	InstallKeys   // "%s" is the confirming letter
	InstallLetter // the letter that confirms: Y, E
	InstallStepMedium
	InstallStepPartition
	InstallStepFormat
	InstallStepCopy
	InstallStepBootloader
	InstallStepFirmware
	InstallDone
	InstallFailed   // "%s" is the reason
	InstallSelf     // after the stick the kiosk runs from, in the list
	InstallSelfWarn // what installing onto it does
	InstallDoneSelf // installed onto it

	// Network settings (F8).
	HintNetwork
	NetTitle
	NetMode
	NetDHCP
	NetStatic
	NetAddress
	NetMask
	NetGateway
	NetDNS1
	NetDNS2
	NetSave
	NetCable   // "%s" is the interface
	NetNoCable // "%s" is the interface
	NetNoAddress
	NetApplying
	NetSaved
	NetSessionOnly
	NetErrAddress
	NetErrMask
	NetErrGateway
	NetErrDNS
	NetErrApply // "%s" is the reason

	// Going to a server at startup (F10 in the connection dialog).
	HintAutoOff    // the toggle: the server list at startup, as without it
	HintAutoDialog // the toggle: this server's dialog at startup
	HintAutoLogon  // the toggle: logging on to it, the password kept
	AutoCountdown  // "%d" is seconds
	AutoWaitNet
	AutoRetry // "%d" is seconds
	AutoKeys

	// Wireless networks, a dialog of the network settings'.
	NetWireless    // the network settings' row for it
	NetWifiChoose  // the row, with no network kept
	NetWifiOn      // "%s" is the network
	NetWifiJoining // "%s" is the network
	NetWifiOff     // "%s" is the network
	NetIfWireless  // "%s" is the interface
	WifiTitle
	WifiHidden // the row for a network that does not show its name
	WifiOpen
	WifiEnterprise
	WifiMarkOn
	WifiMarkJoining
	WifiMarkKept
	WifiScanning
	WifiNone
	WifiDown
	WifiStatusOn // "%s" is the network
	WifiStatusOff
	WifiStatusKept // "%s" is the network
	WifiNoAddress
	WifiJoining // "%s" is the network
	WifiFailed  // "%s" is the network
	WifiRejected
	WifiRejectedUser
	WifiForgotten // "%s" is the network
	WifiNetwork
	WifiName
	WifiSecurity
	WifiErrName
	WifiErrUser
	WifiErrNoPassword
	WifiErrPassword
	WifiErrWEP
	HintForget
	HintBack

	// Updating a disk install, F9 there.
	HintUpdate
	UpdateTitle
	UpdateThis   // the label of the version running
	UpdateLatest // the label of the newest release
	UpdateNotChecked
	UpdateChecking
	UpdateUpToDate
	UpdateReady             // "%s" is the release
	UpdateNoDisk            // "%s" is a release without an update for disk installs
	UpdateDidNotStart       // "%s" is the release
	UpdateDownloading       // "%s" is the release, then MB done and MB in all
	UpdateDownloadingNoSize // "%s" is the release, then MB done
	UpdateRestarting        // "%s" is the release
	UpdateFailed            // "%s" is the reason
	UpdateCheckFailed       // "%s" is the reason
	UpdateErrNetwork        // "%s" is what the network said
	UpdateErrDamaged
	UpdateErrStalled
	UpdateErrUnproven
	UpdateErrInsecure
	UpdateCancelled
	UpdateNew          // "%s" is the release; the note at the foot of the server list
	UpdateNoticeOK     // "%s" is the release; the banner after an update came up
	UpdateNoticeFailed // "%s" is the release, then the version gone back to
	ButtonCheck
	ButtonUpdate
	ButtonClose

	numKeys // must stay last
)

// catalogue holds one message per Key.
type catalogue [numKeys]string

var catalogues = map[Lang]*catalogue{
	LangEN: {
		HostsFound:     "%d servers found",
		HostsFoundOne:  "%d server found",
		Scanning:       "Scanning...",
		ScanComplete:   "Scan complete",
		NoServersFound: "No servers found",
		SelectServer:   "Select a server",
		ColumnServer:   "SERVER",
		ColumnAddress:  "ADDRESS",
		ColumnLatency:  "LATENCY",
		NoServersHint:  "Nothing is listening on port 3389 in %s. F5 scans again, F7 connects to an address, F8 sets up the network.",
		HintSelect:     "Select",
		HintConnect:    "Connect",
		HintRefresh:    "Refresh",
		HintLanguage:   "Language",
		HintKeyboard:   "Keyboard",
		HintSound:      "Sound",
		BadgeSound:     "Sound",
		BadgeKeyboard:  "Keyboard",
		BadgeLanguage:  "Language",
		BadgeVideo:     "Video",
		VideoCPU:       "CPU",
		Tagline:        "REMOTE DESKTOP KIOSK",
		ModalTitle:     "Connection Details",
		LabelServer:    "Server:",
		LabelUsername:  "Username:",
		LabelPassword:  "Password:",
		LabelDomain:    "Domain:",
		HintDomain:     "Domain",
		ButtonConnect:  "Connect",
		ButtonCancel:   "Cancel",
		Connecting:     "Connecting... %s",
		Disconnected:   "Disconnected",
		ErrTimeout:     "Timeout: server unreachable",
		ErrRefused:     "Connection refused",
		ErrAuthFailed:  "Authentication failed",
		ErrBadAddress:  "Server address not found",
		LanguageName:   "English",

		HintAddress:           "Address",
		HintInstall:           "Install",
		InstallTitle:          "Install to disk",
		InstallPick:           "Choose the disk to install on:",
		InstallNoDisk:         "No disk to install on (at least 4 GB).",
		InstallErase:          "Everything on %s will be erased.",
		InstallTable:          "Partition table: %s",
		InstallKeys:           "Press %s to install, Esc to go back.",
		InstallLetter:         "Y",
		InstallStepMedium:     "Reading the installation medium...",
		InstallStepPartition:  "Partitioning the disk...",
		InstallStepFormat:     "Creating file systems...",
		InstallStepCopy:       "Copying the system...",
		InstallStepBootloader: "Installing the boot loader...",
		InstallStepFirmware:   "Adding the UEFI boot entry...",
		InstallDone:           "Installed. Remove the USB stick or CD and press Enter to restart.",
		InstallFailed:         "Installation failed: %s",
		InstallSelf:           "(this stick)",
		InstallSelfWarn:       "The kiosk runs from this stick: what it installs is copied into memory first. Do not unplug the stick until it is done.",
		InstallDoneSelf:       "Installed onto this stick. Press Enter to restart from it.",

		HintNetwork:    "Network",
		NetTitle:       "Network settings",
		NetMode:        "Connection:",
		NetDHCP:        "DHCP (automatic)",
		NetStatic:      "Manual",
		NetAddress:     "IP address:",
		NetMask:        "Subnet mask:",
		NetGateway:     "Gateway:",
		NetDNS1:        "Preferred DNS:",
		NetDNS2:        "Alternate DNS:",
		NetSave:        "Save",
		NetCable:       "%s: cable in",
		NetNoCable:     "%s: no cable",
		NetNoAddress:   "no address",
		NetApplying:    "Applying...",
		NetSaved:       "Saved.",
		NetSessionOnly: "Applied until restart: a CD has nowhere to save settings.",
		NetErrAddress:  "Invalid IP address",
		NetErrMask:     "Invalid subnet mask",
		NetErrGateway:  "The gateway is not in this subnet",
		NetErrDNS:      "Invalid DNS server address",
		NetErrApply:    "Could not apply: %s",

		HintAutoOff:    "At startup: the server list",
		HintAutoDialog: "At startup: this server",
		HintAutoLogon:  "At startup: log on, password kept here",
		AutoCountdown:  "Logging on in %d s",
		AutoWaitNet:    "Waiting for the network",
		AutoRetry:      "Trying again in %d s",
		AutoKeys:       "Enter: now    Esc: server list",

		NetWireless:       "Wireless:",
		NetWifiChoose:     "Choose a network",
		NetWifiOn:         "%s  ·  connected",
		NetWifiJoining:    "%s  ·  connecting",
		NetWifiOff:        "%s  ·  not connected",
		NetIfWireless:     "%s: wireless",
		WifiTitle:         "Wireless networks",
		WifiHidden:        "Hidden network...",
		WifiOpen:          "Open",
		WifiEnterprise:    "Enterprise",
		WifiMarkOn:        "Connected",
		WifiMarkJoining:   "Connecting",
		WifiMarkKept:      "Saved",
		WifiScanning:      "Looking for networks...",
		WifiNone:          "No networks found",
		WifiDown:          "The wireless service is not answering",
		WifiStatusOn:      "Connected to %s",
		WifiStatusOff:     "Not connected",
		WifiStatusKept:    "saved: %s",
		WifiNoAddress:     "waiting for an address",
		WifiJoining:       "Connecting to %s...",
		WifiFailed:        "Could not connect to %s",
		WifiRejected:      "The network did not accept the password",
		WifiRejectedUser:  "The network did not accept the user name or password",
		WifiForgotten:     "%s forgotten",
		WifiNetwork:       "Network:",
		WifiName:          "Name:",
		WifiSecurity:      "Security:",
		WifiErrName:       "Type the network's name",
		WifiErrUser:       "Type the user name",
		WifiErrNoPassword: "Type the password",
		WifiErrPassword:   "A WPA password is 8 to 63 characters",
		WifiErrWEP:        "WEP networks are not supported",
		HintForget:        "Forget",
		HintBack:          "Back",

		HintUpdate:              "Update",
		UpdateTitle:             "Software update",
		UpdateThis:              "This version:",
		UpdateLatest:            "Latest:",
		UpdateNotChecked:        "not checked yet",
		UpdateChecking:          "checking...",
		UpdateUpToDate:          "This is the latest version.",
		UpdateReady:             "%s is out. Update downloads it and restarts into it; should it not start on this machine, the kiosk comes back on this version.",
		UpdateNoDisk:            "%s has no update for installed kiosks; it installs from a USB stick.",
		UpdateDidNotStart:       "%s did not start on this machine, so the kiosk went back to this version.",
		UpdateDownloading:       "Downloading %s: %d of %d MB",
		UpdateDownloadingNoSize: "Downloading %s: %d MB",
		UpdateRestarting:        "%s is installed. Restarting...",
		UpdateFailed:            "Update failed: %s",
		UpdateCheckFailed:       "Could not check: %s",
		UpdateErrNetwork:        "the release server cannot be reached (%s)",
		UpdateErrDamaged:        "the download was damaged; try again",
		UpdateErrStalled:        "the download stopped; check the network and try again",
		UpdateErrUnproven:       "this start-up is still being checked; try again in a minute",
		UpdateErrInsecure:       "the release server is not on HTTPS; updates come over HTTPS only",
		UpdateCancelled:         "cancelled",
		UpdateNew:               "New version %s (F9)",
		UpdateNoticeOK:          "Updated: %s is installed and running.",
		UpdateNoticeFailed:      "Update failed: %s did not start on this machine, so the kiosk went back to %s.",
		ButtonCheck:             "Check",
		ButtonUpdate:            "Update",
		ButtonClose:             "Close",
	},
	LangTR: {
		HostsFound:     "%d sunucu bulundu",
		HostsFoundOne:  "%d sunucu bulundu",
		Scanning:       "Taranıyor...",
		ScanComplete:   "Tarama tamamlandı",
		NoServersFound: "Sunucu bulunamadı",
		SelectServer:   "Bir sunucu seçin",
		ColumnServer:   "SUNUCU",
		ColumnAddress:  "ADRES",
		ColumnLatency:  "GECİKME",
		NoServersHint:  "%s ağında 3389 portunu dinleyen bir sunucu yok. F5 yeniden tarar, F7 adres girer, F8 ağ ayarlarını açar.",
		HintSelect:     "Seç",
		HintConnect:    "Bağlan",
		HintRefresh:    "Yenile",
		HintLanguage:   "Dil",
		HintKeyboard:   "Klavye",
		HintSound:      "Ses",
		BadgeSound:     "Ses",
		BadgeKeyboard:  "Klavye",
		BadgeLanguage:  "Dil",
		BadgeVideo:     "Video",
		VideoCPU:       "İşlemci",
		Tagline:        "UZAK MASAÜSTÜ İSTEMCİSİ",
		ModalTitle:     "Bağlantı Bilgileri",
		LabelServer:    "Sunucu:",
		LabelUsername:  "Kullanıcı:",
		LabelPassword:  "Parola:",
		LabelDomain:    "Etki alanı:",
		HintDomain:     "Etki alanı",
		ButtonConnect:  "Bağlan",
		ButtonCancel:   "İptal",
		Connecting:     "Bağlanıyor... %s",
		Disconnected:   "Bağlantı kesildi",
		ErrTimeout:     "Zaman aşımı: sunucuya ulaşılamıyor",
		ErrRefused:     "Bağlantı reddedildi",
		ErrAuthFailed:  "Kimlik doğrulama başarısız",
		ErrBadAddress:  "Sunucu adresi bulunamadı",
		LanguageName:   "Türkçe",

		HintAddress:           "Adres gir",
		HintInstall:           "Diske kur",
		InstallTitle:          "Diske kurulum",
		InstallPick:           "Kurulacak diski seçin:",
		InstallNoDisk:         "Kurulabilecek disk yok (en az 4 GB).",
		InstallErase:          "%s üzerindeki HER ŞEY silinecek.",
		InstallTable:          "Bölüm tablosu: %s",
		InstallKeys:           "Kurmak için %s, geri dönmek için Esc.",
		InstallLetter:         "E",
		InstallStepMedium:     "Kurulum ortamı okunuyor...",
		InstallStepPartition:  "Disk bölümleniyor...",
		InstallStepFormat:     "Dosya sistemleri oluşturuluyor...",
		InstallStepCopy:       "Sistem kopyalanıyor...",
		InstallStepBootloader: "Önyükleyici kuruluyor...",
		InstallStepFirmware:   "UEFI önyükleme kaydı ekleniyor...",
		InstallDone:           "Kurulum tamam. USB belleği veya CD'yi çıkarıp Enter'a basın.",
		InstallFailed:         "Kurulum başarısız: %s",
		InstallSelf:           "(çalışan bellek)",
		InstallSelfWarn:       "Kiosk bu bellekten çalışıyor: kurulacaklar önce RAM'e kopyalanır. Kurulum bitene kadar belleği çıkarmayın.",
		InstallDoneSelf:       "Bu belleğe kuruldu. Ondan yeniden başlatmak için Enter'a basın.",

		HintNetwork:    "Ağ",
		NetTitle:       "Ağ ayarları",
		NetMode:        "Bağlantı:",
		NetDHCP:        "DHCP (otomatik)",
		NetStatic:      "Manuel",
		NetAddress:     "IP adresi:",
		NetMask:        "Alt ağ maskesi:",
		NetGateway:     "Ağ geçidi:",
		NetDNS1:        "Tercih edilen DNS:",
		NetDNS2:        "Alternatif DNS:",
		NetSave:        "Kaydet",
		NetCable:       "%s: kablo takılı",
		NetNoCable:     "%s: kablo yok",
		NetNoAddress:   "adres yok",
		NetApplying:    "Uygulanıyor...",
		NetSaved:       "Kaydedildi.",
		NetSessionOnly: "Yeniden başlatılana dek uygulandı: CD'de ayar kaydedilecek yer yok.",
		NetErrAddress:  "IP adresi geçersiz",
		NetErrMask:     "Alt ağ maskesi geçersiz",
		NetErrGateway:  "Ağ geçidi bu alt ağda değil",
		NetErrDNS:      "DNS sunucusu adresi geçersiz",
		NetErrApply:    "Uygulanamadı: %s",

		HintAutoOff:    "Açılışta: sunucu listesi",
		HintAutoDialog: "Açılışta: bu sunucu",
		HintAutoLogon:  "Açılışta: oturum aç, parola cihazda saklanır",
		AutoCountdown:  "%d sn içinde oturum açılacak",
		AutoWaitNet:    "Ağ bekleniyor",
		AutoRetry:      "%d sn sonra yeniden denenecek",
		AutoKeys:       "Enter: hemen    Esc: sunucu listesi",

		NetWireless:       "Kablosuz:",
		NetWifiChoose:     "Ağ seçin",
		NetWifiOn:         "%s  ·  bağlı",
		NetWifiJoining:    "%s  ·  bağlanıyor",
		NetWifiOff:        "%s  ·  bağlı değil",
		NetIfWireless:     "%s: kablosuz",
		WifiTitle:         "Kablosuz ağlar",
		WifiHidden:        "Gizli ağ...",
		WifiOpen:          "Açık",
		WifiEnterprise:    "Kurumsal",
		WifiMarkOn:        "Bağlı",
		WifiMarkJoining:   "Bağlanıyor",
		WifiMarkKept:      "Kayıtlı",
		WifiScanning:      "Ağlar aranıyor...",
		WifiNone:          "Ağ bulunamadı",
		WifiDown:          "Kablosuz hizmeti yanıt vermiyor",
		WifiStatusOn:      "Bağlı: %s",
		WifiStatusOff:     "Bağlı değil",
		WifiStatusKept:    "kayıtlı: %s",
		WifiNoAddress:     "adres bekleniyor",
		WifiJoining:       "Bağlanılıyor: %s...",
		WifiFailed:        "Bağlanılamadı: %s",
		WifiRejected:      "Ağ parolayı kabul etmedi",
		WifiRejectedUser:  "Ağ kullanıcı adını veya parolayı kabul etmedi",
		WifiForgotten:     "Unutuldu: %s",
		WifiNetwork:       "Ağ:",
		WifiName:          "Ağ adı:",
		WifiSecurity:      "Güvenlik:",
		WifiErrName:       "Ağın adını yazın",
		WifiErrUser:       "Kullanıcı adını yazın",
		WifiErrNoPassword: "Parolayı yazın",
		WifiErrPassword:   "WPA parolası 8-63 karakterdir",
		WifiErrWEP:        "WEP ağları desteklenmiyor",
		HintForget:        "Unut",
		HintBack:          "Geri",

		HintUpdate:              "Güncelleme",
		UpdateTitle:             "Yazılım güncellemesi",
		UpdateThis:              "Bu sürüm:",
		UpdateLatest:            "Son sürüm:",
		UpdateNotChecked:        "henüz denetlenmedi",
		UpdateChecking:          "denetleniyor...",
		UpdateUpToDate:          "En son sürüm bu.",
		UpdateReady:             "%s çıktı. Güncelle onu indirir ve kiosk onunla yeniden başlar; bu makinede açılmazsa kiosk bu sürüme geri döner.",
		UpdateNoDisk:            "%s için diske kurulu kiosk güncellemesi yok; USB bellekten kurulur.",
		UpdateDidNotStart:       "%s bu makinede açılamadı; kiosk bu sürüme geri döndü.",
		UpdateDownloading:       "%s indiriliyor: %d / %d MB",
		UpdateDownloadingNoSize: "%s indiriliyor: %d MB",
		UpdateRestarting:        "%s kuruldu. Yeniden başlatılıyor...",
		UpdateFailed:            "Güncelleme başarısız: %s",
		UpdateCheckFailed:       "Denetlenemedi: %s",
		UpdateErrNetwork:        "sürüm sunucusuna ulaşılamıyor (%s)",
		UpdateErrDamaged:        "indirilen dosya bozuk; yeniden deneyin",
		UpdateErrStalled:        "indirme durdu; ağı denetleyip yeniden deneyin",
		UpdateErrUnproven:       "bu açılış henüz doğrulanıyor; bir dakika sonra yeniden deneyin",
		UpdateErrInsecure:       "sürüm sunucusu HTTPS değil; güncelleme yalnız HTTPS ile alınır",
		UpdateCancelled:         "iptal edildi",
		UpdateNew:               "Yeni sürüm %s (F9)",
		UpdateNoticeOK:          "Güncelleme başarılı: %s kuruldu ve çalışıyor.",
		UpdateNoticeFailed:      "Güncelleme başarısız: %s bu makinede açılamadı, kiosk %s sürümüne geri döndü.",
		ButtonCheck:             "Denetle",
		ButtonUpdate:            "Güncelle",
		ButtonClose:             "Kapat",
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

// HostCount renders the discovered-host count with the correct plural form.
// Plural rules belong here rather than in the UI: English needs a singular
// form, Turkish does not.
func HostCount(n int) string {
	if n == 1 {
		return Tf(HostsFoundOne, n)
	}
	return Tf(HostsFound, n)
}

// Tf returns the message for key formatted with args.
func Tf(key Key, args ...any) string {
	return fmt.Sprintf(T(key), args...)
}
