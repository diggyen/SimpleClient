package scanner

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"testing"
	"time"
)

// fakeRDP answers one connection the way an RDP server does: the Connection
// Confirm with the given negotiation response, then TLS with a certificate for
// cn when the response selects it.
func fakeRDP(t *testing.T, neg []byte, cn string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		req := make([]byte, len(x224Request))
		if _, err := io.ReadFull(c, req); err != nil {
			return
		}
		cc := append([]byte{0x03, 0x00, 0x00, byte(4 + 7 + len(neg)), byte(6 + len(neg)), 0xd0, 0, 0, 0x12, 0x34, 0}, neg...)
		if _, err := c.Write(cc); err != nil {
			return
		}
		ts := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{cert}})
		_ = ts.Handshake()
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	return rdpName(conn)
}

func TestRDPName(t *testing.T) {
	selected := func(p byte) []byte { return []byte{0x02, 0x00, 0x08, 0x00, p, 0x00, 0x00, 0x00} }
	cases := []struct {
		name string
		neg  []byte
		cn   string
		want string
	}{
		{"windows NLA", selected(0x02), "PC01.corp.local", "PC01"},
		{"workgroup TLS", selected(0x01), "DESKTOP-7F3K2", "DESKTOP-7F3K2"},
		{"xrdp snakeoil", selected(0x01), "mint-ofis", "mint-ofis"},
		{"standard RDP security", selected(0x00), "PC01", ""},
		{"negotiation failure", []byte{0x03, 0x00, 0x08, 0x00, 0x05, 0x00, 0x00, 0x00}, "PC01", ""},
		{"no negotiation", nil, "PC01", ""},
	}
	for _, c := range cases {
		if got := fakeRDP(t, c.neg, c.cn); got != c.want {
			t.Errorf("%s: rdpName = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCertName(t *testing.T) {
	for cn, want := range map[string]string{
		"PC01.corp.local":       "PC01",
		"dc01":                  "dc01",
		"localhost":             "",
		"localhost.localdomain": "",
		"10.7.7.101":            "",
		"":                      "",
		"*.corp.local":          "",
	} {
		if got := certName(cn); got != want {
			t.Errorf("certName(%q) = %q, want %q", cn, got, want)
		}
	}
}
