package usb

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// AgentPort is where a Linux server's SimpleClient agent listens. Installed
// by server/simpleclient-server.sh, it attaches the devices a kiosk offers over
// USB/IP for as long as the kiosk keeps its connection to it open.
const AgentPort = 3241

// agentDialTimeout bounds the look for an agent before every connection. A
// Windows firewall drops the attempt instead of refusing it, so this is time
// added to connecting to a Windows server; on a LAN an agent answers in a
// millisecond.
const agentDialTimeout = 500 * time.Millisecond

// Session is the kiosk's devices for one connection to a server.
type Session struct {
	server net.IP
	exp    *Exporter // nil: the server has no agent, and RDP carries the devices
	stop   chan struct{}
	done   chan struct{}
}

// Begin sets up the devices for a session with server. A Linux server running
// SimpleClient's agent gets them over USB/IP. Any other — Windows, or xrdp
// without the agent — gets them through RDP, for which Args are the engine's
// options.
func Begin(server net.IP) *Session {
	s := &Session{server: server, stop: make(chan struct{}), done: make(chan struct{})}
	c := dialAgent(server)
	if c == nil {
		close(s.done)
		if !startPCSC() {
			log.Printf("usb: no PC/SC daemon; smart cards stay here")
		}
		return s
	}
	// Smart-card readers go to the server whole: the daemon here would hold
	// them.
	stopPCSC()
	exp, err := Export(server)
	if err != nil {
		log.Printf("usb: USB/IP: %v", err)
		c.Close()
		close(s.done)
		startPCSC()
		return s
	}
	log.Printf("usb: %s runs SimpleClient's agent: devices go over USB/IP", server)
	s.exp = exp
	go s.run(c)
	return s
}

// Args are the engine's options for the devices: drives always, by name;
// for a server without the agent also smart cards through its PC/SC
// redirection and the rest as USB devices — those plugged in now by address,
// and any plugged in during the session as they come. Hotplugged devices of
// the kiosk's own classes stay: sound, input, storage (a drive, offered by
// name), hubs, smart-card readers (PC/SC), Bluetooth.
func (s *Session) Args() []string {
	args := []string{"/drive:hotplug,*"}
	if s.exp == nil {
		args = append(args, s.rdpDevices()...)
	}
	log.Printf("usb: engine options %s", strings.Join(args, " "))
	return args
}

// rdpDevices are the options for a server that gets every device through RDP.
func (s *Session) rdpDevices() []string {
	var args []string
	if pcscRunning() {
		args = append(args, "/smartcard")
	}
	var addrs []string
	for _, d := range Scan() {
		if d.Kind() == Redirect && !d.SmartCard() {
			addrs = append(addrs, fmt.Sprintf("%x:%x", d.Bus, d.Dev))
		}
	}
	usb := "/usb:"
	if len(addrs) > 0 {
		usb += "addr:" + strings.Join(addrs, "#") + ","
	}
	return append(args, usb+"auto,filter:1#3#8#9#b#e0")
}

// End gives every device back.
func (s *Session) End() {
	if s.exp == nil {
		return
	}
	close(s.stop)
	<-s.done
}

// run keeps the agent told what is on offer: DEVICES when that changes,
// PING otherwise, which is how it knows the kiosk is still there. A lost
// connection is made again while the session lasts.
func (s *Session) run(c net.Conn) {
	defer close(s.done)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	last, lastSent := "", time.Time{}
	for {
		select {
		case <-s.stop:
			if c != nil {
				c.Close()
			}
			s.exp.Close()
			return
		case <-tick.C:
		}
		s.exp.Check()
		if c == nil {
			if c = dialAgent(s.server); c == nil {
				continue
			}
			last = ""
		}
		offered := s.exp.Offered()
		ids := make([]string, len(offered))
		for i, d := range offered {
			ids[i] = d.BusID
		}
		now := strings.Join(ids, " ")
		msg := ""
		switch {
		case now != last:
			msg = fmt.Sprintf("DEVICES %d\n", len(offered))
		case time.Since(lastSent) > 10*time.Second:
			msg = "PING\n"
		default:
			continue
		}
		c.SetWriteDeadline(time.Now().Add(3 * time.Second))
		if _, err := io.WriteString(c, msg); err != nil {
			c.Close()
			c = nil
			continue
		}
		last, lastSent = now, time.Now()
	}
}

// dialAgent opens the connection to server's agent, or returns nil when it
// has none.
func dialAgent(server net.IP) net.Conn {
	c, err := net.DialTimeout("tcp", net.JoinHostPort(server.String(), strconv.Itoa(AgentPort)), agentDialTimeout)
	if err != nil {
		return nil
	}
	c.SetDeadline(time.Now().Add(2 * time.Second))
	line := ""
	if _, err = io.WriteString(c, "SIMPLECLIENT-USB 1\n"); err == nil {
		line, err = bufio.NewReader(c).ReadString('\n')
	}
	if err != nil || !strings.HasPrefix(line, "OK") {
		c.Close()
		return nil
	}
	c.SetDeadline(time.Time{})
	return c
}

// The PC/SC daemon, which RDP's smart-card redirection talks to.
const (
	pcscdPath = "/usr/sbin/pcscd"
	pcscDir   = "/run/pcscd"
)

var pcsc struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	exited chan struct{}
}

// startPCSC runs pcscd unless it is running already.
func startPCSC() bool {
	pcsc.mu.Lock()
	defer pcsc.mu.Unlock()
	if pcsc.cmd != nil {
		select {
		case <-pcsc.exited:
			pcsc.cmd = nil
		default:
			return true
		}
	}
	if _, err := os.Stat(pcscdPath); err != nil {
		return false
	}
	// What a daemon that is no longer running left behind would make a new
	// one refuse to start.
	os.MkdirAll(pcscDir, 0o755)
	os.Remove(pcscDir + "/pcscd.comm")
	os.Remove(pcscDir + "/pcscd.pid")
	cmd := exec.Command(pcscdPath, "--foreground")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		log.Printf("usb: pcscd: %v", err)
		return false
	}
	exited := make(chan struct{})
	go func() {
		cmd.Wait()
		close(exited)
	}()
	pcsc.cmd, pcsc.exited = cmd, exited
	for i := 0; i < 40; i++ {
		if _, err := os.Stat(pcscDir + "/pcscd.comm"); err == nil {
			log.Printf("usb: PC/SC daemon started")
			return true
		}
		select {
		case <-exited:
			pcsc.cmd = nil
			log.Printf("usb: pcscd exited at start")
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
	return true
}

// stopPCSC stops the daemon, which lets go of the readers.
func stopPCSC() {
	pcsc.mu.Lock()
	defer pcsc.mu.Unlock()
	if pcsc.cmd == nil {
		return
	}
	pcsc.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-pcsc.exited:
	case <-time.After(2 * time.Second):
		pcsc.cmd.Process.Kill()
		<-pcsc.exited
	}
	pcsc.cmd = nil
}

func pcscRunning() bool {
	pcsc.mu.Lock()
	defer pcsc.mu.Unlock()
	if pcsc.cmd == nil {
		return false
	}
	select {
	case <-pcsc.exited:
		pcsc.cmd = nil
		return false
	default:
		return true
	}
}
