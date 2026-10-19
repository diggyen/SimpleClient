package audio

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Output plays a stream of PCM blocks on one endpoint. A change of format or
// endpoint restarts the player; between streams it is closed so the device is
// free and draws no power.
type Output struct {
	mu     sync.Mutex
	dev    string
	format Format
	cmd    *exec.Cmd
	in     io.WriteCloser
}

// bufferTime is the ALSA buffer behind the player. Long enough to ride out a
// network hiccup or a GC pause without a click, short enough that sound stays
// in step with the picture.
const bufferTime = 150 * time.Millisecond

// Play writes one block, starting the player first if needed. It blocks while
// the device buffer is full, which is what paces the stream to real time.
func (o *Output) Play(dev Device, f Format, pcm []byte) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cmd == nil || o.dev != dev.ALSA() || !sameFormat(o.format, f) {
		o.stopLocked()
		if err := o.startLocked(dev.ALSA(), f); err != nil {
			return err
		}
	}
	if _, err := o.in.Write(pcm); err != nil {
		o.stopLocked()
		return err
	}
	return nil
}

// Stop closes the player.
func (o *Output) Stop() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.stopLocked()
}

func (o *Output) startLocked(dev string, f Format) error {
	cmd := exec.Command("aplay", "-q", "-D", dev, "-t", "raw",
		"-f", f.alsaFormat(), "-c", fmt.Sprint(f.Channels), "-r", fmt.Sprint(f.SamplesPerSec),
		"--buffer-time="+fmt.Sprint(bufferTime.Microseconds()))
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("aplay: %w", err)
	}
	o.cmd, o.in, o.dev, o.format = cmd, in, dev, f
	return nil
}

func (o *Output) stopLocked() {
	if o.cmd == nil {
		return
	}
	_ = o.in.Close()
	done := make(chan struct{})
	cmd := o.cmd
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		_ = cmd.Process.Kill()
	}
	o.cmd, o.in = nil, nil
}

func sameFormat(a, b Format) bool {
	return a.Tag == b.Tag && a.Channels == b.Channels &&
		a.SamplesPerSec == b.SamplesPerSec && a.BitsPerSample == b.BitsPerSample
}

// Input records PCM from one endpoint and delivers it in fixed-size packets.
type Input struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	done chan struct{}
}

// Start begins recording; deliver is called from the recording goroutine
// with each packet of exactly packetBytes bytes.
func (in *Input) Start(dev Device, f Format, packetBytes int, deliver func([]byte)) error {
	in.Stop()
	cmd := exec.Command("arecord", "-q", "-D", dev.ALSA(), "-t", "raw",
		"-f", f.alsaFormat(), "-c", fmt.Sprint(f.Channels), "-r", fmt.Sprint(f.SamplesPerSec),
		"--buffer-time=100000")
	cmd.Stderr = os.Stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("arecord: %w", err)
	}
	done := make(chan struct{})
	in.mu.Lock()
	in.cmd, in.done = cmd, done
	in.mu.Unlock()
	go func() {
		defer close(done)
		buf := make([]byte, packetBytes)
		for {
			if _, err := io.ReadFull(out, buf); err != nil {
				return
			}
			deliver(append([]byte(nil), buf...))
		}
	}()
	return nil
}

// Stop ends recording.
func (in *Input) Stop() {
	in.mu.Lock()
	cmd, done := in.cmd, in.done
	in.cmd, in.done = nil, nil
	in.mu.Unlock()
	if cmd == nil {
		return
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	<-done
}

// InitMixer brings every card's mixer to a usable state. ALSA cards come up
// muted or at zero volume, and nothing else on this system would ever turn
// them up: the remote side controls loudness by scaling the samples it sends,
// so the local mixer just has to be open.
func InitMixer() {
	for card := range cardIDs() {
		ensureMixer(card)
	}
}

var (
	mixerMu   sync.Mutex
	mixerDone = map[string]bool{}
)

// ensureMixer initialises one card's mixer, once per card id — a USB headset
// plugged in after boot gets the same treatment the first time it is used.
func ensureMixer(card int) {
	id := cardIDs()[card]
	mixerMu.Lock()
	if mixerDone[id] {
		mixerMu.Unlock()
		return
	}
	mixerDone[id] = true
	mixerMu.Unlock()

	c := fmt.Sprint(card)
	_ = exec.Command("alsactl", "init", c).Run()
	out, err := exec.Command("amixer", "-c", c, "scontents").Output()
	if err != nil {
		return
	}
	for _, ctl := range parseControls(string(out)) {
		for _, args := range mixerSettings(ctl) {
			_ = exec.Command("amixer", append([]string{"-q", "-c", c, "sset", ctl.id}, args...)...).Run()
		}
	}
}

type mixerControl struct {
	id, name string
	caps     string
}

// parseControls reads `amixer scontents`:
//
//	Simple mixer control 'Mic',0
//	  Capabilities: pvolume pswitch cvolume cswitch
func parseControls(out string) []mixerControl {
	var ctls []mixerControl
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Simple mixer control '") {
			rest := strings.TrimPrefix(line, "Simple mixer control '")
			name, idx, _ := strings.Cut(rest, "',")
			ctls = append(ctls, mixerControl{id: name + "," + idx, name: strings.ToLower(name)})
			continue
		}
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "Capabilities:") && len(ctls) > 0 {
			ctls[len(ctls)-1].caps = " " + strings.TrimPrefix(t, "Capabilities:") + " "
		}
	}
	return ctls
}

// mixerSettings is what to set on one control. Playback paths go to 90% and
// unmuted, except the analog loopbacks of microphone and beep into the
// speakers — on HD Audio the "Mic" control's playback side is exactly that,
// and left up it howls. Capture paths go to 80% and are selected.
func mixerSettings(c mixerControl) [][]string {
	var s [][]string
	has := func(cap string) bool { return strings.Contains(c.caps, " "+cap+" ") }
	loopback := strings.Contains(c.name, "mic") || strings.Contains(c.name, "beep") ||
		strings.Contains(c.name, "loopback") || strings.Contains(c.name, "line in")
	switch {
	case c.name == "auto-mute mode":
		// Speakers fall silent when headphones go in, as on any PC.
		return [][]string{{"Enabled"}}
	case strings.Contains(c.name, "boost"):
		// A step of microphone boost, not the maximum, which clips.
		return [][]string{{"1"}}
	}
	if has("pvolume") || has("volume") {
		if loopback {
			s = append(s, []string{"playback", "0%"})
		} else {
			s = append(s, []string{"playback", "90%"})
		}
	}
	if has("pswitch") || has("switch") {
		if loopback {
			s = append(s, []string{"playback", "mute"})
		} else {
			s = append(s, []string{"playback", "unmute"})
		}
	}
	if has("cvolume") {
		s = append(s, []string{"capture", "80%"})
	}
	if has("cswitch") {
		s = append(s, []string{"capture", "cap"})
	}
	return s
}
