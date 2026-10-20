package audio

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeProc lays out /proc/asound for a board with an analog codec and a
// graphics chip's two HDMI ports, a monitor with speakers on the first.
func fakeProc(t *testing.T) {
	dir := t.TempDir()
	old := procRoot
	procRoot = dir
	t.Cleanup(func() { procRoot = old })
	files := map[string]string{
		"cards": " 0 [PCH            ]: HDA-Intel - HDA Intel PCH\n                      HDA Intel PCH at 0xf7f10000 irq 32\n",
		"pcm": "00-00: ALC662 rev3 Analog : ALC662 rev3 Analog : playback 1 : capture 1\n" +
			"00-03: HDMI 0 : HDMI 0 : playback 1\n" +
			"00-07: HDMI 1 : HDMI 1 : playback 1\n",
		"card0/eld#0.0": "monitor_present\t\t1\neld_valid\t\t1\nsad_count\t\t1\n",
		"card0/eld#0.1": "monitor_present\t\t0\neld_valid\t\t0\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// With nothing in the headphone jack sound goes to the monitor, and the badge
// names the HDMI port it is on.
func TestSelectorOutput(t *testing.T) {
	fakeProc(t)
	old := headphonesIn
	headphonesIn = func() bool { return false }
	t.Cleanup(func() { headphonesIn = old })

	var s Selector
	if d, ok := s.Output(); !ok || d.Label() != "HDMI 0" {
		t.Fatalf("automatic choice %q %v, want the monitor's speakers", d.Label(), ok)
	}
	headphonesIn = func() bool { return true }
	if d, ok := s.Output(); !ok || d.Label() != "Analog" {
		t.Errorf("with headphones in, choice %q %v, want the analog jack", d.Label(), ok)
	}

	procRoot = t.TempDir()
	if d, ok := s.Output(); ok {
		t.Errorf("with no sound hardware Output gave %q", d.Label())
	}
}
