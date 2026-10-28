package ui

import (
	"image"
	"image/png"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/diggyen/SimpleClient/internal/autoconnect"
	"github.com/diggyen/SimpleClient/internal/domain"
	"github.com/diggyen/SimpleClient/internal/framebuffer"
	"github.com/diggyen/SimpleClient/internal/i18n"
	"github.com/diggyen/SimpleClient/internal/inputdev"
	"github.com/diggyen/SimpleClient/internal/netconf"
	"github.com/diggyen/SimpleClient/internal/wifi"
)

// TestScreenshots renders every UI state to PNG so the design can be reviewed
// without booting the image. Skipped unless a destination is given.
//
// docs/screenshots/ holds the committed set; regenerate it in place after any
// change to the rendering, and give an absolute path — the test runs with
// internal/ui as its working directory, so a relative one lands there instead:
//
//	SIMPLECLIENT_UI_SHOTS=$PWD/docs/screenshots go test ./internal/ui -run TestScreenshots
func TestScreenshots(t *testing.T) {
	dir := os.Getenv("SIMPLECLIENT_UI_SHOTS")
	if dir == "" {
		t.Skip("SIMPLECLIENT_UI_SHOTS not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// From the defaults, whatever the tests before this one left switched.
	i18n.Set(i18n.Default)
	inputdev.SetLayout(inputdev.LayoutUS)
	t.Cleanup(func() {
		i18n.Set(i18n.Default)
		inputdev.SetLayout(inputdev.LayoutUS)
	})

	shot := func(name string, w, h int, draw func(*image.RGBA)) {
		t.Helper()
		fb := framebuffer.NewMock(w, h)
		img := image.NewRGBA(fb.Bounds())
		draw(img)

		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}

	// The mark is 64px on screen, which is too small to judge a sprite by. This
	// blows it up so individual pixels can be reviewed.
	shot("00-logo", 640, 320, func(b *image.RGBA) {
		FillRect(b, b.Bounds(), ColorBG)
		drawLogoMark(b, 40, 40, 12)
		DrawPixelTextShadowed(b, 40, 250, logoWordA, 4, ColorText, ColorLogoShadow)
		DrawPixelTextShadowed(b, 40+PixelTextWidth(logoWordA, 4)+4, 250, logoWordB, 4,
			ColorAccent, ColorLogoShadow)
	})

	shot("01-scanning", 1280, 800, func(b *image.RGBA) {
		renderDiscovery(b, &UIState{Screen: ScreenDiscovery, ScanProgress: 0.34, SpinnerTick: 1})
	})
	shot("02-list", 1280, 800, func(b *image.RGBA) {
		renderDiscovery(b, &UIState{
			Screen: ScreenDiscovery, Hosts: sampleHosts(7),
			SelectedIdx: 2, ScanDone: true, ScanProgress: 1,
		})
	})
	shot("03-scrolling", 1024, 768, func(b *image.RGBA) {
		renderDiscovery(b, &UIState{
			Screen: ScreenDiscovery, Hosts: sampleHosts(40),
			SelectedIdx: 9, ScrollOffset: 4, ScanDone: true, ScanProgress: 1,
		})
	})
	shot("04-empty", 1280, 800, func(b *image.RGBA) {
		renderDiscovery(b, &UIState{Screen: ScreenDiscovery, ScanDone: true, ScanProgress: 1})
	})
	shot("05-error", 1280, 800, func(b *image.RGBA) {
		renderDiscovery(b, &UIState{
			Screen: ScreenDiscovery, Hosts: sampleHosts(3),
			ScanDone: true, ScanProgress: 1, ErrorMsg: i18n.T(i18n.Disconnected),
		})
	})
	shot("05-error-full", 1366, 768, func(b *image.RGBA) {
		renderDiscovery(b, &UIState{
			Screen: ScreenDiscovery, Hosts: sampleHosts(21),
			ScanDone: true, ScanProgress: 1, ErrorMsg: i18n.T(i18n.Disconnected),
		})
	})
	shot("06-credentials", 1280, 800, func(b *image.RGBA) {
		st := &UIState{
			Screen: ScreenModal, Hosts: sampleHosts(5), SelectedIdx: 1,
			ScanDone: true, ScanProgress: 1,
			Modal: ModalState{Fields: [4]string{"10.7.7.101", "administrator", "secret123", ""}, FocusIdx: fieldPassword},
		}
		renderDiscovery(b, st)
		renderModal(b, st)
	})
	shot("06-credentials-domain", 1280, 800, func(b *image.RGBA) {
		st := &UIState{
			Screen: ScreenModal, Hosts: sampleHosts(5), SelectedIdx: 1,
			ScanDone: true, ScanProgress: 1,
			Modal: ModalState{Fields: [4]string{"10.7.7.101", "administrator", "secret123", "CORP"},
				ShowDomain: true, FocusIdx: fieldDomain},
		}
		renderDiscovery(b, st)
		renderModal(b, st)
	})
	shot("06-network", 1280, 800, func(b *image.RGBA) {
		st := &UIState{
			Screen: ScreenNetwork, Hosts: sampleHosts(5), SelectedIdx: 1,
			ScanDone: true, ScanProgress: 1,
			Net: NetState{
				Static: true, Iface: "eth0", Focus: 1 + netGateway, Port: "3389",
				Fields: [netFields]string{"192.168.1.50", "255.255.255.0", "192.168.1.1", "192.168.1.1", "1.1.1.1"},
				Status: netconf.Status{Interface: "eth0", Address: "192.168.1.50/24", Carrier: true},
				Msg:    i18n.T(i18n.NetSaved),
			},
		}
		renderDiscovery(b, st)
		renderNet(b, st)
	})
	shot("06-network-dhcp", 1280, 800, func(b *image.RGBA) {
		st := &UIState{
			Screen: ScreenNetwork, Hosts: sampleHosts(5), SelectedIdx: 1,
			ScanDone: true, ScanProgress: 1,
			Net: NetState{
				Iface: "eth0", Focus: netFocusPort, Port: "3390",
				Status: netconf.Status{Interface: "eth0", Address: "10.7.7.53/24", Carrier: true},
			},
		}
		renderDiscovery(b, st)
		renderNet(b, st)
	})
	isw := wifiIsWireless
	wifiIsWireless = func(i string) bool { return i == "wlan0" }
	t.Cleanup(func() { wifiIsWireless = isw })
	wifiShot := func(w WifiState) *UIState {
		w.Iface = "wlan0"
		w.Networks = []wifi.Network{
			{SSID: "Office", Signal: -48, Security: wifi.PSK},
			{SSID: "Office-5G", Signal: -53, Security: wifi.SAE},
			{SSID: "CORP-WLAN", Signal: -61, Security: wifi.EAP},
			{SSID: "Guest", Signal: -70, Security: wifi.Open},
			{SSID: "DIRECT-7F-HP LaserJet", Signal: -78, Security: wifi.PSK},
			{SSID: "Old router", Signal: -86, Security: wifi.WEP},
		}
		w.Kept = wifi.Config{SSID: "Office", Security: wifi.PSK}
		w.Status = wifi.Status{State: "COMPLETED", SSID: "Office"}
		w.Address = "192.168.20.31/24"
		return &UIState{
			Screen: ScreenWifi, Hosts: sampleHosts(5), SelectedIdx: 1,
			ScanDone: true, ScanProgress: 1, Wifi: w,
		}
	}
	shot("06-network-wifi", 1280, 800, func(b *image.RGBA) {
		st := wifiShot(WifiState{})
		st.Screen = ScreenNetwork
		st.Net = NetState{
			Iface: "wlan0", Focus: netFocusWifi, Wireless: "wlan0", Port: "3389",
			Status: netconf.Status{Interface: "wlan0", Address: "192.168.20.31/24", Carrier: true},
		}
		renderDiscovery(b, st)
		renderNet(b, st)
	})
	shot("12-wifi", 1280, 800, func(b *image.RGBA) {
		st := wifiShot(WifiState{Sel: 2})
		renderDiscovery(b, st)
		renderWifi(b, st)
	})
	shot("12-wifi-password", 1280, 800, func(b *image.RGBA) {
		st := wifiShot(WifiState{})
		st.Wifi.ask(st.Wifi.Networks[2], false)
		st.Wifi.Fields[itemUser] = `CORP\ayse`
		st.Wifi.Fields[itemPassword] = "secret123"
		st.Wifi.Focus = itemPassword
		renderDiscovery(b, st)
		renderWifi(b, st)
	})
	shot("12-wifi-hidden", 1280, 800, func(b *image.RGBA) {
		st := wifiShot(WifiState{})
		st.Wifi.ask(wifi.Network{Security: wifi.PSK}, true)
		st.Wifi.Fields[itemName] = "Warehouse"
		st.Wifi.Focus = itemSecurity
		renderDiscovery(b, st)
		renderWifi(b, st)
	})
	shot("07-connecting", 1280, 800, func(b *image.RGBA) {
		st := &UIState{
			Screen: ScreenConnecting, Hosts: sampleHosts(5), SelectedIdx: 1,
			ScanDone: true, ScanProgress: 1, SpinnerTick: 2,
		}
		renderDiscovery(b, st)
		renderConnecting(b, st)
	})
	shot("06-credentials-startup", 1280, 800, func(b *image.RGBA) {
		st := &UIState{
			Screen: ScreenModal, Hosts: sampleHosts(5), SelectedIdx: 1,
			ScanDone: true, ScanProgress: 1,
			Modal: ModalState{Fields: [4]string{"10.7.7.101", "kiosk", "secret123", ""},
				FocusIdx: fieldPassword, Auto: autoconnect.Logon},
		}
		renderDiscovery(b, st)
		renderModal(b, st)
	})
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	clock = func() time.Time { return now }
	t.Cleanup(func() { clock = time.Now })
	shot("07-startup-countdown", 1280, 800, func(b *image.RGBA) {
		st := &UIState{
			Screen: ScreenAuto, Hosts: sampleHosts(5), SelectedIdx: 1,
			ScanDone: true, ScanProgress: 1, SpinnerTick: 2,
			AutoAt: now.Add(4 * time.Second), AutoNet: true,
		}
		st.Modal.Fields[fieldServer] = st.Hosts[1].Address()
		renderDiscovery(b, st)
		renderAuto(b, st)
	})

	// The narrowest panel the kiosk is expected to boot on. Everything has to
	// fit here before it is worth judging at 1280.
	// No reverse DNS: the shape a flat office network produces, and the one the
	// card is narrowed for.
	shot("11-bare-ips", 1280, 800, func(b *image.RGBA) {
		hosts := sampleHosts(5)
		for i := range hosts {
			hosts[i].Hostname = ""
		}
		renderDiscovery(b, &UIState{
			Screen: ScreenDiscovery, Hosts: hosts,
			SelectedIdx: 1, ScanDone: true, ScanProgress: 1,
		})
	})

	shot("09-640x480", 640, 480, func(b *image.RGBA) {
		renderDiscovery(b, &UIState{
			Screen: ScreenDiscovery, Hosts: sampleHosts(6),
			SelectedIdx: 1, ScanDone: true, ScanProgress: 1,
		})
	})

	i18n.Set(i18n.LangTR)
	inputdev.SetLayout(inputdev.LayoutForLanguage(string(i18n.LangTR)))
	shot("08-turkish", 1280, 800, func(b *image.RGBA) {
		renderDiscovery(b, &UIState{
			Screen: ScreenDiscovery, Hosts: sampleHosts(5),
			SelectedIdx: 1, ScanDone: true, ScanProgress: 1,
		})
	})
	shot("08-turkish-startup", 1280, 800, func(b *image.RGBA) {
		st := &UIState{
			Screen: ScreenModal, Hosts: sampleHosts(5), SelectedIdx: 1,
			ScanDone: true, ScanProgress: 1,
			Modal: ModalState{Fields: [4]string{"10.7.7.101", "kiosk", "secret123", ""},
				FocusIdx: fieldPassword, Auto: autoconnect.Logon},
		}
		renderDiscovery(b, st)
		renderModal(b, st)
	})
	shot("08-turkish-wifi", 1280, 800, func(b *image.RGBA) {
		st := wifiShot(WifiState{Sel: 1, Msg: i18n.T(i18n.NetSaved)})
		renderDiscovery(b, st)
		renderWifi(b, st)
	})
	shot("08-turkish-power", 1280, 800, func(b *image.RGBA) {
		st := &UIState{
			Screen: ScreenPower, Hosts: sampleHosts(5), SelectedIdx: 1,
			ScanDone: true, ScanProgress: 1,
			Power: PowerState{Focus: powerFocusCancel, Going: powerNone},
		}
		renderDiscovery(b, st)
		renderPower(b, st)
	})
	shot("08-turkish-power-driver", 1280, 800, func(b *image.RGBA) {
		st := &UIState{
			Screen: ScreenPower, Hosts: sampleHosts(5), SelectedIdx: 1,
			ScanDone: true, ScanProgress: 1,
			Power: PowerState{Focus: powerFocusCancel, Going: powerNone, Driver: true},
		}
		renderDiscovery(b, st)
		renderPower(b, st)
	})

	t.Logf("wrote screenshots to %s", dir)
}

func sampleHosts(n int) []domain.Host {
	names := []string{"veeam-2", "dc01", "", "fileserver", "", "sql-prod", "rds-gw", "app-01", "", "backup-02"}
	latency := []int64{3, 12, 47, 88, 132, 210, 6, 19, 340, 61}

	out := make([]domain.Host, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, domain.Host{
			IP:           net.IPv4(10, 7, 7, byte(100+i)),
			Hostname:     names[i%len(names)],
			LatencyMs:    latency[i%len(latency)],
			DiscoveredAt: time.Now(),
		})
	}
	return out
}
