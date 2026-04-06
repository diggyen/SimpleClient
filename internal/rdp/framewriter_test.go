package rdp

import (
	"image"
	"testing"
)

func TestScaleToFit(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	dst := scaleToFit(src, 1280, 720)
	if dst.Bounds().Dx() != 1280 || dst.Bounds().Dy() != 720 {
		t.Fatalf("expected 1280x720, got %dx%d", dst.Bounds().Dx(), dst.Bounds().Dy())
	}
}

func TestScaleToFit_SmallSrc(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 100))
	dst := scaleToFit(src, 1280, 720)
	if dst == nil {
		t.Fatal("scaleToFit returned nil")
	}
	if dst.Bounds().Dx() != 1280 {
		t.Fatalf("expected width 1280, got %d", dst.Bounds().Dx())
	}
}
