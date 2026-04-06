package ui

import (
	"sync"

	"github.com/diggyen/SimpleClient/internal/domain"
)

// Screen identifies which UI screen is currently active.
type Screen int

const (
	ScreenDiscovery  Screen = iota // Host list / scanning
	ScreenModal                    // Credential entry modal
	ScreenConnecting               // "Connecting..." overlay
	ScreenSession                  // Active RDP session
)

// ModalState holds the state of the credential entry modal.
type ModalState struct {
	Fields   [3]string // 0=Username, 1=Password, 2=Domain
	FocusIdx int       // 0=user, 1=pass, 2=domain, 3=connect button, 4=cancel button
	Error    string    // Error message to display
}

// UIState is the complete mutable state of the UI layer.
type UIState struct {
	Mu           sync.Mutex
	Screen       Screen
	Hosts        []domain.Host
	SelectedIdx  int
	ScrollOffset int
	ScanProgress float64 // [0, 1]
	ScanDone     bool
	Modal        ModalState
	ErrorMsg     string // Transient info/error banner
	MouseX       int
	MouseY       int
	SpinnerTick  int // Used for connecting-screen spinner animation
}

// Transition moves the UI to a new screen.
// Only whitelisted transitions are allowed per IMPLEMENTATION.md 2.1.
func (s *UIState) Transition(to Screen) {
	allowed := map[Screen][]Screen{
		ScreenDiscovery:  {ScreenModal},
		ScreenModal:      {ScreenDiscovery, ScreenConnecting},
		ScreenConnecting: {ScreenSession, ScreenModal},
		ScreenSession:    {ScreenDiscovery},
	}
	for _, valid := range allowed[s.Screen] {
		if valid == to {
			s.Screen = to
			return
		}
	}
	// Allow same-screen transitions (no-op) and any transition to Discovery (fallback).
	if to == s.Screen || to == ScreenDiscovery {
		s.Screen = to
	}
}

// SelectedHost returns a pointer to the currently selected host, or nil.
func (s *UIState) SelectedHost() *domain.Host {
	if len(s.Hosts) == 0 {
		return nil
	}
	idx := s.SelectedIdx
	if idx < 0 {
		idx = 0
	}
	if idx >= len(s.Hosts) {
		idx = len(s.Hosts) - 1
	}
	return &s.Hosts[idx]
}

// VisibleHosts returns the slice of hosts that should be rendered given the
// current scroll offset and maximum displayable rows.
func (s *UIState) VisibleHosts(maxRows int) []domain.Host {
	if len(s.Hosts) == 0 {
		return nil
	}
	start := s.ScrollOffset
	if start < 0 {
		start = 0
	}
	end := start + maxRows
	if end > len(s.Hosts) {
		end = len(s.Hosts)
	}
	return s.Hosts[start:end]
}

// HandleScanEvent updates state in response to a scanner event.
func (s *UIState) HandleScanEvent(ev domain.ScanEvent) {
	switch ev.Type {
	case domain.EventHostFound:
		if ev.Host != nil {
			// Append and deduplicate by IP.
			existing := false
			for i, h := range s.Hosts {
				if h.IP.Equal(ev.Host.IP) {
					s.Hosts[i] = *ev.Host
					existing = true
					break
				}
			}
			if !existing {
				s.Hosts = append(s.Hosts, *ev.Host)
			}
		}

	case domain.EventScanProgress:
		if ev.Total > 0 {
			s.ScanProgress = float64(ev.Scanned) / float64(ev.Total)
		}

	case domain.EventScanComplete:
		s.ScanProgress = 1.0
		s.ScanDone = true
	}
}

// MoveSelection moves the host list cursor by delta, clamped to valid range.
func (s *UIState) MoveSelection(delta, maxRows int) {
	s.SelectedIdx += delta
	if s.SelectedIdx < 0 {
		s.SelectedIdx = 0
	}
	if s.SelectedIdx >= len(s.Hosts) {
		s.SelectedIdx = len(s.Hosts) - 1
	}
	// Adjust scroll so cursor stays visible.
	if s.SelectedIdx < s.ScrollOffset {
		s.ScrollOffset = s.SelectedIdx
	}
	if s.SelectedIdx >= s.ScrollOffset+maxRows {
		s.ScrollOffset = s.SelectedIdx - maxRows + 1
	}
}
