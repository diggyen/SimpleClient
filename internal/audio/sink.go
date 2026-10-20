package audio

import (
	"sync"
	"time"
)

// rescanEvery is how often the playback endpoint is re-chosen while sound is
// playing, so a USB headset plugged in mid-session, or headphones pushed into
// the jack, take over within a couple of seconds.
const rescanEvery = 2 * time.Second

// Sink connects the RDP sound channels to the hardware: it plays to the
// selected output and records from the selected input.
type Sink struct {
	Sel *Selector

	out Output
	in  Input

	mu       sync.Mutex
	dev      Device
	haveDev  bool
	lastScan time.Time
}

// NewSink returns a sink choosing endpoints with sel.
func NewSink(sel *Selector) *Sink { return &Sink{Sel: sel} }

// Play implements rdpsnd.Player.
func (s *Sink) Play(f Format, pcm []byte) error {
	s.mu.Lock()
	if !s.haveDev || time.Since(s.lastScan) > rescanEvery {
		s.dev, s.haveDev = s.Sel.Output()
		s.lastScan = time.Now()
	}
	dev, ok := s.dev, s.haveDev
	s.mu.Unlock()
	if !ok {
		return nil // no sound hardware: play into the void
	}
	ensureMixer(dev.Card)
	return s.out.Play(dev, f, pcm)
}

// Stop implements rdpsnd.Player.
func (s *Sink) Stop() {
	s.out.Stop()
	s.mu.Lock()
	s.haveDev = false
	s.mu.Unlock()
}

// Mic returns the recorder half of the sink, for the microphone channel.
func (s *Sink) Mic() Mic { return Mic{s} }

// Mic records from the selected input; it implements dvc.Recorder.
type Mic struct{ s *Sink }

// Start begins recording f, delivering packets of packetBytes.
func (m Mic) Start(f Format, packetBytes int, deliver func([]byte)) error {
	dev, ok := m.s.Sel.Input()
	if !ok {
		return errNoInput
	}
	ensureMixer(dev.Card)
	return m.s.in.Start(dev, f, packetBytes, deliver)
}

// Stop ends recording.
func (m Mic) Stop() { m.s.in.Stop() }

type sinkError string

func (e sinkError) Error() string { return string(e) }

const errNoInput = sinkError("no microphone found")
