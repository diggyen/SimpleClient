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

// F4 steps through every output and says which of how many it is on; the
// HDMI ports go by their own names, so each step reads differently.
func TestSelectorCycle(t *testing.T) {
	fakeProc(t)
	old := headphonesIn
	headphonesIn = func() bool { return false }
	t.Cleanup(func() { headphonesIn = old })

	var s Selector
	if d, ok := s.Output(); !ok || d.Label() != "HDMI 0" {
		t.Fatalf("automatic choice %q %v, want the monitor's speakers", d.Label(), ok)
	}
	for _, want := range []struct {
		label string
		i     int
	}{{"HDMI 1", 3}, {"Analog", 1}, {"HDMI 0", 2}} {
		d, i, n := s.Cycle()
		if d.Label() != want.label || i != want.i || n != 3 {
			t.Errorf("Cycle gave %q %d of %d, want %q %d of 3", d.Label(), i, n, want.label, want.i)
		}
		if got, _ := s.Output(); got.Label() != want.label {
			t.Errorf("Output after Cycle is %q, want %q", got.Label(), want.label)
		}
	}

	procRoot = t.TempDir()
	if _, i, n := s.Cycle(); i != 0 || n != 0 {
		t.Errorf("with no sound hardware Cycle gave %d of %d", i, n)
	}
}
