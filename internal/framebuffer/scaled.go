package framebuffer

import (
	"image"
	"image/color"
)

// Scale is how many screen pixels make one pixel of the kiosk's own screens on
// a display of w x h. They are laid out in pixels, for screens of about 1080p;
// with a GPU driver the screen is the display's native mode, and on 4K they
// would come out a quarter of the size. Whole numbers only, so every pixel
// stays a sharp square: 2 from 3840x2160, 3 from 5760x3240.
func Scale(w, h int) int {
	return max(1, min(w/1920, h/1080, 4))
}

// Scaled is a Device factor times smaller than the one under it, each of its
// pixels drawn as a factor x factor square.
type Scaled struct {
	dev    Device
	factor int
	w, h   int
	// wide is one row widened to the device, factor rows tall, repositioned
	// over the screen row being drawn.
	wide *image.RGBA
}

// NewScaled returns dev itself for a factor of 1.
func NewScaled(dev Device, factor int) Device {
	if factor <= 1 {
		return dev
	}
	return &Scaled{
		dev:    dev,
		factor: factor,
		w:      dev.Width() / factor,
		h:      dev.Height() / factor,
		wide:   image.NewRGBA(image.Rect(0, 0, dev.Width(), factor)),
	}
}

func (s *Scaled) Bounds() image.Rectangle { return image.Rect(0, 0, s.w, s.h) }
func (s *Scaled) Width() int              { return s.w }
func (s *Scaled) Height() int             { return s.h }
func (s *Scaled) Close() error            { return s.dev.Close() }

func (s *Scaled) Blit(img *image.RGBA) { s.BlitRect(img, img.Bounds()) }

func (s *Scaled) BlitRect(img *image.RGBA, r image.Rectangle) {
	r = r.Intersect(s.Bounds()).Intersect(img.Bounds())
	if r.Empty() {
		return
	}
	f := s.factor
	x0, n := r.Min.X*f*4, r.Dx()*f*4
	for y := r.Min.Y; y < r.Max.Y; y++ {
		src := img.Pix[img.PixOffset(r.Min.X, y):][:r.Dx()*4]
		row := s.wide.Pix[x0 : x0+n]
		for x, d := 0, 0; x < len(src); x += 4 {
			p0, p1, p2, p3 := src[x], src[x+1], src[x+2], src[x+3]
			for i := 0; i < f; i++ {
				row[d], row[d+1], row[d+2], row[d+3] = p0, p1, p2, p3
				d += 4
			}
		}
		for i := 1; i < f; i++ {
			copy(s.wide.Pix[i*s.wide.Stride+x0:][:n], row)
		}
		s.wide.Rect = image.Rect(0, y*f, s.dev.Width(), y*f+f)
		s.dev.BlitRect(s.wide, image.Rect(r.Min.X*f, y*f, r.Max.X*f, y*f+f))
	}
}

func (s *Scaled) WritePixel(x, y int, c color.RGBA) {
	if x < 0 || x >= s.w || y < 0 || y >= s.h {
		return
	}
	for dy := 0; dy < s.factor; dy++ {
		for dx := 0; dx < s.factor; dx++ {
			s.dev.WritePixel(x*s.factor+dx, y*s.factor+dy, c)
		}
	}
}
