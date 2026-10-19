package ui

import (
	"image"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/diggyen/SimpleClient/internal/i18n"
	"github.com/diggyen/SimpleClient/internal/inputdev"
	"github.com/diggyen/SimpleClient/internal/wifi"
)

// fakeWifi stands in for wpa_supplicant and the files the dialog writes: the
// networks in range and the interface's status, and every network put in
// effect, kept and applied.
type fakeWifi struct {
	mu      sync.Mutex
	nets    []wifi.Network
	status  wifi.Status
	kept    wifi.Config
	used    []wifi.Config
	saved   []wifi.Config
	applied int
}

func withFakeWifi(t *testing.T) *fakeWifi {
	t.Helper()
	f := &fakeWifi{}
	ifs, isw, scan, res, cur, load, use, save, apply, addr :=
		wifiInterfaces, wifiIsWireless, wifiScan, wifiResults, wifiCurrent, wifiLoad, wifiUse, wifiSave, wifiApply, wifiAddress
	t.Cleanup(func() {
		wifiInterfaces, wifiIsWireless, wifiScan, wifiResults, wifiCurrent, wifiLoad, wifiUse, wifiSave, wifiApply, wifiAddress =
			ifs, isw, scan, res, cur, load, use, save, apply, addr
	})
	wifiInterfaces = func() []string { return []string{"wlan0"} }
	wifiIsWireless = func(i string) bool { return i == "wlan0" }
	wifiScan = func(string) error { return nil }
	wifiResults = func(string) ([]wifi.Network, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return append([]wifi.Network(nil), f.nets...), nil
	}
	wifiCurrent = func(string) (wifi.Status, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.status, nil
	}
	wifiLoad = func() (wifi.Config, bool) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.kept, f.kept.SSID != ""
	}
	wifiUse = func(c wifi.Config) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.used = append(f.used, c)
		return nil
	}
	wifiSave = func(c wifi.Config, _ bool) (bool, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.saved = append(f.saved, c)
		return true, nil
	}
	wifiApply = func() error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.applied++
		return nil
	}
	wifiAddress = func(string) string {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.status.Joined() {
			return "192.168.20.31/24"
		}
		return ""
	}
	return f
}

func (f *fakeWifi) set(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

// waitFor polls cond, under the state's lock, until it holds; the dialog does
// its work on goroutines.
func waitFor(t *testing.T, state *UIState, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(5 * time.Millisecond) {
		state.Mu.Lock()
		ok := cond()
		state.Mu.Unlock()
		if ok {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// poll has the dialog read wpa_supplicant once more, as its ticker does.
func poll(t *testing.T, state *UIState) {
	t.Helper()
	waitFor(t, state, "the poll before", func() bool { return !state.Wifi.polling })
	state.Mu.Lock()
	refreshWifi(state)
	state.Mu.Unlock()
	waitFor(t, state, "the poll", func() bool { return !state.Wifi.polling })
}

func keys(state *UIState, codes ...int) {
	state.Mu.Lock()
	defer state.Mu.Unlock()
	for _, c := range codes {
		handleWifiKey(inputdev.InputEvent{Type: inputdev.EvKey, Pressed: true, KeyCode: c}, state)
	}
}

func typeText(state *UIState, s string) {
	state.Mu.Lock()
	defer state.Mu.Unlock()
	for _, r := range s {
		handleWifiKey(inputdev.InputEvent{Type: inputdev.EvKey, Pressed: true, Rune: r}, state)
	}
}

// openTestWifi opens the dialog from the network settings, as Enter on their
// wireless row does, and waits for the first poll.
func openTestWifi(t *testing.T, f *fakeWifi) *UIState {
	t.Helper()
	state := &UIState{Screen: ScreenNetwork, Subnet: "10.7.7.0/24", ScanDone: true}
	state.Mu.Lock()
	state.Net.Wireless = initWifi(state)
	openWifi(state)
	state.Mu.Unlock()
	if state.Screen != ScreenWifi {
		t.Fatalf("screen %v, want the wireless dialog", state.Screen)
	}
	waitFor(t, state, "the networks", func() bool { return len(state.Wifi.Networks) == len(f.nets) })
	return state
}

func TestWifi_JoinKeepsOnceOn(t *testing.T) {
	i18n.Set(i18n.LangEN)
	f := withFakeWifi(t)
	f.nets = []wifi.Network{{SSID: "Office", Signal: -50, Security: wifi.PSK}, {SSID: "Guest", Signal: -70, Security: wifi.Open}}
	state := openTestWifi(t, f)

	keys(state, inputdev.KeyEnter)
	if w := &state.Wifi; !w.Prompt || w.Net.SSID != "Office" || w.Focus != itemPassword {
		t.Fatalf("Enter on a WPA2 network: prompt %v for %q, focus %d", w.Prompt, w.Net.SSID, w.Focus)
	}
	typeText(state, "short")
	keys(state, inputdev.KeyEnter)
	if w := &state.Wifi; !w.Prompt || !w.Err || w.Msg != i18n.T(i18n.WifiErrPassword) {
		t.Fatalf("a 5-character passphrase: prompt %v, %q", w.Prompt, w.Msg)
	}
	keys(state, inputdev.KeyBackspace, inputdev.KeyBackspace, inputdev.KeyBackspace, inputdev.KeyBackspace, inputdev.KeyBackspace)
	typeText(state, "parola 123")
	keys(state, inputdev.KeyEnter)
	waitFor(t, state, "joining", func() bool { return state.Wifi.Joining.SSID == "Office" })

	f.mu.Lock()
	used, saved := f.used, len(f.saved)
	f.mu.Unlock()
	if len(used) != 1 || used[0].SSID != "Office" || used[0].Security != wifi.PSK ||
		len(used[0].Key) != 64 || strings.Contains(used[0].Key, "parola") {
		t.Fatalf("put in effect: %+v", used)
	}
	if saved != 0 || state.Wifi.Kept.SSID != "" {
		t.Fatalf("kept before the kiosk is on it: %d saves, kept %q", saved, state.Wifi.Kept.SSID)
	}

	// wpa_supplicant gets there.
	f.set(func() { f.status = wifi.Status{State: "COMPLETED", SSID: "Office"} })
	poll(t, state)
	waitFor(t, state, "keeping it", func() bool { return state.Wifi.Msg == i18n.T(i18n.NetSaved) })
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.saved) != 1 || f.saved[0] != used[0] || state.Wifi.Kept != used[0] || state.Wifi.Joining.SSID != "" {
		t.Fatalf("saved %+v, kept %+v, joining %q", f.saved, state.Wifi.Kept, state.Wifi.Joining.SSID)
	}
	if line, _ := state.Wifi.statusLine(); line != "Connected to Office  ·  192.168.20.31/24" {
		t.Errorf("status line %q", line)
	}
	if state.Subnet != "" || state.ScanDone {
		t.Error("the subnet of the new network is not scanned")
	}
}

func TestWifi_RefusedPasswordGoesBack(t *testing.T) {
	i18n.Set(i18n.LangEN)
	f := withFakeWifi(t)
	home := wifi.Config{SSID: "Home", Security: wifi.PSK, Key: strings.Repeat("ab", 32)}
	f.kept = home
	f.status = wifi.Status{State: "COMPLETED", SSID: "Home"}
	f.nets = []wifi.Network{{SSID: "Home", Signal: -45, Security: wifi.PSK}, {SSID: "Corp", Signal: -60, Security: wifi.EAP}}
	state := openTestWifi(t, f)
	if state.Wifi.Kept != home {
		t.Fatalf("kept %+v", state.Wifi.Kept)
	}

	keys(state, inputdev.KeyDown, inputdev.KeyEnter)
	if w := &state.Wifi; !w.Prompt || w.Focus != itemUser {
		t.Fatalf("an enterprise network: prompt %v, focus %d", w.Prompt, w.Focus)
	}
	keys(state, inputdev.KeyTab) // the empty user name
	typeText(state, "Parola1")
	keys(state, inputdev.KeyEnter)
	if w := &state.Wifi; w.Msg != i18n.T(i18n.WifiErrUser) || w.Focus != itemUser {
		t.Fatalf("no user name: %q, focus %d", w.Msg, w.Focus)
	}
	typeText(state, `CORP\ayse`)
	keys(state, inputdev.KeyEnter)
	waitFor(t, state, "joining", func() bool { return state.Wifi.Joining.SSID == "Corp" })

	// The handshake fails: the prompt is back for the password, and the
	// kiosk goes back to the network it had.
	f.set(func() { f.status = wifi.Status{State: "DISCONNECTED", Rejected: true} })
	poll(t, state)
	w := &state.Wifi
	if !w.Prompt || w.Net.SSID != "Corp" || w.Fields[itemUser] != `CORP\ayse` || w.Fields[itemPassword] != "" ||
		w.Focus != itemPassword || w.Msg != i18n.T(i18n.WifiRejectedUser) {
		t.Fatalf("after the refusal: prompt %v %q user %q pass %q focus %d %q",
			w.Prompt, w.Net.SSID, w.Fields[itemUser], w.Fields[itemPassword], w.Focus, w.Msg)
	}
	waitFor(t, state, "going back", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.used) == 2 && f.applied == 2
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.used[0].SSID != "Corp" || f.used[0].Identity != `CORP\ayse` || f.used[1] != home || len(f.saved) != 0 {
		t.Fatalf("used %+v, saved %+v", f.used, f.saved)
	}
	if state.Wifi.Kept != home {
		t.Fatalf("kept %+v after a refusal", state.Wifi.Kept)
	}
}

func TestWifi_GivesUpInTime(t *testing.T) {
	i18n.Set(i18n.LangEN)
	f := withFakeWifi(t)
	f.nets = []wifi.Network{{SSID: "Guest", Signal: -70, Security: wifi.Open}}
	state := openTestWifi(t, f)

	keys(state, inputdev.KeyEnter) // an open network: no prompt
	waitFor(t, state, "joining", func() bool { return state.Wifi.Joining.SSID == "Guest" })
	if state.Wifi.Prompt {
		t.Fatal("an open network asked for a password")
	}
	state.Mu.Lock()
	state.Wifi.JoinBy = time.Now().Add(-time.Second)
	state.Mu.Unlock()
	poll(t, state)
	if w := &state.Wifi; w.Joining.SSID != "" || !w.Err || w.Msg != "Could not connect to Guest" || w.Prompt {
		t.Fatalf("out of time: joining %q, %q, prompt %v", w.Joining.SSID, w.Msg, w.Prompt)
	}
	waitFor(t, state, "going back", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.used) == 2 && f.used[1] == (wifi.Config{})
	})
}

func TestWifi_HiddenNetwork(t *testing.T) {
	i18n.Set(i18n.LangEN)
	f := withFakeWifi(t)
	f.nets = []wifi.Network{{SSID: "Office", Signal: -50, Security: wifi.PSK}}
	state := openTestWifi(t, f)

	keys(state, inputdev.KeyDown, inputdev.KeyDown, inputdev.KeyEnter) // past the end: the hidden network
	w := &state.Wifi
	if !w.Prompt || !w.Hidden || w.Focus != itemName ||
		len(w.items()) != 5 || w.items()[1] != itemSecurity || w.items()[2] != itemPassword {
		t.Fatalf("hidden: prompt %v hidden %v focus %d items %v", w.Prompt, w.Hidden, w.Focus, w.items())
	}
	keys(state, inputdev.KeyEnter)
	if w.Msg != i18n.T(i18n.WifiErrName) || w.Focus != itemName {
		t.Fatalf("no name: %q focus %d", w.Msg, w.Focus)
	}
	typeText(state, "Depo AP")
	keys(state, inputdev.KeyTab, inputdev.KeyRight) // WPA2 to enterprise
	if w.Net.Security != wifi.EAP || w.items()[2] != itemUser {
		t.Fatalf("security %v, items %v", w.Net.Security, w.items())
	}
	keys(state, inputdev.KeyLeft, inputdev.KeyLeft) // and round to open
	if w.Net.Security != wifi.Open || len(w.items()) != 4 {
		t.Fatalf("security %v, items %v", w.Net.Security, w.items())
	}
	keys(state, inputdev.KeySpace, inputdev.KeyTab) // WPA2 again, then the password
	typeText(state, "depo şifresi")
	keys(state, inputdev.KeyEnter)
	waitFor(t, state, "joining", func() bool { return state.Wifi.Joining.SSID == "Depo AP" })
	if j := state.Wifi.Joining; j.Security != wifi.PSK || len(j.Key) != 64 {
		t.Fatalf("joining %+v", j)
	}
}

func TestWifi_KeptAndForget(t *testing.T) {
	i18n.Set(i18n.LangEN)
	f := withFakeWifi(t)
	home := wifi.Config{SSID: "Home", Security: wifi.PSK, Key: strings.Repeat("cd", 32)}
	f.kept = home
	f.nets = []wifi.Network{{SSID: "Home", Signal: -45, Security: wifi.PSK}}
	state := openTestWifi(t, f)

	// Not on it, as when it was out of range: Enter tries it again as kept.
	keys(state, inputdev.KeyEnter)
	waitFor(t, state, "joining", func() bool { return state.Wifi.Joining == home })
	if state.Wifi.Prompt {
		t.Fatal("the kept network asked for its password again")
	}

	keys(state, inputdev.KeyDelete)
	waitFor(t, state, "forgetting", func() bool { return !state.Wifi.Busy })
	f.mu.Lock()
	defer f.mu.Unlock()
	if w := &state.Wifi; w.Kept.SSID != "" || w.Joining.SSID != "" || w.Msg != "Home forgotten" ||
		len(f.saved) != 1 || f.saved[0] != (wifi.Config{}) {
		t.Fatalf("kept %q joining %q %q, saved %+v", w.Kept.SSID, w.Joining.SSID, w.Msg, f.saved)
	}
}

func TestWifi_SelectionFollowsTheNetwork(t *testing.T) {
	w := &WifiState{}
	w.setNetworks([]wifi.Network{{SSID: "A", Signal: -40}, {SSID: "B", Signal: -50}, {SSID: "C", Signal: -60}})
	w.move(2)
	w.setNetworks([]wifi.Network{{SSID: "C", Signal: -30}, {SSID: "A", Signal: -40}})
	if w.Sel != 0 {
		t.Errorf("selection on %d, want C's new place", w.Sel)
	}
	w.move(5) // the hidden network's row stays chosen as the list changes
	w.setNetworks([]wifi.Network{{SSID: "A"}, {SSID: "B"}, {SSID: "C"}})
	if w.Sel != 3 {
		t.Errorf("selection on %d, want the hidden network's row", w.Sel)
	}
	nets := make([]wifi.Network, 12)
	for i := range nets {
		nets[i].SSID = string(rune('a' + i))
	}
	w.setNetworks(nets)
	if w.Sel != 12 || w.Top != 12+1-wifiRows {
		t.Errorf("selection %d top %d", w.Sel, w.Top)
	}
}

func TestNetDialog_WirelessRow(t *testing.T) {
	i18n.Set(i18n.LangEN)
	f := withFakeWifi(t)
	f.status = wifi.Status{State: "COMPLETED", SSID: "Office"}
	state := &UIState{Screen: ScreenDiscovery}
	state.Mu.Lock()
	openNetwork(state)
	n := &state.Net
	if n.Wireless != "wlan0" {
		t.Fatalf("wireless %q", n.Wireless)
	}
	handleNetKey(inputdev.InputEvent{Type: inputdev.EvKey, Pressed: true, KeyCode: inputdev.KeyTab}, state)
	if n.Focus != netFocusWifi {
		t.Fatalf("Tab from the switch went to %d, want the wireless row", n.Focus)
	}
	state.Mu.Unlock()
	waitFor(t, state, "the status", func() bool { return state.Wifi.Status.Joined() })
	state.Mu.Lock()
	if got := state.Wifi.summary(); got != "Office  ·  connected" {
		t.Errorf("row %q", got)
	}
	handleNetKey(inputdev.InputEvent{Type: inputdev.EvKey, Pressed: true, KeyCode: inputdev.KeyEnter}, state)
	if state.Screen != ScreenWifi {
		t.Fatalf("Enter on the row: screen %v", state.Screen)
	}
	handleWifiKey(inputdev.InputEvent{Type: inputdev.EvKey, Pressed: true, KeyCode: inputdev.KeyEsc}, state)
	if state.Screen != ScreenNetwork || n.Focus != netFocusWifi {
		t.Fatalf("Esc: screen %v focus %d, want the settings as they were", state.Screen, n.Focus)
	}
	state.Mu.Unlock()
}

func TestWifi_Click(t *testing.T) {
	i18n.Set(i18n.LangEN)
	f := withFakeWifi(t)
	f.nets = []wifi.Network{{SSID: "Office", Signal: -50, Security: wifi.PSK}, {SSID: "Lab", Signal: -60, Security: wifi.SAE}}
	state := openTestWifi(t, f)
	screen := image.Rect(0, 0, 1280, 800)

	state.Mu.Lock()
	defer state.Mu.Unlock()
	w := &state.Wifi
	r := layoutWifi(screen, w).row(1)
	handleWifiClick(state, screen, r.Min.Add(image.Pt(40, 5)))
	if !w.Prompt || w.Net.SSID != "Lab" || w.Net.Security != wifi.SAE {
		t.Fatalf("click on Lab: prompt %v for %q", w.Prompt, w.Net.SSID)
	}
	l := layoutWifi(screen, w)
	handleWifiClick(state, screen, l.Cancel.Min.Add(image.Pt(5, 5)))
	if w.Prompt {
		t.Fatal("Cancel left the prompt up")
	}
}

func TestRenderWifi(t *testing.T) {
	i18n.Set(i18n.LangTR)
	t.Cleanup(func() { i18n.Set(i18n.Default) })
	w := WifiState{
		Iface: "wlan0",
		Networks: []wifi.Network{
			{SSID: "Ofis", Signal: -48, Security: wifi.PSK},
			{SSID: strings.Repeat("Çok uzun bir ağ adı ", 3), Signal: -66, Security: wifi.EAP},
		},
		Kept:   wifi.Config{SSID: "Ofis", Security: wifi.PSK},
		Status: wifi.Status{State: "COMPLETED", SSID: "Ofis"},
	}
	for _, size := range []image.Point{{640, 480}, {1280, 800}} {
		for _, prompt := range []bool{false, true} {
			st := &UIState{Screen: ScreenWifi, Wifi: w}
			if prompt {
				st.Wifi.ask(wifi.Network{Security: wifi.PSK}, true)
			}
			img := image.NewRGBA(image.Rect(0, 0, size.X, size.Y))
			renderWifi(img, st)
			l := layoutWifi(img.Bounds(), &st.Wifi)
			if !l.Box.In(img.Bounds()) {
				t.Errorf("%v: the dialog %v does not fit", size, l.Box)
			}
		}
	}
}
