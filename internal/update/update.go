// Package update brings a disk install up to the newest release.
//
// It asks the release server which release is the newest and, when someone at
// the kiosk chooses to, downloads that release's update into the system slot
// the kiosk is not running from and has the next boot try that slot once. A
// boot of it that comes up makes it the slot to keep (bootenv.MarkGood); one
// that does not leaves GRUB on the slot it came from, which an update never
// touches.
//
// The release server is the project's releases on GitHub, or a mirror laid out
// the same way under another base URL:
//
//	<base>/latest                      redirects to <base>/tag/<tag>, or holds the tag
//	<base>/download/<tag>/SHA256SUMS   sha256sum's output for the release's files
//	<base>/download/<tag>/simpleclient-update.tar
//
// The tar holds a system slot's files at its top level: the kernels and the
// initrds, slot.cfg, which is how GRUB boots them, and version, the release's
// tag. It has to match its line in SHA256SUMS, which comes over HTTPS from the
// same release.
package update

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/diggyen/SimpleClient/internal/bootenv"
	"github.com/diggyen/SimpleClient/internal/version"
)

// DefaultBase is the release server: the project's releases on GitHub.
const DefaultBase = "https://github.com/diggyen/SimpleClient/releases"

// Asset is the file of a release that a disk install updates from.
const Asset = "simpleclient-update.tar"

// basePath points a kiosk at another release server, a mirror on the local
// network for instance: a file on the data partition holding its base URL.
var basePath = "/data/simpleclient/update-url"

// Base is the release server this kiosk updates from.
func Base() string {
	b, err := os.ReadFile(basePath)
	if s := strings.TrimSpace(string(b)); err == nil && s != "" {
		return strings.TrimRight(s, "/")
	}
	return DefaultBase
}

// Release is the newest release, as the server announced it.
type Release struct {
	Tag  string // its version, "v0.7.1"
	Sum  string // its update's SHA-256, in hex; empty when it has none
	Base string // the server it is on
}

var (
	// ErrNoUpdate is a release without an update for disk installs, one
	// from before there were updates. Installing it takes the USB stick.
	ErrNoUpdate = errors.New("update: the release has no update for disk installs")
	// ErrDamaged is a download that does not match its checksum.
	ErrDamaged = errors.New("update: the download is damaged")
	// ErrStalled is a download that stopped coming.
	ErrStalled = errors.New("update: the download stopped")
	// ErrUnproven is an update asked for before this boot has proven itself
	// (bootenv.MarkGood): the other slot is still the way back.
	ErrUnproven = errors.New("update: this boot has not proven itself yet")
)

// Latest asks the release server for its newest release. One without an
// update comes back with ErrNoUpdate, and its tag.
func Latest(ctx context.Context) (Release, error) {
	base := Base()
	tag, err := latestTag(ctx, base)
	if err != nil {
		return Release{}, err
	}
	sums, err := fetchSmall(ctx, base+"/download/"+url.PathEscape(tag)+"/SHA256SUMS")
	if err != nil {
		return Release{}, err
	}
	r := Release{Tag: tag, Sum: findSum(sums, Asset), Base: base}
	if r.Sum == "" {
		return r, ErrNoUpdate
	}
	return r, nil
}

// latestTag asks which release is the newest. GitHub answers with a redirect
// to the release's page, whose address ends in its tag; a mirror can do the
// same, or hold the tag in a file.
func latestTag(ctx context.Context, base string) (string, error) {
	resp, err := get(ctx, noRedirects, base+"/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var tag string
	switch {
	case resp.StatusCode/100 == 3:
		loc := resp.Header.Get("Location")
		if i := strings.LastIndex(loc, "/tag/"); i >= 0 {
			tag, _ = url.PathUnescape(loc[i+len("/tag/"):])
		}
	case resp.StatusCode == http.StatusOK:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		tag = strings.TrimSpace(string(b))
	default:
		return "", fmt.Errorf("update: latest release: %s", resp.Status)
	}
	if !validTag(tag) {
		return "", errors.New("update: the server has no release")
	}
	return tag, nil
}

// fetchSmall fetches a file of at most 64 KiB.
func fetchSmall(ctx context.Context, u string) (string, error) {
	resp, err := get(ctx, client, u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("update: %s: %s", u[strings.LastIndex(u, "/")+1:], resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return string(b), err
}

func get(ctx context.Context, c *http.Client, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "SimpleClient/"+version.Version)
	return c.Do(req)
}

// findSum picks name's checksum out of sha256sum's output.
func findSum(sums, name string) string {
	for _, line := range strings.Split(sums, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			if _, err := hex.DecodeString(f[0]); err == nil {
				return strings.ToLower(f[0])
			}
		}
	}
	return ""
}

// validTag accepts what a release tag can be, and nothing that would change
// the meaning of a URL it goes into.
func validTag(t string) bool {
	if t == "" || len(t) > 64 || t[0] == '.' {
		return false
	}
	for _, c := range t {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(".-_+", c)) {
			return false
		}
	}
	return true
}

// Newer reports whether release tag is a later version than current: v0.10.0
// is later than v0.9.3. Anything after the three numbers is ignored, so a build
// a few commits past a release ("v0.7.0-3-g1a2b3c4") counts as that release,
// and one that is not from a release at all ("dev") as older than every one.
func Newer(tag, current string) bool {
	t, ok := parse(tag)
	if !ok {
		return false
	}
	c, _ := parse(current)
	return slices.Compare(t[:], c[:]) > 0
}

// parse reads the numbers of "v1.2.3".
func parse(v string) ([3]int, bool) {
	var n [3]int
	v = strings.TrimPrefix(v, "v")
	for i := range n {
		j := 0
		for j < len(v) && v[j] >= '0' && v[j] <= '9' {
			j++
		}
		if j == 0 || j > 6 {
			return [3]int{}, false
		}
		n[i], _ = strconv.Atoi(v[:j])
		v = v[j:]
		if i < 2 {
			if !strings.HasPrefix(v, ".") {
				return [3]int{}, false
			}
			v = v[1:]
		}
	}
	return n, true
}

// The connections to the release server. There is no limit on a whole
// request — an update is hundreds of megabytes, over whatever line the kiosk
// has — but connecting, the TLS handshake and the server's answer each have
// one, and a download that stops coming is given up on (see watchdog).
var (
	transport = &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       60 * time.Second,
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, Time: tlsTime},
	}
	client      = &http.Client{Transport: transport}
	noRedirects = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
)

// tlsTime is the time certificates are checked against. The kiosk keeps time
// only in the machine's clock chip, with no time server to correct it, and
// one whose battery has gone flat starts up years in the past, where every
// certificate is not valid yet. A clock earlier than the build itself is
// wrong, and the build's own date is the better guess.
func tlsTime() time.Time {
	now := time.Now()
	if b := version.Built(); now.Before(b) {
		return b
	}
	return now
}

// Progress hears how the download goes: done bytes of total, total -1 while
// the server has not said.
type Progress func(done, total int64)

// Install writes release r into the slot boot is not running from and has the
// next boot try it. Restarting is left to the caller.
func Install(ctx context.Context, r Release, boot bootenv.Boot, progress Progress) error {
	if !boot.Installed() {
		return errors.New("update: not running from a disk install")
	}
	if r.Sum == "" {
		return ErrNoUpdate
	}
	env, err := bootenv.Read(boot)
	if err != nil {
		return err
	}
	if env["sc_boot"] != boot.Slot {
		return ErrUnproven
	}
	slot := boot.Other()
	dev, err := bootenv.SlotDevice(env, slot)
	if err != nil {
		return err
	}
	if err := bootenv.WithMounted(dev, "ext4", func(dir string) error {
		return unpack(ctx, r, dir, progress)
	}); err != nil {
		return err
	}
	if err := bootenv.TryOther(boot); err != nil {
		return err
	}
	remember(r.Tag, slot)
	return nil
}

// An update's files: plain names, at most maxFiles of them, none larger than
// maxFile. They are written as name+partial until all of them are in.
const (
	maxFiles = 32
	maxFile  = 512 << 20
	partial  = ".part"
)

// unpack downloads r's update and unpacks it into dir, the other slot's file
// system, in place of what the slot held. The files take their own names only
// once the whole download has matched its checksum, so the slot never holds
// part of a release: an update that fails leaves it empty.
func unpack(ctx context.Context, r Release, dir string, progress Progress) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	resp, err := get(ctx, client, r.Base+"/download/"+url.PathEscape(r.Tag)+"/"+Asset)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("update: %s: %s", Asset, resp.Status)
	}
	// The slot holds the version before this one until the update is
	// coming: the boot menu's previous version.
	if err := clearSlot(dir); err != nil {
		return err
	}

	dog := watch(resp.Body, cancel)
	defer dog.stop()
	sum := sha256.New()
	in := &counter{r: io.TeeReader(dog, sum), total: resp.ContentLength, progress: progress}
	names, err := extract(tar.NewReader(in), dir)
	if err == nil {
		// The blocks that end the archive are part of what the checksum
		// covers.
		_, err = io.Copy(io.Discard, in)
	}
	in.report()
	switch {
	case err != nil && dog.fired():
		err = ErrStalled
	case err == nil && hex.EncodeToString(sum.Sum(nil)) != r.Sum:
		err = ErrDamaged
	case err == nil:
		err = check(dir, names, r.Tag)
	}
	for _, n := range names {
		if err == nil {
			err = os.Rename(filepath.Join(dir, n+partial), filepath.Join(dir, n))
		}
	}
	if err != nil {
		clearSlot(dir)
	}
	return err
}

// extract writes the files of an update into dir under their partial names,
// and returns their names.
func extract(tr *tar.Reader, dir string) ([]string, error) {
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return names, nil
		}
		if err != nil {
			return names, err
		}
		name := strings.TrimPrefix(h.Name, "./")
		if h.Typeflag == tar.TypeDir && name == "" {
			continue
		}
		if h.Typeflag != tar.TypeReg || !validName(name) || h.Size > maxFile ||
			len(names) == maxFiles || slices.Contains(names, name) {
			return names, fmt.Errorf("update: unexpected %q in the update", h.Name)
		}
		names = append(names, name)
		if err := writeFile(filepath.Join(dir, name+partial), tr); err != nil {
			return names, err
		}
	}
}

// validName accepts the names a slot's files have: lower case, digits, dots,
// dashes, with no way out of the slot.
func validName(n string) bool {
	if n == "" || len(n) > 64 || n[0] == '.' || n[0] == '-' || strings.HasSuffix(n, partial) {
		return false
	}
	for _, c := range n {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func writeFile(path string, r io.Reader) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// check makes sure an update is a system slot of release tag: GRUB boots a
// slot by its slot.cfg, and version says which release it holds.
func check(dir string, names []string, tag string) error {
	for _, need := range []string{"slot.cfg", "version"} {
		if !slices.Contains(names, need) {
			return fmt.Errorf("update: the update has no %s", need)
		}
	}
	v, err := os.ReadFile(filepath.Join(dir, "version"+partial))
	if err != nil {
		return err
	}
	if got := strings.TrimSpace(string(v)); got != tag {
		return fmt.Errorf("update: the update is %q, not %s", got, tag)
	}
	return nil
}

// clearSlot empties a slot's file system, but for what mkfs put there.
func clearSlot(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == "lost+found" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// stallAfter is how long a download may go without a byte before it is given
// up on: a connection that died without closing never ends by itself.
var stallAfter = 60 * time.Second

// watchdog cancels a download that has stalled.
type watchdog struct {
	r     io.Reader
	timer *time.Timer
	bit   atomic.Bool
}

func watch(r io.Reader, cancel func()) *watchdog {
	w := &watchdog{r: r}
	w.timer = time.AfterFunc(stallAfter, func() {
		w.bit.Store(true)
		cancel()
	})
	return w
}

func (w *watchdog) Read(p []byte) (int, error) {
	n, err := w.r.Read(p)
	if n > 0 {
		w.timer.Reset(stallAfter)
	}
	return n, err
}

func (w *watchdog) stop()       { w.timer.Stop() }
func (w *watchdog) fired() bool { return w.bit.Load() }

// counter passes the download's progress on, a MiB at a time.
type counter struct {
	r          io.Reader
	done, told int64
	total      int64
	progress   Progress
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	if c.done-c.told >= 1<<20 {
		c.report()
	}
	return n, err
}

func (c *counter) report() {
	c.told = c.done
	if c.progress != nil {
		c.progress(c.done, c.total)
	}
}

// triedPath remembers, across the restart, the release the next boot tries
// and the slot it is in; failedPath, a release that did not come up here.
var (
	triedPath  = "/data/simpleclient/update-try"
	failedPath = "/data/simpleclient/update-failed"
)

func remember(tag, slot string) {
	_ = os.MkdirAll(filepath.Dir(triedPath), 0o755)
	_ = os.WriteFile(triedPath, []byte(tag+" "+slot+"\n"), 0o644)
}

// tried reads which release the last update had the next boot try, and in
// which slot.
func tried() (tag, slot string) {
	b, _ := os.ReadFile(triedPath)
	if f := strings.Fields(string(b)); len(f) == 2 {
		return f[0], f[1]
	}
	return "", ""
}

// Settle looks at how the last update's try went, as this boot sees it. A
// boot from the slot that was tried is that release up and running: tried is
// its tag and ok is set. A boot from the other slot means the release never
// came up on this machine and GRUB went back to the slot that works: tried is
// its tag, ok is not set, and the release is remembered as failed until an
// update does come up. failed is that release, if there is one.
//
// A try that came up stays on record until Kept: should this boot die before
// it has proven itself, the next one, from the other slot, finds the try
// failed.
func Settle(boot bootenv.Boot) (tag string, ok bool, failed string) {
	tag, slot := tried()
	switch {
	case tag == "":
	case slot == boot.Slot:
		ok = true
	default:
		os.Remove(triedPath)
		_ = os.WriteFile(failedPath, []byte(tag+"\n"), 0o644)
	}
	b, _ := os.ReadFile(failedPath)
	return tag, ok, strings.TrimSpace(string(b))
}

// Kept closes the last update's try once this boot has proven itself
// (bootenv.MarkGood), if it is the boot of the slot that was tried: the
// release is there to stay, and none is remembered as failed.
func Kept(boot bootenv.Boot) {
	if tag, slot := tried(); tag != "" && slot == boot.Slot {
		os.Remove(triedPath)
		os.Remove(failedPath)
	}
}
