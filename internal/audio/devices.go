package audio

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Kind classifies a sound endpoint the way a user thinks of it.
type Kind int

const (
	KindAnalog Kind = iota // the jack and built-in speaker on the board
	KindHDMI               // a monitor's speakers over HDMI or DisplayPort
	KindUSB                // a USB headset, speaker or sound card
)

func (k Kind) String() string {
	switch k {
	case KindHDMI:
		return "HDMI"
	case KindUSB:
		return "USB"
	}
	return "Analog"
}

// Device is one ALSA PCM endpoint.
type Device struct {
	Card, Dev int
	CardID    string // e.g. "PCH", stable across reboots
	Name      string // the PCM's own name, e.g. "ALC662 rev3 Analog"
	Kind      Kind
	Playback  bool
	Capture   bool
	// Monitor reports, for HDMI, that a display is attached that has
	// speakers (its EDID lists audio formats).
	Monitor bool
}

// ALSA is the device string aplay/arecord open. plughw converts rate,
// channels and sample format to what the hardware accepts.
func (d Device) ALSA() string {
	return fmt.Sprintf("plughw:%d,%d", d.Card, d.Dev)
}

// Label is a short name for the on-screen indicator.
func (d Device) Label() string {
	return d.Kind.String()
}

// String describes the endpoint for the log, e.g.
// `plughw:0,0 Analog "ALC662 rev3 Analog" play rec`.
func (d Device) String() string {
	s := fmt.Sprintf("%s %s %q", d.ALSA(), d.Kind, d.Name)
	if d.Playback {
		s += " play"
	}
	if d.Capture {
		s += " rec"
	}
	if d.Monitor {
		s += " monitor"
	}
	return s
}

// procRoot is where /proc/asound is read from; tests point it elsewhere.
var procRoot = "/proc/asound"

// Scan lists every PCM endpoint the kernel has.
func Scan() []Device {
	f, err := os.Open(filepath.Join(procRoot, "pcm"))
	if err != nil {
		return nil
	}
	defer f.Close()

	ids := cardIDs()
	var devs []Device
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		// "00-03: HDMI 0 : HDMI 0 : playback 1"
		line := sc.Text()
		head, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		cs, ds, ok := strings.Cut(strings.TrimSpace(head), "-")
		if !ok {
			continue
		}
		card, err1 := strconv.Atoi(cs)
		dev, err2 := strconv.Atoi(ds)
		if err1 != nil || err2 != nil {
			continue
		}
		if ids[card] == "pcsp" {
			// The PC speaker driver: a buzzer on the board, not a sound output.
			continue
		}
		parts := strings.Split(rest, ":")
		d := Device{Card: card, Dev: dev, CardID: ids[card], Name: strings.TrimSpace(parts[0])}
		for _, p := range parts[1:] {
			p = strings.TrimSpace(p)
			d.Playback = d.Playback || strings.HasPrefix(p, "playback")
			d.Capture = d.Capture || strings.HasPrefix(p, "capture")
		}
		switch {
		case isUSBCard(card):
			d.Kind = KindUSB
		case isHDMIName(d.Name):
			d.Kind = KindHDMI
		}
		devs = append(devs, d)
	}

	// Which HDMI PCM a monitor sits on is not exposed directly. The ELD files
	// come in pin order and the PCMs in device order, which line up on every
	// common codec; the n-th connected pin with audio is taken to be the n-th
	// HDMI PCM. A wrong guess is one F4 press away from fixed.
	for card := range ids {
		var hdmi []*Device
		for i := range devs {
			if devs[i].Card == card && devs[i].Kind == KindHDMI {
				hdmi = append(hdmi, &devs[i])
			}
		}
		for i, present := range eldPresence(card) {
			if present && i < len(hdmi) {
				hdmi[i].Monitor = true
			}
		}
	}
	return devs
}

// cardIDs maps card numbers to their ids from /proc/asound/cards.
func cardIDs() map[int]string {
	ids := map[int]string{}
	data, err := os.ReadFile(filepath.Join(procRoot, "cards"))
	if err != nil {
		return ids
	}
	for _, line := range strings.Split(string(data), "\n") {
		// " 0 [PCH            ]: HDA-Intel - HDA Intel PCH"
		f := strings.Fields(line)
		if len(f) < 2 || !strings.HasPrefix(f[1], "[") {
			continue
		}
		n, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		id := strings.TrimPrefix(f[1], "[")
		id = strings.TrimSuffix(id, "]:")
		id = strings.TrimSuffix(id, "]")
		ids[n] = id
	}
	return ids
}

func isUSBCard(card int) bool {
	_, err := os.Stat(filepath.Join(procRoot, fmt.Sprintf("card%d", card), "usbid"))
	return err == nil
}

func isHDMIName(name string) bool {
	n := strings.ToUpper(name)
	return strings.Contains(n, "HDMI") || strings.Contains(n, "DISPLAYPORT") ||
		strings.HasPrefix(n, "DP ") || strings.Contains(n, " DP")
}

// eldPresence reports, per HDMI pin in order, whether a monitor with speakers
// is attached.
func eldPresence(card int) []bool {
	files, _ := filepath.Glob(filepath.Join(procRoot, fmt.Sprintf("card%d", card), "eld#*"))
	sort.Strings(files)
	var out []bool
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			out = append(out, false)
			continue
		}
		v := map[string]string{}
		for _, line := range strings.Split(string(data), "\n") {
			k, val, ok := strings.Cut(line, "\t")
			if ok {
				v[strings.TrimSpace(k)] = strings.TrimSpace(val)
			}
		}
		sads, _ := strconv.Atoi(v["sad_count"])
		out = append(out, v["monitor_present"] == "1" && v["eld_valid"] == "1" && sads > 0)
	}
	return out
}

// Selector picks the playback and capture endpoints, and lets the user
// override the playback choice at runtime.
type Selector struct {
	mu       sync.Mutex
	override string // CardID,Dev of a user-chosen output, "" for automatic
}

// headphonesIn is replaced on Linux by a check of the codec's jack switches.
var headphonesIn = func() bool { return false }

// Output returns the playback endpoint to use, or ok=false when there is none.
//
// Automatic choice, in order: a USB headset or speaker (plugging one in is an
// explicit choice); the analog jack when headphones are in it; a monitor with
// speakers on HDMI/DisplayPort; the analog output; anything that plays.
func (s *Selector) Output() (Device, bool) {
	devs := Scan()
	s.mu.Lock()
	override := s.override
	s.mu.Unlock()
	if override != "" {
		for _, d := range devs {
			if d.Playback && key(d) == override {
				return d, true
			}
		}
	}
	return pickOutput(devs, headphonesIn())
}

func pickOutput(devs []Device, phones bool) (Device, bool) {
	rank := func(d Device) int {
		switch {
		case d.Kind == KindUSB:
			return 0
		case d.Kind == KindAnalog && phones:
			return 1
		case d.Kind == KindHDMI && d.Monitor:
			return 2
		case d.Kind == KindAnalog:
			return 3
		}
		return 4
	}
	best, found := Device{}, false
	for _, d := range devs {
		if !d.Playback {
			continue
		}
		if !found || rank(d) < rank(best) {
			best, found = d, true
		}
	}
	return best, found
}

// Input returns the capture endpoint: a USB headset's microphone if there is
// one, otherwise the board's.
func (s *Selector) Input() (Device, bool) {
	best, found := Device{}, false
	for _, d := range Scan() {
		if !d.Capture || d.Kind == KindHDMI {
			continue
		}
		if !found || (d.Kind == KindUSB && best.Kind != KindUSB) {
			best, found = d, true
		}
	}
	return best, found
}

// Cycle moves the playback choice to the next endpoint and returns it.
func (s *Selector) Cycle() (Device, bool) {
	cur, ok := s.Output()
	var outs []Device
	for _, d := range Scan() {
		if d.Playback {
			outs = append(outs, d)
		}
	}
	if len(outs) == 0 {
		return Device{}, false
	}
	next := outs[0]
	if ok {
		for i, d := range outs {
			if key(d) == key(cur) {
				next = outs[(i+1)%len(outs)]
				break
			}
		}
	}
	s.mu.Lock()
	s.override = key(next)
	s.mu.Unlock()
	return next, true
}

func key(d Device) string { return fmt.Sprintf("%s,%d", d.CardID, d.Dev) }
