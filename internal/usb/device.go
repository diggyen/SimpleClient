// Package usb hands the kiosk's USB devices to the session. A drive is
// mounted and offered to the server by name, as a drive of its own (RDP drive
// redirection, which Windows and xrdp both serve). Everything else that is not
// the kiosk's own — printers, scanners, webcams, smart-card readers, serial
// adapters — goes to the server whole: over RDP's USB redirection to a Windows
// server, or over USB/IP to a Linux one running SimpleClient's agent, where it
// appears as if plugged in. Either way only to the server the session is with,
// and only while it lasts.
//
// Keyboards, mice and touch screens, headsets, hubs and network adapters stay
// with the kiosk, which needs them. A webcam's microphone goes with the webcam.
package usb

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// sysDevices is where the kernel lists USB devices; tests point it elsewhere.
var sysDevices = "/sys/bus/usb/devices"

// Kind is what the kiosk does with a device.
type Kind int

const (
	// Local devices stay with the kiosk: input, sound, hubs, network
	// adapters, Bluetooth.
	Local Kind = iota
	// Storage is a drive: mounted here and offered to the session by name.
	Storage
	// Redirect devices go to the server whole.
	Redirect
)

func (k Kind) String() string {
	switch k {
	case Storage:
		return "storage"
	case Redirect:
		return "redirect"
	}
	return "local"
}

// The USB classes the kiosk decides on.
const (
	classAudio     = 0x01
	classHID       = 0x03
	classStorage   = 0x08
	classHub       = 0x09
	classSmartCard = 0x0b
	classVideo     = 0x0e
	classWireless  = 0xe0
)

// Interface is one interface of a device's active configuration.
type Interface struct {
	Class, SubClass, Protocol uint8
}

// Device is a USB device as sysfs describes it.
type Device struct {
	BusID      string // the port it is plugged into, e.g. "1-2.3"
	Path       string // its sysfs directory, symlinks resolved
	Bus, Dev   uint32 // bus number and device address
	Speed      uint32 // the kernel's enum usb_device_speed
	Vendor     uint16
	Product    uint16
	BCD        uint16
	Class      uint8
	SubClass   uint8
	Protocol   uint8
	Config     uint8 // bConfigurationValue; 0 while unconfigured
	NumConfigs uint8
	Interfaces []Interface
	Net        bool   // one of its interfaces is a network adapter
	Driver     string // the device's own driver: "usb", or "usbip-host" while exported
	Name       string // its manufacturer and product strings
}

// Kind sorts the device. Anything the kiosk uses itself stays; a drive is a
// drive; the rest is the server's. A printer with a card slot is a printer:
// storage among other functions goes with them. A webcam is a camera, though
// it has a microphone and often a snapshot button: a video call on the server
// wants the microphone in front of the user, and the button is no use here.
func (d Device) Kind() Kind {
	if d.Net || d.Class == classHub || d.Class == classWireless {
		return Local
	}
	for _, i := range d.Interfaces {
		if i.Class == classVideo {
			return Redirect
		}
	}
	storage, other := false, false
	for _, i := range d.Interfaces {
		switch i.Class {
		case classAudio, classHID, classHub, classWireless:
			return Local
		case classStorage:
			storage = true
		default:
			other = true
		}
	}
	switch {
	case other:
		return Redirect
	case storage:
		return Storage
	}
	// No interfaces: an unconfigured device, nothing to judge it by.
	return Local
}

// SmartCard reports whether the device is a smart-card reader, which a
// Windows server gets through RDP's smart-card redirection rather than as a
// USB device.
func (d Device) SmartCard() bool {
	for _, i := range d.Interfaces {
		if i.Class == classSmartCard {
			return true
		}
	}
	return false
}

// String describes the device for the log, e.g.
// `1-2 04f9:0042 "Brother HL-L2350DW" (07 ff) redirect`.
func (d Device) String() string {
	cls := make([]string, 0, len(d.Interfaces))
	for _, i := range d.Interfaces {
		cls = append(cls, fmt.Sprintf("%02x", i.Class))
	}
	return fmt.Sprintf("%s %04x:%04x %q (%s) %s", d.BusID, d.Vendor, d.Product, d.Name,
		strings.Join(cls, " "), d.Kind())
}

// Scan lists the USB devices plugged in now, root hubs left out.
func Scan() []Device {
	entries, err := os.ReadDir(sysDevices)
	if err != nil {
		return nil
	}
	var devs []Device
	for _, e := range entries {
		if !isBusID(e.Name()) {
			continue
		}
		if d, ok := readDevice(e.Name()); ok {
			devs = append(devs, d)
		}
	}
	sort.Slice(devs, func(i, j int) bool { return devs[i].BusID < devs[j].BusID })
	return devs
}

// isBusID reports whether a name under /sys/bus/usb/devices is a device
// ("2-1.4") rather than a root hub ("usb2") or an interface ("2-1.4:1.0").
func isBusID(name string) bool {
	bus, port, ok := strings.Cut(name, "-")
	if !ok || bus == "" || port == "" || strings.Contains(port, ":") {
		return false
	}
	for _, s := range append([]string{bus}, strings.Split(port, ".")...) {
		if _, err := strconv.ParseUint(s, 10, 16); err != nil {
			return false
		}
	}
	return true
}

func readDevice(busID string) (Device, bool) {
	dir := filepath.Join(sysDevices, busID)
	path, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return Device{}, false
	}
	d := Device{BusID: busID, Path: path}
	d.Bus = uint32(attrUint(dir, "busnum", 10))
	d.Dev = uint32(attrUint(dir, "devnum", 10))
	if d.Bus == 0 || d.Dev == 0 {
		return Device{}, false
	}
	d.Speed = speedEnum(attr(dir, "speed"))
	d.Vendor = uint16(attrUint(dir, "idVendor", 16))
	d.Product = uint16(attrUint(dir, "idProduct", 16))
	d.BCD = uint16(attrUint(dir, "bcdDevice", 16))
	d.Class = uint8(attrUint(dir, "bDeviceClass", 16))
	d.SubClass = uint8(attrUint(dir, "bDeviceSubClass", 16))
	d.Protocol = uint8(attrUint(dir, "bDeviceProtocol", 16))
	d.Config = uint8(attrUint(dir, "bConfigurationValue", 10))
	d.NumConfigs = uint8(attrUint(dir, "bNumConfigurations", 10))
	if l, err := os.Readlink(filepath.Join(dir, "driver")); err == nil {
		d.Driver = filepath.Base(l)
	}
	d.Name = strings.TrimSpace(attr(dir, "manufacturer") + " " + attr(dir, "product"))

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), busID+":") {
			continue
		}
		idir := filepath.Join(dir, e.Name())
		d.Interfaces = append(d.Interfaces, Interface{
			Class:    uint8(attrUint(idir, "bInterfaceClass", 16)),
			SubClass: uint8(attrUint(idir, "bInterfaceSubClass", 16)),
			Protocol: uint8(attrUint(idir, "bInterfaceProtocol", 16)),
		})
		if _, err := os.Stat(filepath.Join(idir, "net")); err == nil {
			d.Net = true
		}
	}
	return d, true
}

// speedEnum turns sysfs's speed in Mbit/s into the kernel's enum
// usb_device_speed, which USB/IP carries.
func speedEnum(s string) uint32 {
	switch s {
	case "1.5":
		return 1
	case "12":
		return 2
	case "480":
		return 3
	case "53.3-480":
		return 4
	case "5000":
		return 5
	case "10000", "20000":
		return 6
	}
	return 0
}

func attr(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func attrUint(dir, name string, base int) uint64 {
	v, _ := strconv.ParseUint(attr(dir, name), base, 32)
	return v
}
