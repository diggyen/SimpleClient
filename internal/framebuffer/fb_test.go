//go:build linux

package framebuffer

import (
	"image"
	"image/color"
	"testing"
	"unsafe"
)

func TestFBVarScreenInfoSize(t *testing.T) {
	var v fbVarScreenInfo
	size := unsafe.Sizeof(v)
	if size != 160 {
		t.Fatalf("fbVarScreenInfo size = %d, want 160", size)
	}
}

func TestMockFBBlit(t *testing.T) {
	fb := NewMock(100, 100)
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	img.SetRGBA(50, 50, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	fb.Blit(img)
	got := fb.Img.RGBAAt(50, 50)
	if got.R != 255 || got.G != 0 || got.B != 0 {
		t.Fatalf("unexpected pixel at (50,50): %v", got)
	}
}

func TestMockFBWritePixel(t *testing.T) {
	fb := NewMock(200, 200)
	fb.WritePixel(10, 20, color.RGBA{R: 0, G: 128, B: 255, A: 255})
	got := fb.Img.RGBAAt(10, 20)
	if got.G != 128 || got.B != 255 {
		t.Fatalf("unexpected pixel: %v", got)
	}
}
