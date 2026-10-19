package wifi

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// IEEE 802.11i, annex H.4: the passphrase-to-key test vectors.
func TestPBKDF2_WPAVectors(t *testing.T) {
	for _, v := range []struct{ pass, ssid, key string }{
		{"password", "IEEE", "f42c6fc52df0ebef9ebb4b90b38a5f902e83fe1b135a70e23aed762e9710a12e"},
		{"ThisIsAPassword", "ThisIsASSID", "0dc0d6eb90555ed6419756b9a15ec3e3209b63df707dd508d14581f8982721af"},
	} {
		if got := hex.EncodeToString(pbkdf2SHA1([]byte(v.pass), []byte(v.ssid), 4096, 32)); got != v.key {
			t.Errorf("%q/%q: %s, want %s", v.pass, v.ssid, got, v.key)
		}
	}
}

func TestNTHash(t *testing.T) {
	if got := ntHash("password"); got != "8846f7eaee8fb117ad06bdd830b7586c" {
		t.Fatalf("NT hash of \"password\" = %s", got)
	}
}

func TestParseResults(t *testing.T) {
	out := "bssid / frequency / signal level / flags / ssid\n" +
		"02:00:00:00:01:00\t2412\t-71\t[WPA2-PSK-CCMP][ESS]\tEv Ağı\n" +
		"02:00:00:00:02:00\t5180\t-48\t[WPA2-PSK-CCMP][ESS]\tEv Ağı\n" +
		"02:00:00:00:03:00\t2437\t-60\t[WPA2-PSK+SAE-CCMP][ESS]\tOfis\n" +
		"02:00:00:00:04:00\t2462\t-80\t[RSN-SAE-CCMP][ESS]\tYeni\n" +
		"02:00:00:00:05:00\t2412\t-66\t[WPA2-EAP-CCMP][ESS]\tKurum\n" +
		"02:00:00:00:06:00\t2412\t-90\t[ESS]\tMisafir \\\"A\\\" \\x01\n" +
		"02:00:00:00:07:00\t2412\t-40\t[WPA2-PSK-CCMP][ESS]\t\n" + // hidden
		"02:00:00:00:09:00\t2412\t-42\t[WPA2-PSK-CCMP][ESS]\t\\x00\\x00\\x00\\x00\n" + // hidden, zero-filled
		"02:00:00:00:08:00\t2412\t-85\t[WEP][ESS]\tEski\n"
	got := parseResults(out)
	want := []Network{
		{"Ev Ağı", -48, PSK}, {"Ofis", -60, PSK}, {"Kurum", -66, EAP},
		{"Yeni", -80, SAE}, {"Eski", -85, WEP}, {"Misafir \"A\" \x01", -90, Open},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %+v, want %+v", i, got[i], want[i])
		}
	}
	if got[0].Bars() != 4 || got[2].Bars() != 3 || got[3].Bars() != 1 {
		t.Errorf("bars %d %d %d", got[0].Bars(), got[2].Bars(), got[3].Bars())
	}
}

func TestConfigRoundTrip(t *testing.T) {
	for _, c := range []struct {
		n        Network
		id, pass string
	}{
		{Network{SSID: "Ev Ağı", Security: PSK}, "", "gizli parola 1"},
		{Network{SSID: `tırnak"li}`, Security: SAE}, "", "p\"ass\nword"},
		{Network{SSID: "Kurum", Security: EAP}, `OFIS\ayse`, "Parola1"},
		{Network{SSID: "Misafir", Security: Open}, "", ""},
	} {
		cfg, err := NewConfig(c.n, c.id, c.pass)
		if err != nil {
			t.Fatalf("%+v: %v", c.n, err)
		}
		enc := string(cfg.Encode())
		if c.pass != "" && strings.Contains(enc, c.pass) {
			t.Errorf("%s: the passphrase shows as typed:\n%s", c.n.SSID, enc)
		}
		back, ok := Parse(cfg.Encode())
		if !ok || back != cfg {
			t.Errorf("%+v came back as %+v (%v)\n%s", cfg, back, ok, enc)
		}
	}
	if b := string(Config{}.Encode()); strings.Contains(b, "network=") || !strings.Contains(b, "ctrl_interface=") {
		t.Errorf("no network: %q", b)
	}
}

func TestNewConfig_Rejects(t *testing.T) {
	if _, err := NewConfig(Network{SSID: "a", Security: PSK}, "", "kısa"); err != ErrPassphrase {
		t.Errorf("short passphrase: %v", err)
	}
	if _, err := NewConfig(Network{SSID: "a", Security: EAP}, "", "x"); err != ErrIdentity {
		t.Errorf("no user name: %v", err)
	}
	if _, err := NewConfig(Network{SSID: "a", Security: WEP}, "", "12345"); err != ErrUnsupported {
		t.Errorf("WEP: %v", err)
	}
	for _, name := range []string{"", strings.Repeat("ğ", 17)} { // 34 bytes
		if _, err := NewConfig(Network{SSID: name, Security: Open}, "", ""); err != ErrName {
			t.Errorf("name %q: %v", name, err)
		}
	}
}

func TestInterfaces(t *testing.T) {
	dir := t.TempDir()
	sysNet = dir
	t.Cleanup(func() { sysNet = "/sys/class/net" })
	for _, p := range []string{"eth0/device", "wlan0/wireless", "wlp2s0/wireless", "lo"} {
		if err := os.MkdirAll(filepath.Join(dir, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(Interfaces(), " "); got != "wlan0 wlp2s0" {
		t.Fatalf("Interfaces() = %q", got)
	}
}

func TestSave_Installed(t *testing.T) {
	dir := t.TempDir()
	RunPath = filepath.Join(dir, "run.conf")
	dataPath = filepath.Join(dir, "data", "wifi.conf")
	t.Cleanup(func() { RunPath, dataPath = "/run/sc-wifi.conf", "/data/simpleclient/wifi.conf" })

	c, _ := NewConfig(Network{SSID: "Ev", Security: PSK}, "", "parola123")
	if kept, err := Save(c, true); err != nil || !kept {
		t.Fatalf("Save: %v %v", kept, err)
	}
	if got, ok := Load(); !ok || got != c {
		t.Fatalf("Load gave %+v %v", got, ok)
	}
	if b, err := os.ReadFile(dataPath); err != nil || string(b) != string(c.Encode()) {
		t.Fatalf("data partition holds %q, %v", b, err)
	}
	// Forgetting it leaves wpa_supplicant a file to run with, and no network.
	if _, err := Save(Config{}, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := Load(); ok {
		t.Fatal("still a network after forgetting it")
	}
}

func TestUnescape(t *testing.T) {
	for in, want := range map[string]string{
		`plain`: "plain", `a\\b`: `a\b`, `\"q\"`: `"q"`, `\x41\x7a`: "Az", `t\tn\n`: "t\tn\n", `\xZZ`: "xZZ", `end\`: `end\`,
	} {
		if got := unescape(in); got != want {
			t.Errorf("unescape(%q) = %q, want %q", in, got, want)
		}
	}
}
