package rdp

import (
	"image"
	"image/color"
	"image/draw"

	"github.com/kullanici/rdpboot/internal/framebuffer"
	xdraw "golang.org/x/image/draw"
)

// FrameWriter receives RDP video frames and writes them to the framebuffer.
type FrameWriter struct {
	FB framebuffer.Device
}

// Write scales img to fit the framebuffer and blits it.
func (fw *FrameWriter) Write(img image.Image) {
	if img == nil {
		return
	}
	bounds := fw.FB.Bounds()
	scaled := scaleToFit(img, bounds.Dx(), bounds.Dy())
	fw.FB.Blit(scaled)
}

// scaleToFit scales src to (w, h) using BiLinear interpolation and returns
// the result as *image.RGBA.
func scaleToFit(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	// Fill with black first.
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	xdraw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)
	return dst
}
