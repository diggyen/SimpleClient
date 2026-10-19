package ui

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/diggyen/SimpleClient/internal/domain"
)

// savedPath is where an installed kiosk keeps the servers it has connected to:
// one per line, most recent first, as address, name, username and domain
// separated by tabs. Never a password. The data partition is mounted at /data
// only on an installed kiosk, so the file exists only there.
const savedPath = "/data/simpleclient/hosts"

// maxSaved bounds the file; a kiosk is used with a handful of servers.
const maxSaved = 32

// loadSaved puts the servers saved in path into the list and their accounts
// into lastUser, so an installed kiosk lists them at once, before the scan
// has found anything — and lists the ones on other subnets at all.
func (s *UIState) loadSaved(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		rec := strings.Split(sc.Text(), "\t")
		for len(rec) < 4 {
			rec = append(rec, "")
		}
		h, err := domain.ParseIPAddress(rec[0])
		if err != nil {
			continue
		}
		h.Hostname = rec[1]
		s.AddHost(h)
		s.RememberCredential(h.AddrRDP(), rec[2], rec[3])
	}
}

// saveConnection records a successful connection: the account for the next
// dialog and, on an installed kiosk, the server and account in SavePath, the
// most recent first. Failing to write is not worth bothering anyone about.
func (s *UIState) saveConnection(h domain.Host, user, dom string) {
	s.RememberCredential(h.AddrRDP(), user, dom)
	if s.SavePath == "" {
		return
	}

	lines := []string{strings.Join([]string{h.Address(), h.Hostname, user, dom}, "\t")}
	if f, err := os.Open(s.SavePath); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() && len(lines) < maxSaved {
			rec := strings.SplitN(sc.Text(), "\t", 2)
			if o, err := domain.ParseIPAddress(rec[0]); err == nil && !o.Same(h) {
				lines = append(lines, sc.Text())
			}
		}
		f.Close()
	}

	// Written whole and renamed into place, so a power cut mid-write leaves the
	// old list rather than half of a new one.
	if err := os.MkdirAll(filepath.Dir(s.SavePath), 0o755); err != nil {
		return
	}
	tmp := s.SavePath + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, s.SavePath)
}
