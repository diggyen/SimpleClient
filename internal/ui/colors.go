package ui

import "image/color"

// Color palette for the SimpleClient kiosk UI.
// Matches IMPLEMENTATION.md 6.1 specification.
var (
	ColorBG       = color.RGBA{R: 26, G: 26, B: 46, A: 255}    // #1a1a2e dark navy
	ColorPanel    = color.RGBA{R: 22, G: 33, B: 62, A: 255}    // #16213e
	ColorAccent   = color.RGBA{R: 233, G: 69, B: 96, A: 255}   // #e94560 red accent
	ColorSelected = color.RGBA{R: 15, G: 52, B: 96, A: 255}    // #0f3460
	ColorText     = color.RGBA{R: 224, G: 224, B: 224, A: 255} // #e0e0e0
	ColorMuted    = color.RGBA{R: 136, G: 136, B: 136, A: 255} // #888888
	ColorSuccess  = color.RGBA{R: 100, G: 221, B: 100, A: 255} // connected
	ColorWarning  = color.RGBA{R: 255, G: 200, B: 50, A: 255}  // Yellow warning
	ColorError    = color.RGBA{R: 255, G: 80, B: 80, A: 255}   // Red error
	ColorBorder   = color.RGBA{R: 30, G: 40, B: 70, A: 255}    // #1e2846
	ColorFocus    = color.RGBA{R: 233, G: 69, B: 96, A: 255}   // = ColorAccent for focused input
	ColorOverlay  = color.RGBA{R: 0, G: 0, B: 0, A: 180}       // Semi-transparent overlay
	ColorBar      = color.RGBA{R: 22, G: 33, B: 62, A: 255}    // = ColorPanel
	ColorCursor   = color.RGBA{R: 255, G: 255, B: 100, A: 255} // #ffff64
	ColorLatency  = color.RGBA{R: 100, G: 221, B: 100, A: 255} // = ColorSuccess
)
