package ui

import (
	"image"
	"time"

	"github.com/diggyen/SimpleClient/internal/framebuffer"
)

// The splash is what the screen shows while the kiosk boots: the mark, the
// wordmark and a spinner under them, which says the machine is busy rather
// than dead. Without it the screen stayed black from the boot menu to the
// first list, a minute from a slow USB stick.
//
// GRUB shows the same picture, still, while it reads the kernel and the
// initrds: build/splash.png is drawn from this (see TestSplashPNG). /init then
// runs RunSplash from the moment the kernel has started, so the spinner takes
// up where the boot loader's picture left off.

// SplashPNGWidth and SplashPNGHeight are the size of GRUB's copy: the mode
// grub.cfg sets, which the firmware framebuffer the kernel starts on keeps.
const (
	SplashPNGWidth  = 1024
	SplashPNGHeight = 768
)

// splashStep is how long the spinner's head stays on one cell.
const splashStep = 80 * time.Millisecond

// splashLayout places the parts of the splash.
type splashLayout struct {
	Mark, Word, Spinner image.Rectangle
	Scale, Cell         int
}

// layoutSplash centres the mark and the wordmark on a w x h screen, a little
// above the middle, with the spinner under them. The block is larger than the
// list's header: it is all there is on the screen.
func layoutSplash(w, h int) splashLayout {
	scale := max(2, min(w/160, h/120, 8))
	markSide := logoSpriteSize * scale
	gap := 5 * scale
	wordW := PixelTextWidth(logoWordA+logoWordB, scale)
	wordH := PixelTextHeight(scale)
	cell := 2 * scale
	spin := SpinnerSize(cell)
	spinGap := 10 * scale

	x := (w - (markSide + gap + wordW)) / 2
	y := (h - (markSide + spinGap + spin)) / 2
	wordX := x + markSide + gap
	wordY := y + (markSide-wordH)/2
	spinX := (w - spin) / 2
	spinY := y + markSide + spinGap
	return splashLayout{
		Mark:    image.Rect(x, y, x+markSide, y+markSide),
		Word:    image.Rect(wordX, wordY, wordX+wordW, wordY+wordH),
		Spinner: image.Rect(spinX, spinY, spinX+spin, spinY+spin),
		Scale:   scale,
		Cell:    cell,
	}
}

// RenderSplash draws the whole splash, with the spinner at tick.
func RenderSplash(img *image.RGBA, tick int) {
	l := layoutSplash(img.Bounds().Dx(), img.Bounds().Dy())
	FillRect(img, img.Bounds(), ColorBG)
	drawLogoMark(img, l.Mark.Min.X, l.Mark.Min.Y, l.Scale)
	drawWordmark(img, l.Word.Min.X, l.Word.Min.Y, l.Scale)
	DrawSpinner(img, l.Spinner.Min.X, l.Spinner.Min.Y, tick, l.Cell, ColorAccent)
}

// RunSplash shows the splash on fb and turns its spinner until stop is closed.
// The picture stays on the screen when it returns, until whatever comes next
// draws over it. Only the spinner is put on the screen again at each step.
func RunSplash(fb framebuffer.Device, stop <-chan struct{}) {
	back := image.NewRGBA(fb.Bounds())
	RenderSplash(back, 0)
	fb.Blit(back)
	l := layoutSplash(back.Bounds().Dx(), back.Bounds().Dy())

	t := time.NewTicker(splashStep)
	defer t.Stop()
	for tick := 1; ; tick++ {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		DrawSpinner(back, l.Spinner.Min.X, l.Spinner.Min.Y, tick, l.Cell, ColorAccent)
		fb.BlitRect(back, l.Spinner)
		if f, ok := fb.(interface{ Flush() }); ok {
			f.Flush()
		}
	}
}
