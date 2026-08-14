package ui

import "image/color"

// Colour palette for the SimpleClient kiosk UI.
//
// Three surfaces, each a clear step above the last — background, card, header
// strip — plus one accent. Kiosk panels are frequently cheap TN screens viewed
// off-axis, where a two-value difference between surfaces disappears entirely,
// so the steps here are deliberately wider than they would be on a desk
// monitor. Everything else in the palette is either state (latency, errors) or
// a shade of the accent.
var (
	ColorBG       = color.RGBA{R: 18, G: 19, B: 33, A: 255}    // #121321 screen
	ColorPanel    = color.RGBA{R: 26, G: 33, B: 60, A: 255}    // #1a213c card surface
	ColorAccent   = color.RGBA{R: 233, G: 69, B: 96, A: 255}   // #e94560 brand red
	ColorSelected = color.RGBA{R: 21, G: 58, B: 105, A: 255}   // #153a69 selected row
	ColorText     = color.RGBA{R: 226, G: 231, B: 242, A: 255} // #e2e7f2 primary
	ColorMuted    = color.RGBA{R: 140, G: 150, B: 178, A: 255} // #8c96b2 secondary
	ColorSuccess  = color.RGBA{R: 84, G: 214, B: 132, A: 255}  // #54d684 fast / done
	ColorWarning  = color.RGBA{R: 255, G: 196, B: 76, A: 255}  // #ffc44c middling
	ColorError    = color.RGBA{R: 240, G: 84, B: 84, A: 255}   // #f05454 slow / failed
	ColorBorder   = color.RGBA{R: 40, G: 52, B: 90, A: 255}    // #28345a hairlines
	ColorFocus    = ColorAccent                                // focused input ring
	ColorOverlay  = color.RGBA{A: 190}                         // scrim behind dialogs
	ColorBar      = ColorPanel
	ColorCursor   = color.RGBA{R: 255, G: 255, B: 255, A: 255} // pointer body
	ColorLatency  = ColorSuccess

	// Card and list.
	ColorCardHeader = color.RGBA{R: 33, G: 43, B: 76, A: 255}    // #212b4c header strip
	ColorCardShadow = color.RGBA{A: 110}                         // drop shadow
	ColorPanelEdge  = color.RGBA{R: 58, G: 73, B: 120, A: 255}   // 1px top bevel on panels
	ColorRowHover   = color.RGBA{R: 31, G: 39, B: 70, A: 255}    // banding for alternate rows
	ColorScrollBar  = color.RGBA{R: 72, G: 94, B: 148, A: 255}   // scrollbar thumb
	ColorTrack      = color.RGBA{R: 45, G: 57, B: 97, A: 255}    // scrollbar / meter track
	ColorDim        = color.RGBA{R: 112, G: 124, B: 158, A: 255} // headings, secondary text
	ColorFaint      = color.RGBA{R: 64, G: 78, B: 118, A: 255}   // the project URL, and nothing louder

	// Accent shades, for the trail behind the connecting indicator.
	ColorAccentDim   = color.RGBA{R: 148, G: 48, B: 66, A: 255}
	ColorAccentFaint = color.RGBA{R: 78, G: 32, B: 48, A: 255}

	// The error banner is a tinted surface with a red edge rather than a slab of
	// red. A full-width saturated bar under the card outshouted the card itself,
	// which is where the operator still has to act.
	ColorErrorBG     = color.RGBA{R: 56, G: 25, B: 38, A: 255}
	ColorErrorBorder = color.RGBA{R: 122, G: 44, B: 58, A: 255}

	// Logo sprite. Four fixed shades: the mark must look identical whatever the
	// rest of the screen is doing, so it does not borrow the surface colours.
	ColorLogoOutline = color.RGBA{R: 9, G: 10, B: 20, A: 255}    // sprite outline
	ColorLogoBezelHi = color.RGBA{R: 96, G: 112, B: 158, A: 255} // lit bezel edge
	ColorLogoBezelLo = color.RGBA{R: 46, G: 58, B: 98, A: 255}   // shaded bezel edge
	ColorLogoScreen  = color.RGBA{R: 14, G: 20, B: 42, A: 255}   // inside the bezel
	ColorLogoGlint   = color.RGBA{R: 32, G: 44, B: 82, A: 255}   // reflection on the glass
	ColorLogoShadow  = color.RGBA{R: 9, G: 10, B: 20, A: 255}    // hard drop shadow
)

// LatencyColor grades a round-trip time so the list can be scanned at a glance.
func LatencyColor(ms int64) color.RGBA {
	switch {
	case ms <= 0:
		return ColorMuted
	case ms < 50:
		return ColorSuccess
	case ms < 150:
		return ColorWarning
	default:
		return ColorError
	}
}
