package scanner

import (
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"time"
)

// nameTimeout bounds asking one RDP server its name.
const nameTimeout = time.Second

// x224Request is an X.224 Connection Request carrying an RDP Negotiation
// Request for TLS or CredSSP ([MS-RDPBCGR] 2.2.1.1), the way every RDP
// connection starts.
var x224Request = []byte{
	0x03, 0x00, 0x00, 0x13, // TPKT, 19 bytes in all
	0x0e, 0xe0, 0x00, 0x00, 0x00, 0x00, 0x00, // Connection Request
	0x01, 0x00, 0x08, 0x00, 0x03, 0x00, 0x00, 0x00, // RDP_NEG_REQ: PROTOCOL_SSL | PROTOCOL_HYBRID
}

// Every suite Go has, the insecure ones included: Windows 7 and Server 2008 R2
// speak TLS 1.0 with RSA key exchange only, and nothing secret goes over a
// connection that only reads a certificate.
var nameSuites = func() []uint16 {
	var ids []uint16
	for _, s := range append(tls.CipherSuites(), tls.InsecureCipherSuites()...) {
		ids = append(ids, s.ID)
	}
	return ids
}()

// rdpName asks the RDP server at the other end of conn its computer name: the
// subject of the certificate it opens TLS with. Windows makes that certificate
// for the computer's own name, and xrdp on Debian and Ubuntu uses the
// machine's snakeoil certificate, made for its host name. It names the
// computers no DNS server knows by their address, the common case on an
// office network, where the list showed only addresses. "" when the server
// does not say: standard RDP security has no certificate.
func rdpName(conn net.Conn) string {
	_ = conn.SetDeadline(time.Now().Add(nameTimeout))
	if _, err := conn.Write(x224Request); err != nil {
		return ""
	}

	// TPKT header, then the Connection Confirm and, from a server that
	// negotiates, its RDP_NEG_RSP.
	var tpkt [4]byte
	if _, err := io.ReadFull(conn, tpkt[:]); err != nil || tpkt[0] != 0x03 {
		return ""
	}
	n := int(binary.BigEndian.Uint16(tpkt[2:]))
	if n < 4+7+8 || n > 64 {
		return ""
	}
	resp := make([]byte, n-4)
	if _, err := io.ReadFull(conn, resp); err != nil {
		return ""
	}
	// Connection Confirm, a negotiation response (not a failure), and a
	// protocol that runs over TLS.
	if resp[1]&0xf0 != 0xd0 || resp[7] != 0x02 || binary.LittleEndian.Uint32(resp[11:15])&0x0b == 0 {
		return ""
	}

	tc := tls.Client(conn, &tls.Config{
		InsecureSkipVerify: true, // the name is all that is read
		MinVersion:         tls.VersionTLS10,
		CipherSuites:       nameSuites,
	})
	if err := tc.Handshake(); err != nil {
		return ""
	}
	certs := tc.ConnectionState().PeerCertificates
	_ = tc.Close()
	if len(certs) == 0 {
		return ""
	}
	return certName(certs[0].Subject.CommonName)
}

// certName is the computer name in a certificate's common name: the first
// label of a host name, "PC01" of "PC01.corp.local", as Windows itself shows
// it. Nothing for an address, or for localhost, which names no machine in
// particular.
func certName(cn string) string {
	cn = strings.TrimSpace(cn)
	if cn == "" || net.ParseIP(cn) != nil {
		return ""
	}
	name, _, _ := strings.Cut(cn, ".")
	if name == "" || strings.EqualFold(name, "localhost") || strings.ContainsAny(name, " *") {
		return ""
	}
	return name
}
