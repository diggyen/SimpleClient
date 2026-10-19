package autoconnect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	for _, c := range []Config{
		{Mode: Dialog, Server: "10.0.0.5", Username: "ayse", Domain: "OFIS"},
		{Mode: Logon, Server: "rdp.example.com:53389", Username: "kiosk", Password: `p@ss=wörd\`},
		{Mode: Logon, Server: "10.0.0.5", Username: "kiosk"},
	} {
		got, ok := Parse(c.Encode())
		if !ok || got != c {
			t.Errorf("%+v came back as %+v (%v)", c, got, ok)
		}
	}
}

func TestEncode_KeepsThePasswordOutOfSight(t *testing.T) {
	b := string(Config{Mode: Logon, Server: "10.0.0.5", Username: "kiosk", Password: "gizli123"}.Encode())
	if strings.Contains(b, "gizli123") {
		t.Fatalf("the password shows in the file: %q", b)
	}
	// A dialog keeps no password, whatever it was given.
	b = string(Config{Mode: Dialog, Server: "10.0.0.5", Password: "gizli123"}.Encode())
	if !strings.Contains(b, "password=\n") {
		t.Fatalf("a dialog setting kept a password: %q", b)
	}
	// Nor can a typed value add a line of its own.
	c, ok := Parse(Config{Mode: Dialog, Server: "10.0.0.5", Username: "a\nmode=logon"}.Encode())
	if !ok || c.Mode != Dialog || c.Username != "amode=logon" {
		t.Fatalf("got %+v", c)
	}
}

func TestParse_Incomplete(t *testing.T) {
	for _, s := range []string{
		"",
		"mode=dialog\n",                 // no server
		"mode=off\nserver=10.0.0.5\n",   // no mode
		"mode=logon\nserver=10.0.0.5\n", // logon without an account
		"mode=logon\nserver=10.0.0.5\nusername=k\npassword=***\n", // not base64
	} {
		if c, ok := Parse([]byte(s)); ok {
			t.Errorf("%q parsed as %+v", s, c)
		}
	}
}

func TestSave_Installed(t *testing.T) {
	dir := t.TempDir()
	RunPath = filepath.Join(dir, "run")
	dataPath = filepath.Join(dir, "data", "simpleclient", "autoconnect")
	t.Cleanup(func() { RunPath, dataPath = "/run/sc-autoconnect", "/data/simpleclient/autoconnect" })

	c := Config{Mode: Logon, Server: "10.0.0.5", Username: "kiosk", Password: "x"}
	if kept, err := Save(c, true); err != nil || !kept {
		t.Fatalf("Save: %v %v", kept, err)
	}
	if got, ok := Load(); !ok || got != c {
		t.Fatalf("Load gave %+v %v", got, ok)
	}
	b, err := os.ReadFile(dataPath)
	if err != nil || string(b) != string(c.Encode()) {
		t.Fatalf("data partition holds %q, %v", b, err)
	}
	if fi, err := os.Stat(dataPath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the file is readable by others: %v %v", fi.Mode(), err)
	}

	// Off takes it away, from both places.
	if kept, err := Save(Config{}, true); err != nil || !kept {
		t.Fatalf("Save off: %v %v", kept, err)
	}
	if _, ok := Load(); ok {
		t.Fatal("still in effect")
	}
	if _, err := os.Stat(dataPath); !os.IsNotExist(err) {
		t.Fatalf("still kept: %v", err)
	}
	// And removing what is not there is no error.
	if _, err := Save(Config{}, true); err != nil {
		t.Fatal(err)
	}
}

func TestModeNext(t *testing.T) {
	if Off.Next() != Dialog || Dialog.Next() != Logon || Logon.Next() != Off {
		t.Fatal("F10 does not cycle off, dialog, logon")
	}
}
