package ui

import (
	"image"
	"net"
	"testing"
	"time"

	"github.com/diggyen/SimpleClient/internal/domain"
	"github.com/diggyen/SimpleClient/internal/framebuffer"
)

func TestRenderDiscovery_Empty(t *testing.T) {
	fb := framebuffer.NewMock(1280, 720)
	back := image.NewRGBA(fb.Bounds())
	state := &UIState{Screen: ScreenDiscovery}
	Render(fb, back, state, 640, 360)

	// Top bar and bottom bar should be non-background-colored pixels.
	topPx := back.RGBAAt(10, 10)
	if topPx == ColorBG {
		t.Fatal("top bar should not be same as background color")
	}
}

func TestRenderDiscovery_ThreeHosts(t *testing.T) {
	fb := framebuffer.NewMock(1280, 720)
	back := image.NewRGBA(fb.Bounds())
	state := &UIState{
		Screen: ScreenDiscovery,
		Hosts: []domain.Host{
			{IP: net.ParseIP("192.168.1.1"), Hostname: "server1", DiscoveredAt: time.Now()},
			{IP: net.ParseIP("192.168.1.2"), Hostname: "server2", DiscoveredAt: time.Now()},
			{IP: net.ParseIP("192.168.1.3"), Hostname: "server3", DiscoveredAt: time.Now()},
		},
	}
	Render(fb, back, state, 0, 0)

	// The list area should have non-bg pixels (host rows rendered).
	listY := barH + 10
	hasContent := false
	for x := 10; x < 300; x++ {
		if back.RGBAAt(x, listY) != ColorBG {
			hasContent = true
			break
		}
	}
	if !hasContent {
		t.Fatal("discovery screen with 3 hosts should have content in list area")
	}
}

func TestRenderDiscovery_SelectedRow(t *testing.T) {
	fb := framebuffer.NewMock(1280, 720)
	back := image.NewRGBA(fb.Bounds())
	state := &UIState{
		Screen:      ScreenDiscovery,
		SelectedIdx: 0,
		Hosts: []domain.Host{
			{IP: net.ParseIP("10.0.0.1"), DiscoveredAt: time.Now()},
		},
	}
	renderDiscovery(back, state)

	// Row 0 at listTop+4 should be highlighted (ColorSelected or border).
	rowY := barH + 2 + 4
	rowPx := back.RGBAAt(10, rowY)
	if rowPx == ColorBG {
		t.Fatal("selected row should not be plain background color")
	}
}

func TestRenderModal_NonEmpty(t *testing.T) {
	fb := framebuffer.NewMock(1280, 720)
	back := image.NewRGBA(fb.Bounds())
	state := &UIState{Screen: ScreenModal}
	renderDiscovery(back, state)
	renderModal(back, state)

	// Centre of modal should not be background.
	cx, cy := 640, 360
	px := back.RGBAAt(cx, cy)
	if px == ColorBG {
		t.Fatal("modal centre should not be plain background color")
	}
}
