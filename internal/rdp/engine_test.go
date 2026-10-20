package rdp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeEngine stands in for sc-rdp: it speaks the same protocol over fd 3 and
// fd 4, logs every command it is sent to $SC_FAKE_LOG, and behaves as
// $SC_FAKE_MODE says.
const fakeEngine = `#!/bin/sh
while read -r line <&3; do
	echo "$line" >> "$SC_FAKE_LOG"
	[ "$line" = start ] && break
done
case "$SC_FAKE_MODE" in
auth)  echo "ERROR auth FREERDP_ERROR_CONNECT_LOGON_FAILURE Logon failed" >&4; exit 1 ;;
crash) exit 3 ;;
esac
echo "MODE 1920 1080 144 card0:HDMI-A-1" >&4
echo "CONNECTED 1920 1080" >&4
echo "POINTER 10 20" >&4
case "$SC_FAKE_MODE" in
lost)     echo "END lost" >&4; exit 0 ;;
rejected) echo "END logoff" >&4; exit 0 ;;
logoff)   echo "SESSION" >&4; echo "END logoff" >&4; exit 0 ;;
esac
while read -r line <&3; do
	echo "$line" >> "$SC_FAKE_LOG"
	if [ "$line" = q ]; then echo "END quit" >&4; exit 0; fi
done
echo "END lost" >&4
`

func useFakeEngine(t *testing.T, mode string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sc-rdp")
	if err := os.WriteFile(path, []byte(fakeEngine), 0o755); err != nil {
		t.Fatal(err)
	}
	old := EnginePath
	EnginePath = path
	t.Cleanup(func() { EnginePath = old })
	log := filepath.Join(dir, "log")
	t.Setenv("SC_FAKE_LOG", log)
	t.Setenv("SC_FAKE_MODE", mode)
	return log
}

func waitDone(t *testing.T, s Session) {
	t.Helper()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the engine did not exit")
	}
}

// A session: the connection goes down the pipe (the password with it, never
// on the command line), the display mode comes back, input is forwarded in
// order, and Close ends it cleanly.
func TestEngine_Session(t *testing.T) {
	log := useFakeEngine(t, "")
	pointer := make(chan [2]int, 1)
	e, err := StartEngine("10.0.0.5:3389", Credentials{Username: "admin", Password: "s3cret", Domain: "CORP"},
		[]string{"/drive:hotplug,*"}, func(x, y int) { pointer <- [2]int{x, y} })
	if err != nil {
		t.Fatal(err)
	}
	if e.Width != 1920 || e.Height != 1080 || e.Refresh != 144 || e.Display != "card0:HDMI-A-1" {
		t.Fatalf("mode %dx%d@%d on %q", e.Width, e.Height, e.Refresh, e.Display)
	}
	select {
	case p := <-pointer:
		if p != [2]int{10, 20} {
			t.Fatalf("pointer moved to %v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the server's pointer move never arrived")
	}

	_ = e.SendMouse(5, 6, 0)
	_ = e.SendKey(30, true) // A
	_ = e.SendKey(103, true)
	e.SendMouseDown(1, 5, 6)
	e.SendMouseUp(1, 5, 6)
	e.SendWheel(-2, 5, 6)
	_ = e.Close()
	waitDone(t, e)
	if e.Failed() {
		t.Error("a session closed from here counted as failed")
	}

	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"arg /v:10.0.0.5:3389", "arg /u:admin", "arg /d:CORP", "arg /kbd:layout:0x00000409",
		"arg /drive:hotplug,*", "arg /log-level:WARN", "password s3cret", "start",
		"m 5 6", "k 30 0 1", "k 72 1 1", "b 1 1 5 6", "b 1 0 5 6", "w -2 5 6", "q",
	}
	if lines := strings.Split(strings.TrimSpace(string(got)), "\n"); strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("the engine was sent\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

// A rejected account is reported as such, not as an engine that cannot run.
func TestEngine_AuthError(t *testing.T) {
	useFakeEngine(t, "auth")
	_, err := StartEngine("10.0.0.5:3389", Credentials{Username: "admin", Password: "wrong"}, nil, nil)
	var ee *EngineError
	if !errors.As(err, &ee) || ee.Class != "auth" || errors.Is(err, ErrNoEngine) {
		t.Fatalf("got %v, want an auth EngineError", err)
	}
}

// An engine that dies without a word — a missing library, a crash — or that
// is not there at all leaves the session to the built-in client.
func TestEngine_FallsBack(t *testing.T) {
	useFakeEngine(t, "crash")
	if _, err := StartEngine("10.0.0.5:3389", Credentials{}, nil, nil); !errors.Is(err, ErrNoEngine) {
		t.Fatalf("crash: got %v, want ErrNoEngine", err)
	}
	EnginePath = filepath.Join(t.TempDir(), "missing")
	if _, err := StartEngine("10.0.0.5:3389", Credentials{}, nil, nil); !errors.Is(err, ErrNoEngine) {
		t.Fatalf("missing: got %v, want ErrNoEngine", err)
	}
}

// A session whose connection died is worth reconnecting; Failed says so.
func TestEngine_Lost(t *testing.T) {
	useFakeEngine(t, "lost")
	e, err := StartEngine("10.0.0.5:3389", Credentials{Username: "u", Password: "p"}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, e)
	if !e.Failed() {
		t.Fatal("a lost connection did not count as failed")
	}
}

// A server that hangs up before it has logged the user on turned the password
// down, as xrdp does with require_credentials; one that logs the user off had
// logged them on first, and a lost connection is no answer at all.
func TestEngine_Rejected(t *testing.T) {
	for _, c := range []struct {
		mode string
		want bool
	}{{"rejected", true}, {"logoff", false}, {"lost", false}} {
		useFakeEngine(t, c.mode)
		e, err := StartEngine("10.0.0.5:3389", Credentials{Username: "u", Password: "p"}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		waitDone(t, e)
		if e.Rejected() != c.want {
			t.Errorf("%s: Rejected() = %v, want %v", c.mode, e.Rejected(), c.want)
		}
	}
}
