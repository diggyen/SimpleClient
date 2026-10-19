package usb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The kiosk's side of USB/IP: what usbipd does, for the one server a session
// is with. The protocol here only lists devices and hands one over; from then
// on the device's traffic runs in the kernel, between the usbip-host driver
// here and vhci-hcd on the server.
const (
	usbipPort    = 3240
	usbipVersion = 0x0111

	opReqDevlist = 0x8005
	opRepDevlist = 0x0005
	opReqImport  = 0x8003
	opRepImport  = 0x0003

	stOK    = 0x00
	stNA    = 0x01
	stNoDev = 0x04

	usbipHost = "usbip-host"
)

// sysDrivers is where USB drivers are bound and unbound.
var sysDrivers = "/sys/bus/usb/drivers"

// Exporter offers the kiosk's Redirect devices to one server over USB/IP.
type Exporter struct {
	server net.IP
	ln     net.Listener

	mu     sync.Mutex
	bound  map[string]Device // devices handed to usbip-host, as they were before
	closed bool
}

// Export starts serving devices to server: connections from anywhere else
// are closed unanswered.
func Export(server net.IP) (*Exporter, error) {
	// The driver is loaded on first use; it is not needed otherwise.
	_ = exec.Command("modprobe", "-q", "usbip-host").Run()
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", usbipPort))
	if err != nil {
		return nil, err
	}
	e := &Exporter{server: server, ln: ln, bound: map[string]Device{}}
	go e.accept()
	return e, nil
}

func (e *Exporter) accept() {
	for {
		c, err := e.ln.Accept()
		if err != nil {
			return
		}
		host, _, _ := net.SplitHostPort(c.RemoteAddr().String())
		if ip := net.ParseIP(host); ip == nil || !ip.Equal(e.server) {
			log.Printf("usb: USB/IP connection from %s refused: the session is with %s", host, e.server)
			c.Close()
			continue
		}
		go e.serve(c)
	}
}

// serve answers one request. An import hands the connection to the kernel,
// which keeps it for as long as the server uses the device.
func (e *Exporter) serve(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	var hdr [8]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		return
	}
	switch binary.BigEndian.Uint16(hdr[2:]) {
	case opReqDevlist:
		c.Write(devlistReply(e.Offered()))
	case opReqImport:
		var id [32]byte
		if _, err := io.ReadFull(c, id[:]); err != nil {
			return
		}
		busID := strings.TrimRight(string(id[:]), "\x00")
		if err := e.importDevice(c, busID); err != nil {
			log.Printf("usb: %s to %s over USB/IP: %v", busID, e.server, err)
		}
	}
}

// Offered lists the devices the server may take: the Redirect ones, and the
// ones already handed to usbip-host, which no longer describe themselves
// (a device bound there is unconfigured until the server configures it).
func (e *Exporter) Offered() []Device {
	e.mu.Lock()
	defer e.mu.Unlock()
	var devs []Device
	for _, d := range Scan() {
		if b, ok := e.bound[d.BusID]; ok && d.Driver == usbipHost {
			b.Bus, b.Dev, b.Path = d.Bus, d.Dev, d.Path
			devs = append(devs, b)
		} else if d.Kind() == Redirect {
			devs = append(devs, d)
		}
	}
	return devs
}

func (e *Exporter) importDevice(c net.Conn, busID string) error {
	var d Device
	found := false
	for _, o := range e.Offered() {
		if o.BusID == busID {
			d, found = o, true
		}
	}
	if !found {
		c.Write(opHeader(opRepImport, stNoDev))
		return errors.New("no such device to offer")
	}
	if err := e.bind(d); err != nil {
		c.Write(opHeader(opRepImport, stNA))
		return err
	}
	tc, ok := c.(*net.TCPConn)
	if !ok {
		return errors.New("not a TCP connection")
	}
	tc.SetNoDelay(true)
	f, err := tc.File()
	if err != nil {
		c.Write(opHeader(opRepImport, stNA))
		return err
	}
	defer f.Close()
	// usbipd's order: the kernel first, then the reply. Nothing more is read
	// from the connection here; the kernel's thread does that now.
	if err := writeAttr(filepath.Join(sysDevices, busID, "usbip_sockfd"), fmt.Sprintf("%d\n", f.Fd())); err != nil {
		c.Write(opHeader(opRepImport, stNA))
		return err
	}
	reply := append(opHeader(opRepImport, stOK), deviceRecord(d)...)
	if _, err := f.Write(reply); err != nil {
		return err
	}
	log.Printf("usb: %s now used by %s over USB/IP", d, e.server)
	return nil
}

// bind takes a device from its driver here and gives it to usbip-host, as
// "usbip bind" does.
func (e *Exporter) bind(d Device) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return errors.New("the session has ended")
	}
	if _, ok := e.bound[d.BusID]; !ok {
		if d.Driver != "" && d.Driver != usbipHost {
			if err := writeAttr(filepath.Join(sysDrivers, d.Driver, "unbind"), d.BusID); err != nil {
				return fmt.Errorf("unbind from %s: %w", d.Driver, err)
			}
		}
		if err := writeAttr(filepath.Join(sysDrivers, usbipHost, "match_busid"), "add "+d.BusID); err != nil {
			return fmt.Errorf("usbip-host: %w", err)
		}
		if err := writeAttr(filepath.Join(sysDrivers, usbipHost, "bind"), d.BusID); err != nil {
			release(d.BusID)
			return fmt.Errorf("usbip-host bind: %w", err)
		}
		e.bound[d.BusID] = d
	}
	// 1: available, 2: in use.
	for i := 0; i < 40; i++ {
		switch attr(filepath.Join(sysDevices, d.BusID), "usbip_status") {
		case "1":
			return nil
		case "2":
			return errors.New("already in use")
		}
		time.Sleep(50 * time.Millisecond)
	}
	return errors.New("usbip-host did not take the device")
}

// Check forgets bound devices that have been unplugged. usbip-host keeps
// claiming a port it was given, so whatever goes into it next would be the
// server's too — a keyboard plugged in where a printer was would stop typing
// here. A device in such a port that is not the one bound there is given back.
func (e *Exporter) Check() {
	present := map[string]Device{}
	for _, d := range Scan() {
		present[d.BusID] = d
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, b := range e.bound {
		if d, ok := present[id]; !ok || d.Vendor != b.Vendor || d.Product != b.Product {
			release(id)
			delete(e.bound, id)
			log.Printf("usb: %s unplugged, its port given back", id)
		}
	}
}

// Close stops serving and gives every device back to its own driver here:
// the server sees it unplugged.
func (e *Exporter) Close() {
	e.ln.Close()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	for id := range e.bound {
		release(id)
		delete(e.bound, id)
		log.Printf("usb: %s given back", id)
	}
}

// release gives a device back to the kiosk's drivers, as "usbip unbind"
// does.
func release(busID string) {
	host := filepath.Join(sysDrivers, usbipHost)
	writeAttr(filepath.Join(host, "unbind"), busID)
	writeAttr(filepath.Join(host, "match_busid"), "del "+busID)
	writeAttr(filepath.Join(host, "rebind"), busID)
}

// ReleaseStale gives back devices a previous run of the kiosk left with
// usbip-host — it was stopped in the middle of a session.
func ReleaseStale() {
	for _, id := range strings.Fields(attr(filepath.Join(sysDrivers, usbipHost), "match_busid")) {
		release(id)
		log.Printf("usb: %s given back from an earlier session", id)
	}
}

func writeAttr(path, value string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, err = f.WriteString(value)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// opHeader is USB/IP's op_common: version, code, status.
func opHeader(code uint16, status uint32) []byte {
	b := binary.BigEndian.AppendUint16(nil, usbipVersion)
	b = binary.BigEndian.AppendUint16(b, code)
	return binary.BigEndian.AppendUint32(b, status)
}

// deviceRecord is struct usbip_usb_device: 312 bytes, big-endian.
func deviceRecord(d Device) []byte {
	var path [256]byte
	var busID [32]byte
	copy(path[:len(path)-1], d.Path)
	copy(busID[:len(busID)-1], d.BusID)
	b := append(path[:], busID[:]...)
	b = binary.BigEndian.AppendUint32(b, d.Bus)
	b = binary.BigEndian.AppendUint32(b, d.Dev)
	b = binary.BigEndian.AppendUint32(b, d.Speed)
	b = binary.BigEndian.AppendUint16(b, d.Vendor)
	b = binary.BigEndian.AppendUint16(b, d.Product)
	b = binary.BigEndian.AppendUint16(b, d.BCD)
	return append(b, d.Class, d.SubClass, d.Protocol, d.Config, d.NumConfigs, uint8(len(d.Interfaces)))
}

// devlistReply is OP_REP_DEVLIST: the device count, then each device with
// its interfaces.
func devlistReply(devs []Device) []byte {
	b := opHeader(opRepDevlist, stOK)
	b = binary.BigEndian.AppendUint32(b, uint32(len(devs)))
	for _, d := range devs {
		b = append(b, deviceRecord(d)...)
		for _, i := range d.Interfaces {
			b = append(b, i.Class, i.SubClass, i.Protocol, 0)
		}
	}
	return b
}
