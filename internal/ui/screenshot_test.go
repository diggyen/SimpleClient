package ui

import (
	"image"
	"image/png"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/diggyen/SimpleClient/internal/domain"
	"github.com/diggyen/SimpleClient/internal/framebuffer"
	"github.com/diggyen/SimpleClient/internal/i18n"
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
	t.Cleanup(func() { i18n.Set(i18n.Default) })

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
	shot("06-credentials", 1280, 800, func(b *image.RGBA) {
		st := &UIState{
			Screen: ScreenModal, Hosts: sampleHosts(5), SelectedIdx: 1,
			ScanDone: true, ScanProgress: 1,
			Modal: ModalState{Fields: [3]string{"administrator", "secret123", ""}, FocusIdx: 1},
		}
		renderDiscovery(b, st)
		renderModal(b, st)
	})
	shot("07-connecting", 1280, 800, func(b *image.RGBA) {
		st := &UIState{
			Screen: ScreenConnecting, Hosts: sampleHosts(5), SelectedIdx: 1,
			ScanDone: true, ScanProgress: 1, SpinnerTick: 2,
		}
		renderDiscovery(b, st)
		renderConnecting(b, st)
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
	shot("08-turkish", 1280, 800, func(b *image.RGBA) {
		renderDiscovery(b, &UIState{
			Screen: ScreenDiscovery, Hosts: sampleHosts(5),
			SelectedIdx: 1, ScanDone: true, ScanProgress: 1,
		})
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
