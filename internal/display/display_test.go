package display

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	for in, want := range map[string]Mode{
		"1600x900":  {1600, 900},
		"1024x768":  {1024, 768},
		"3840x2160": {3840, 2160},
	} {
		if got, ok := Parse(in); !ok || got != want {
			t.Errorf("Parse(%q) = %v %v, want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "auto", "1600", "1600x", "x900", "1920x1080i", "16x9", "1600x900@60", "-1x-1"} {
		if m, ok := Parse(in); ok {
			t.Errorf("Parse(%q) = %v, want none", in, m)
		}
	}
}

// fakeDRM lays out /sys/class/drm with one connector per entry.
func fakeDRM(t *testing.T, conns map[string][3]string) {
	t.Helper()
	dir := t.TempDir()
	for name, c := range conns {
		d := filepath.Join(dir, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		for file, body := range map[string]string{"status": c[0], "modes": c[1], "edid": c[2]} {
			if err := os.WriteFile(filepath.Join(d, file), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	old := drmPath
	drmPath = dir
	t.Cleanup(func() { drmPath = old })
}

// A display with no EDID gets the kernel's guesses; one with it, its modes.
// Disconnected connectors and interlaced modes are left out.
func TestOutputs(t *testing.T) {
	fakeDRM(t, map[string][3]string{
		"card0-VGA-1":    {"connected\n", "1024x768\n800x600\n800x600\n640x480\n", ""},
		"card0-HDMI-A-1": {"disconnected\n", "", ""},
		"card1-DP-1":     {"connected\n", "1600x900\n1920x1080i\n1440x900\n", "\x00\xff\xff"},
	})
	got := Outputs()
	want := []Output{
		{Name: "VGA-1", Modes: []Mode{{1024, 768}, {800, 600}, {640, 480}}},
		{Name: "DP-1", EDID: true, Modes: []Mode{{1600, 900}, {1440, 900}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Outputs() = %+v\nwant %+v", got, want)
	}
	if !Blind(got) || Blind(got[1:]) {
		t.Fatal("Blind is wrong")
	}
	if !Reported(got, Mode{1440, 900}) || Reported(got, Mode{1366, 768}) {
		t.Fatal("Reported is wrong")
	}
}

func TestChoicesLargestFirstEachOnce(t *testing.T) {
	got := Choices([]Output{{Modes: []Mode{{1024, 768}, {2560, 1440}, {800, 600}, {1600, 900}}}})
	if got[0] != (Mode{2560, 1440}) {
		t.Fatalf("first is %v, want the largest", got[0])
	}
	seen := map[Mode]bool{}
	for i, m := range got {
		if seen[m] {
			t.Fatalf("%v twice", m)
		}
		seen[m] = true
		if i > 0 && m.W*m.H > got[i-1].W*got[i-1].H {
			t.Fatalf("%v after %v", m, got[i-1])
		}
	}
	for _, m := range append(Common, Mode{800, 600}) {
		if !seen[m] {
			t.Fatalf("%v missing", m)
		}
	}
}
