package update

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/diggyen/SimpleClient/internal/bootenv"
)

// slotTar builds an update holding files, in order.
func slotTar(t *testing.T, files ...[2]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for _, f := range files {
		if err := w.WriteHeader(&tar.Header{Name: f[0], Mode: 0o644, Size: int64(len(f[1])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(f[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func goodTar(t *testing.T, tag string) []byte {
	return slotTar(t,
		[2]string{"vmlinuz-new", "kernel"},
		[2]string{"initramfs.cpio.gz", strings.Repeat("i", 3<<20)},
		[2]string{"slot.cfg", "linux /vmlinuz-${sc_k}\n"},
		[2]string{"version", tag + "\n"})
}

func sumOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// server is a release server with one release, tag, whose update is upd. It
// announces the release by redirect, the way GitHub does.
func server(t *testing.T, tag string, upd []byte, sums string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("/releases/download/"+tag+"/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(sums))
	})
	mux.HandleFunc("/releases/download/"+tag+"/"+Asset, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(upd)))
		w.Write(upd)
	})
	return tlsServer(t, mux)
}

// tlsServer is an HTTPS server the kiosk trusts for the test.
func tlsServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	s := httptest.NewTLSServer(h)
	old := transport.TLSClientConfig.RootCAs
	pool := x509.NewCertPool()
	pool.AddCert(s.Certificate())
	transport.TLSClientConfig.RootCAs = pool
	t.Cleanup(func() {
		s.Close()
		transport.TLSClientConfig.RootCAs = old
		transport.CloseIdleConnections()
	})
	return s
}

// useServer points the kiosk at base for the test.
func useServer(t *testing.T, base string) {
	t.Helper()
	old := basePath
	basePath = filepath.Join(t.TempDir(), "update-url")
	if err := os.WriteFile(basePath, []byte(base+"/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { basePath = old })
}

func TestLatestFollowsGitHubsRedirect(t *testing.T) {
	upd := goodTar(t, "v0.7.1")
	sums := "0000000000000000000000000000000000000000000000000000000000000000  simpleclient.iso\n" +
		sumOf(upd) + "  " + Asset + "\n"
	s := server(t, "v0.7.1", upd, sums)
	useServer(t, s.URL+"/releases")

	r, err := Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Tag != "v0.7.1" || r.Sum != sumOf(upd) || r.Base != s.URL+"/releases" {
		t.Fatalf("got %+v", r)
	}
}

func TestLatestReadsAMirrorsFile(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("v1.2.3\n")) })
	mux.HandleFunc("/download/v1.2.3/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("ab", 32) + " *" + Asset + "\n"))
	})
	s := tlsServer(t, mux)
	useServer(t, s.URL)

	r, err := Latest(context.Background())
	if err != nil || r.Tag != "v1.2.3" || r.Sum != strings.Repeat("ab", 32) {
		t.Fatalf("got %+v, %v", r, err)
	}
}

// A release from before updates, v0.6.0, has an ISO and nothing to update a
// disk install with.
func TestLatestSaysWhenThereIsNoUpdate(t *testing.T) {
	s := server(t, "v0.6.0", nil, strings.Repeat("0", 64)+"  simpleclient.iso\n")
	useServer(t, s.URL+"/releases")

	r, err := Latest(context.Background())
	if !errors.Is(err, ErrNoUpdate) || r.Tag != "v0.6.0" {
		t.Fatalf("got %+v, %v", r, err)
	}
}

func TestLatestRejectsWhatIsNotATag(t *testing.T) {
	for _, loc := range []string{"/releases", "/releases/tag/", "/releases/tag/..%2F..%2Fetc", "/releases/tag/v1%3Fx"} {
		mux := http.NewServeMux()
		mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, loc, http.StatusFound)
		})
		s := tlsServer(t, mux)
		useServer(t, s.URL)
		if r, err := Latest(context.Background()); err == nil {
			t.Errorf("redirect to %s: got release %+v", loc, r)
		}
		s.Close()
	}
}

// The kiosk runs what it downloads: the update, and what it is checked
// against, come over HTTPS or not at all.
func TestOnlyHTTPS(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("asked for %s over plain HTTP", r.URL)
	}))
	defer plain.Close()
	useServer(t, plain.URL)
	if _, err := Latest(context.Background()); !errors.Is(err, ErrInsecure) {
		t.Fatalf("plain HTTP mirror: got %v, want ErrInsecure", err)
	}
	dir := t.TempDir()
	fill(t, dir)
	r := Release{Tag: "v0.7.1", Sum: strings.Repeat("0", 64), Base: plain.URL}
	if err := unpack(context.Background(), r, dir, nil); !errors.Is(err, ErrInsecure) {
		t.Fatalf("plain HTTP download: got %v, want ErrInsecure", err)
	}
	if got := list(t, dir); got != "lost+found slot.cfg version vmlinuz-lts vmlinuz-new" {
		t.Fatalf("slot holds %s", got)
	}

	// Nor can an HTTPS server send the kiosk to plain HTTP.
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("v0.7.1\n")) })
	mux.HandleFunc("/download/v0.7.1/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+r.URL.Path, http.StatusFound)
	})
	s := tlsServer(t, mux)
	useServer(t, s.URL)
	if _, err := Latest(context.Background()); !errors.Is(err, ErrInsecure) {
		t.Fatalf("redirect to plain HTTP: got %v, want ErrInsecure", err)
	}
	r.Base = s.URL
	if err := unpack(context.Background(), r, dir, nil); !errors.Is(err, ErrInsecure) {
		t.Fatalf("download redirected to plain HTTP: got %v, want ErrInsecure", err)
	}
}

// A mirror whose certificate is from its own CA is trusted once that CA is in
// update-ca.pem, and not before.
func TestRootsTrustTheMirrorsCA(t *testing.T) {
	s := httptest.NewTLSServer(http.NotFoundHandler())
	defer s.Close()
	old := transport.TLSClientConfig.RootCAs
	defer func() {
		transport.TLSClientConfig.RootCAs = old
		transport.CloseIdleConnections()
	}()
	path := filepath.Join(t.TempDir(), "update-ca.pem")

	transport.TLSClientConfig.RootCAs = roots(path)
	if transport.TLSClientConfig.RootCAs != nil {
		t.Fatal("no update-ca.pem: want the image's bundle alone")
	}
	if resp, err := get(context.Background(), client, s.URL+"/x"); err == nil {
		resp.Body.Close()
		t.Fatal("a certificate of an unknown CA was trusted")
	}

	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw})
	if err := os.WriteFile(path, ca, 0o644); err != nil {
		t.Fatal(err)
	}
	transport.TLSClientConfig.RootCAs = roots(path)
	resp, err := get(context.Background(), client, s.URL+"/x")
	if err != nil {
		t.Fatalf("with its CA in update-ca.pem: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got %s", resp.Status)
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		tag, current string
		want         bool
	}{
		{"v0.7.1", "v0.7.0", true},
		{"v0.10.0", "v0.9.3", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.7.0", "v0.7.0", false},
		{"v0.6.0", "v0.7.0", false},
		{"v0.7.0", "v0.7.0-3-g1a2b3c4-dirty", false},
		{"v0.7.1", "v0.7.0-3-g1a2b3c4", true},
		{"v0.7.0", "dev", true},
		{"v0.7.0-rc1", "v0.6.2", true},
		{"latest", "v0.6.0", false},
		{"v1.2", "v0.6.0", false},
	} {
		if got := Newer(c.tag, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.tag, c.current, got, c.want)
		}
	}
}

// fill puts an older release's files in a slot.
func fill(t *testing.T, dir string) {
	t.Helper()
	for _, n := range []string{"vmlinuz-new", "vmlinuz-lts", "slot.cfg", "version"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "lost+found"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func list(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, " ")
}

func TestUnpackReplacesTheSlot(t *testing.T) {
	upd := goodTar(t, "v0.7.1")
	s := server(t, "v0.7.1", upd, "")
	dir := t.TempDir()
	fill(t, dir)

	var last, total int64
	err := unpack(context.Background(), Release{Tag: "v0.7.1", Sum: sumOf(upd), Base: s.URL + "/releases"}, dir,
		func(done, all int64) { last, total = done, all })
	if err != nil {
		t.Fatal(err)
	}
	if got := list(t, dir); got != "initramfs.cpio.gz lost+found slot.cfg version vmlinuz-new" {
		t.Fatalf("slot holds %s", got)
	}
	if v, _ := os.ReadFile(filepath.Join(dir, "version")); string(v) != "v0.7.1\n" {
		t.Fatalf("version %q", v)
	}
	if last != int64(len(upd)) || total != int64(len(upd)) {
		t.Fatalf("progress ended at %d of %d, the update is %d bytes", last, total, len(upd))
	}
}

// Whatever goes wrong, the slot is left empty rather than holding part of a
// release, or a release other than the one announced.
func TestUnpackLeavesNothingOfAFailedUpdate(t *testing.T) {
	good := goodTar(t, "v0.7.1")
	damaged := bytes.Clone(good)
	damaged[600] ^= 1
	for _, c := range []struct {
		name string
		upd  []byte
		sum  string
		want error
	}{
		{"damaged", damaged, sumOf(good), ErrDamaged},
		{"another release", goodTar(t, "v0.7.2"), sumOf(goodTar(t, "v0.7.2")), nil},
		{"no slot.cfg", slotTar(t, [2]string{"vmlinuz-new", "k"}, [2]string{"version", "v0.7.1"}), "", nil},
		{"a path", slotTar(t, [2]string{"../escape", "x"}, [2]string{"slot.cfg", "x"}), "", nil},
		{"a partial name", slotTar(t, [2]string{"version.part", "x"}), "", nil},
		{"cut short", good[:len(good)/2], "", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.sum == "" {
				c.sum = sumOf(c.upd)
			}
			s := server(t, "v0.7.1", c.upd, "")
			dir := t.TempDir()
			fill(t, dir)
			err := unpack(context.Background(), Release{Tag: "v0.7.1", Sum: c.sum, Base: s.URL + "/releases"}, dir, nil)
			if err == nil || (c.want != nil && !errors.Is(err, c.want)) {
				t.Fatalf("got error %v, want %v", err, c.want)
			}
			if got := list(t, dir); got != "lost+found" {
				t.Fatalf("slot holds %s", got)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape")); err == nil {
				t.Fatal("a file was written outside the slot")
			}
		})
	}
}

// A connection that goes quiet without closing ends the download, rather than
// leaving it waiting for ever.
func TestUnpackGivesUpOnAStalledDownload(t *testing.T) {
	old := stallAfter
	stallAfter = 300 * time.Millisecond
	defer func() { stallAfter = old }()

	release := make(chan struct{})
	defer close(release)
	mux := http.NewServeMux()
	mux.HandleFunc("/download/v0.7.1/"+Asset, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10000000")
		w.Write(make([]byte, 4096))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	s := tlsServer(t, mux)

	dir := t.TempDir()
	start := time.Now()
	err := unpack(context.Background(), Release{Tag: "v0.7.1", Sum: strings.Repeat("0", 64), Base: s.URL}, dir, nil)
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("got %v, want ErrStalled", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v to notice", d)
	}
}

func TestSettleTellsHowATryWent(t *testing.T) {
	dir := t.TempDir()
	oldT, oldF := triedPath, failedPath
	triedPath, failedPath = filepath.Join(dir, "update-try"), filepath.Join(dir, "update-failed")
	defer func() { triedPath, failedPath = oldT, oldF }()
	a, b := bootenv.Boot{Slot: "A"}, bootenv.Boot{Slot: "B"}
	settle := func(boot bootenv.Boot) string {
		tag, ok, failed := Settle(boot)
		return fmt.Sprintf("%s %v %s", tag, ok, failed)
	}

	if got := settle(a); got != " false " {
		t.Fatalf("nothing tried: got %q", got)
	}

	// v0.7.1 went into B, and the machine came back up on A: once it says
	// so, and it remembers.
	remember("v0.7.1", "B")
	if got := settle(a); got != "v0.7.1 false v0.7.1" {
		t.Fatalf("got %q", got)
	}
	if got := settle(a); got != " false v0.7.1" {
		t.Fatalf("at the next boot: got %q", got)
	}

	// v0.7.2 went into B and came up there; until that boot has proven
	// itself, a boot of A would still find it failed.
	remember("v0.7.2", "B")
	if got := settle(b); got != "v0.7.2 true v0.7.1" {
		t.Fatalf("got %q", got)
	}
	Kept(a)
	if got := settle(b); got != "v0.7.2 true v0.7.1" {
		t.Fatalf("kept by the wrong slot: got %q", got)
	}
	Kept(b)
	if got := settle(b); got != " false " {
		t.Fatalf("after it was kept: got %q", got)
	}

	// v0.7.3 came up in A, and that boot died before it proved itself.
	remember("v0.7.3", "A")
	if got := settle(a); got != "v0.7.3 true " {
		t.Fatalf("got %q", got)
	}
	if got := settle(b); got != "v0.7.3 false v0.7.3" {
		t.Fatalf("back on B: got %q", got)
	}
}

// An update that cannot even start leaves the slot as it was: it holds the
// version before this one, which the boot menu still offers.
func TestUnpackKeepsTheSlotWhenTheDownloadDoesNotStart(t *testing.T) {
	s := tlsServer(t, http.NotFoundHandler())
	dir := t.TempDir()
	fill(t, dir)
	if err := unpack(context.Background(), Release{Tag: "v0.7.1", Sum: strings.Repeat("0", 64), Base: s.URL}, dir, nil); err == nil {
		t.Fatal("a missing update was installed")
	}
	if got := list(t, dir); got != "lost+found slot.cfg version vmlinuz-lts vmlinuz-new" {
		t.Fatalf("slot holds %s", got)
	}
}
