# SimpleClient — Claude Code Implementation Prompt

## Proje Özeti

SimpleClient, USB belleğe yazılıp herhangi bir x86_64 bilgisayarı önyükleyen ~50 MB'lık minimal bir Linux ISO'sudur. Sistem başladığında tek bir statik derlenmiş Go binary'si `/dev/fb0` Linux framebuffer'ına doğrudan piksel yazarak **tam ekran kiosk arayüzü** açar. Kullanıcı bu ekranda ağdaki RDP sunucularını görür, klavye/fare ile seçer, kimlik bilgilerini girer ve uzak masaüstü oturumu aynı ekranda açılır. X11, Wayland, web tarayıcısı, HTTP sunucu yoktur. Sistem kilitlidir: başka yere gidilemez.

---

## Tech Stack

| Katman | Teknoloji | Versiyon |
|--------|-----------|----------|
| Dil | Go | 1.23 |
| Framebuffer | syscall + mmap (stdlib) | — |
| Font | `golang.org/x/image/font/basicfont` | x/image latest |
| Görüntü | `image`, `image/draw`, `image/color` (stdlib) | — |
| Input | Linux evdev syscall (stdlib) | — |
| RDP istemcisi | `github.com/tomatome/grdp` | latest |
| UUID | `github.com/google/uuid` | latest |
| Kernel | Linux | 6.6 LTS |
| Init | BusyBox | 1.36.1 |
| Bootloader | GRUB2 | 2.12 |

**Web sunucu / HTTP / WebSocket / SSE: YOK.**

---

## Proje Yapısı

```
SimpleClient/
├── cmd/simpleclient/main.go
├── internal/
│   ├── domain/
│   │   ├── host.go
│   │   ├── events.go
│   │   └── interfaces.go
│   ├── framebuffer/
│   │   ├── fb.go
│   │   └── fb_info.go
│   ├── inputdev/
│   │   ├── reader.go
│   │   └── keycodes.go
│   ├── ui/
│   │   ├── state.go
│   │   ├── loop.go
│   │   ├── render.go
│   │   ├── render_discovery.go
│   │   ├── render_modal.go
│   │   ├── render_connecting.go
│   │   ├── draw.go
│   │   ├── colors.go
│   │   └── fonts.go
│   ├── scanner/
│   │   ├── scanner.go
│   │   └── cidr.go
│   ├── rdp/
│   │   ├── client.go
│   │   └── framewriter.go
│   ├── network/iface.go
│   └── config/config.go
├── build/
│   ├── Dockerfile
│   ├── Makefile
│   ├── init
│   ├── kernel.config
│   └── grub.cfg
├── go.mod
└── README.md
```

---

## Bağımlılıklar

```bash
go mod init github.com/diggyen/SimpleClient
go get github.com/tomatome/grdp@latest
go get github.com/google/uuid@latest
go get golang.org/x/image@latest
go mod tidy
```

---

## Uygulama Sırası

### Adım 1: Proje İskeleti

Her Go dosyası kendi paket bildirimiyle başlayan boş stub. `go build ./...` hatasız çalışmalı.

`go.mod`:
```
module github.com/diggyen/SimpleClient
go 1.23
```

`.gitignore`:
```
SimpleClient
SimpleClient.iso
build/rootfs/
build/initramfs.cpio.gz
build/iso-root/
```

---

### Adım 2: Domain Tipleri

**Dosyalar:** `internal/domain/host.go`, `internal/domain/events.go`, `internal/domain/interfaces.go`

```go
// internal/domain/host.go
package domain

import "net"

type Host struct {
    IP           net.IP
    Hostname     string
    LatencyMs    int64
}

func (h Host) AddrRDP() string { return h.IP.String() + ":3389" }
func (h Host) DisplayName() string {
    if h.Hostname != "" { return h.Hostname }
    return h.IP.String()
}
```

```go
// internal/domain/events.go
package domain

type ScanEventType string
const (
    EventHostFound    ScanEventType = "host_found"
    EventScanProgress ScanEventType = "scan_progress"
    EventScanComplete ScanEventType = "scan_complete"
)
type ScanEvent struct {
    Type       ScanEventType
    Host       *Host
    Scanned    int
    Total      int
    DurationMs int64
}
```

```go
// internal/domain/interfaces.go
package domain

import "context"

type Scanner interface {
    Start(ctx context.Context, cidrs []string) <-chan ScanEvent
    Cancel()
    Hosts() []Host
}
```

---

### Adım 3: Ağ Arayüzü + CIDR

**Dosyalar:** `internal/network/iface.go`, `internal/scanner/cidr.go`

```go
// internal/network/iface.go
package network

import (
    "fmt"
    "net"
)

func DetectCIDR() (string, error) {
    ifaces, _ := net.Interfaces()
    for _, iface := range ifaces {
        if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
            continue
        }
        addrs, _ := iface.Addrs()
        for _, addr := range addrs {
            if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.To4() != nil {
                return ipNet.String(), nil
            }
        }
    }
    return "", fmt.Errorf("aktif IPv4 arayüzü bulunamadı")
}

func DetectIP() (string, error) {
    cidr, err := DetectCIDR()
    if err != nil { return "", err }
    ip, _, err := net.ParseCIDR(cidr)
    return ip.String(), err
}
```

```go
// internal/scanner/cidr.go
package scanner

import (
    "fmt"
    "net"
)

func ExpandCIDR(cidr string) ([]net.IP, error) {
    _, ipNet, err := net.ParseCIDR(cidr)
    if err != nil { return nil, fmt.Errorf("geçersiz CIDR: %w", err) }

    var ips []net.IP
    for ip := cloneIP(ipNet.IP); ipNet.Contains(ip); incrementIP(ip) {
        if isNetworkOrBroadcast(ip, ipNet) { continue }
        ips = append(ips, cloneIP(ip))
    }
    return ips, nil
}

func cloneIP(ip net.IP) net.IP { c := make(net.IP, len(ip)); copy(c, ip); return c }

func incrementIP(ip net.IP) {
    for i := len(ip) - 1; i >= 0; i-- {
        ip[i]++
        if ip[i] != 0 { break }
    }
}

func isNetworkOrBroadcast(ip net.IP, ipNet *net.IPNet) bool {
    if ip.Equal(ipNet.IP) { return true }
    broadcast := make(net.IP, len(ipNet.IP))
    for i := range ipNet.IP {
        broadcast[i] = ipNet.IP[i] | ^ipNet.Mask[i]
    }
    return ip.Equal(broadcast)
}
```

---

### Adım 4: Framebuffer Sürücüsü

**Dosyalar:** `internal/framebuffer/fb_info.go`, `internal/framebuffer/fb.go`

```go
// internal/framebuffer/fb_info.go
package framebuffer

// Linux fb_var_screeninfo struct (tam 160 byte)
// ioctl FBIOGET_VSCREENINFO için kullanılır
const FBIOGET_VSCREENINFO = 0x4600

type bitfield struct {
    Offset    uint32
    Length    uint32
    MsbRight  uint32
}

type fbVarScreenInfo struct {
    XRes          uint32
    YRes          uint32
    XResVirtual   uint32
    YResVirtual   uint32
    XOffset       uint32
    YOffset       uint32
    BitsPerPixel  uint32
    Grayscale     uint32
    Red           bitfield
    Green         bitfield
    Blue          bitfield
    Transp        bitfield
    Nonstd        uint32
    Activate      uint32
    Height        uint32  // mm cinsinden (ignore)
    Width         uint32  // mm cinsinden (ignore)
    AccelFlags    uint32
    Pixclock      uint32
    LeftMargin    uint32
    RightMargin   uint32
    UpperMargin   uint32
    LowerMargin   uint32
    HsyncLen      uint32
    VsyncLen      uint32
    Sync          uint32
    Vmode         uint32
    Rotate        uint32
    Colorspace    uint32
    Reserved      [4]uint32
}
// unsafe.Sizeof(fbVarScreenInfo{}) == 160 olmalı
```

```go
// internal/framebuffer/fb.go
package framebuffer

import (
    "fmt"
    "image"
    "os"
    "syscall"
    "unsafe"
)

type FB struct {
    file   *os.File
    mem    []byte
    Width  int
    Height int
    Stride int
    BPP    int
}

func Open(path string) (*FB, error) {
    f, err := os.OpenFile(path, os.O_RDWR, 0)
    if err != nil {
        return nil, fmt.Errorf("framebuffer açılamadı %q: %w", path, err)
    }

    var info fbVarScreenInfo
    if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
        f.Fd(), FBIOGET_VSCREENINFO, uintptr(unsafe.Pointer(&info))); errno != 0 {
        f.Close()
        return nil, fmt.Errorf("FBIOGET_VSCREENINFO: %v", errno)
    }

    stride := int(info.XRes) * int(info.BitsPerPixel) / 8
    size := stride * int(info.YRes)

    mem, err := syscall.Mmap(int(f.Fd()), 0, size,
        syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
    if err != nil {
        f.Close()
        return nil, fmt.Errorf("mmap: %w", err)
    }

    return &FB{
        file:   f,
        mem:    mem,
        Width:  int(info.XRes),
        Height: int(info.YRes),
        Stride: stride,
        BPP:    int(info.BitsPerPixel),
    }, nil
}

// Blit: RGBA image'ı framebuffer'a yazar.
// Çoğu Linux framebuffer BGRA (little-endian) formatındadır.
func (fb *FB) Blit(img *image.RGBA) {
    bytesPerPix := fb.BPP / 8
    for y := 0; y < fb.Height && y < img.Bounds().Dy(); y++ {
        for x := 0; x < fb.Width && x < img.Bounds().Dx(); x++ {
            srcOff := y*img.Stride + x*4
            dstOff := y*fb.Stride + x*bytesPerPix
            r := img.Pix[srcOff+0]
            g := img.Pix[srcOff+1]
            b := img.Pix[srcOff+2]
            if bytesPerPix >= 3 {
                fb.mem[dstOff+0] = b // B
                fb.mem[dstOff+1] = g // G
                fb.mem[dstOff+2] = r // R
            }
            if bytesPerPix == 4 {
                fb.mem[dstOff+3] = 0xFF // A (padding)
            }
        }
    }
}

func (fb *FB) Bounds() image.Rectangle {
    return image.Rect(0, 0, fb.Width, fb.Height)
}

func (fb *FB) Close() error {
    syscall.Munmap(fb.mem)
    return fb.file.Close()
}
```

**🔍 Checkpoint:** `go build ./internal/framebuffer/...` hatasız. `unsafe.Sizeof(fbVarScreenInfo{})` == 160.

---

### Adım 5: evdev Input Sürücüsü

**Dosyalar:** `internal/inputdev/keycodes.go`, `internal/inputdev/reader.go`

```go
// internal/inputdev/keycodes.go
package inputdev

// Linux klavye → rune eşlemesi (shift durumuna göre)
// İlk eleman normal, ikinci eleman shift basılıyken
var keycodeMap = map[int][2]rune{
    2:  {'1', '!'}, 3:  {'2', '@'}, 4:  {'3', '#'}, 5:  {'4', '$'},
    6:  {'5', '%'}, 7:  {'6', '^'}, 8:  {'7', '&'}, 9:  {'8', '*'},
    10: {'9', '('}, 11: {'0', ')'},
    16: {'q', 'Q'}, 17: {'w', 'W'}, 18: {'e', 'E'}, 19: {'r', 'R'},
    20: {'t', 'T'}, 21: {'y', 'Y'}, 22: {'u', 'U'}, 23: {'i', 'I'},
    24: {'o', 'O'}, 25: {'p', 'P'},
    30: {'a', 'A'}, 31: {'s', 'S'}, 32: {'d', 'D'}, 33: {'f', 'F'},
    34: {'g', 'G'}, 35: {'h', 'H'}, 36: {'j', 'J'}, 37: {'k', 'K'},
    38: {'l', 'L'},
    44: {'z', 'Z'}, 45: {'x', 'X'}, 46: {'c', 'C'}, 47: {'v', 'V'},
    48: {'b', 'B'}, 49: {'n', 'N'}, 50: {'m', 'M'},
    51: {',', '<'}, 52: {'.', '>'}, 53: {'/', '?'},
    57: {' ', ' '}, // Space
    12: {'-', '_'}, 13: {'=', '+'}, 26: {'[', '{'}, 27: {']', '}'},
    39: {';', ':'}, 40: {'\'', '"'}, 43: {'\\', '|'},
}

// Özel tuş sabitleri (Linux keycode)
const (
    KeyEnter     = 28
    KeyEsc       = 1
    KeyBackspace = 14
    KeyTab       = 15
    KeyUp        = 103
    KeyDown      = 108
    KeyLeft      = 105
    KeyRight     = 106
    KeyPageUp    = 104
    KeyPageDown  = 109
    KeyF5        = 63
    KeyLeftCtrl  = 29
    KeyRightCtrl = 97
    KeyLeftAlt   = 56
    KeyRightAlt  = 100
    KeyLeftShift = 42
    KeyRightShift= 54
    KeyEnd       = 107
    KeyDelete    = 111
    // Mouse butonları (BTN_LEFT=272, BTN_RIGHT=273)
    BtnLeft      = 272
    BtnRight     = 273
)
```

```go
// internal/inputdev/reader.go
package inputdev

import (
    "encoding/binary"
    "fmt"
    "os"
    "strings"
    "sync/atomic"
)

// Linux evdev input_event (24 bytes)
type linuxInputEvent struct {
    TVSec    uint32
    TVUSec   uint32
    _        uint32 // padding (64-bit timeval)
    _        uint32
    Type     uint16
    Code     uint16
    Value    int32
}

const (
    evSyn = 0x00
    evKey = 0x01
    evRel = 0x02
    relX  = 0
    relY  = 1
)

type EventType int
const (
    EvKey         EventType = iota
    EvMouseMove
    EvMouseButton
)

type InputEvent struct {
    Type    EventType
    KeyCode int    // Linux keycode (EvKey için)
    Rune    rune   // Baskı yapılabilir karakter (0 = özel tuş)
    MouseX  int    // Mutlak fare pozisyonu
    MouseY  int
    Button  int    // 1=sol, 2=sağ (EvMouseButton için)
    Pressed bool
}

type Reader struct {
    kbdFile   *os.File
    mouseFile *os.File
    ch        chan InputEvent
    mouseX    int64 // atomic
    mouseY    int64
    screenW   int
    screenH   int
    shiftDown bool
    ctrlDown  bool
    altDown   bool
}

func DetectKeyboard() (string, error) {
    return detectDevice("keyboard", "EV=120013")
}

func DetectMouse() (string, error) {
    return detectDevice("mouse", "EV=17")
}

// detectDevice: /proc/bus/input/devices parse ederek cihaz dosyası bulur
func detectDevice(hint, evHint string) (string, error) {
    data, err := os.ReadFile("/proc/bus/input/devices")
    if err != nil {
        // Fallback: ilk event cihazı dene
        for i := 0; i <= 5; i++ {
            path := fmt.Sprintf("/dev/input/event%d", i)
            if _, err := os.Stat(path); err == nil {
                return path, nil
            }
        }
        return "", fmt.Errorf("input cihazı bulunamadı")
    }

    lines := strings.Split(string(data), "\n")
    var currentHandlers string
    var currentEV string

    for _, line := range lines {
        line = strings.TrimSpace(line)
        if strings.HasPrefix(line, "H: Handlers=") {
            currentHandlers = line
        }
        if strings.HasPrefix(line, "B: EV=") {
            currentEV = strings.ToLower(line)
        }
        if line == "" && currentHandlers != "" {
            // Klavye: EV bitleri arasında KEY (0x01) ve EV_KEY tip event'ları
            // Fare: REL event var
            matched := false
            if hint == "keyboard" && strings.Contains(currentHandlers, "kbd") {
                matched = true
            } else if hint == "mouse" && strings.Contains(currentHandlers, "mouse") {
                matched = true
            } else if evHint != "" && strings.Contains(currentEV, strings.ToLower(evHint)) {
                matched = true
            }
            if matched {
                for _, h := range strings.Fields(currentHandlers[len("H: Handlers="):]) {
                    if strings.HasPrefix(h, "event") {
                        return "/dev/input/" + h, nil
                    }
                }
            }
            currentHandlers = ""
            currentEV = ""
        }
    }
    return "", fmt.Errorf("%s cihazı bulunamadı", hint)
}

func New(kbdPath, mousePath string, screenW, screenH int) (*Reader, error) {
    r := &Reader{
        ch:      make(chan InputEvent, 64),
        screenW: screenW,
        screenH: screenH,
    }
    atomic.StoreInt64(&r.mouseX, int64(screenW/2))
    atomic.StoreInt64(&r.mouseY, int64(screenH/2))

    if kbdPath != "" {
        f, err := os.Open(kbdPath)
        if err != nil { return nil, fmt.Errorf("klavye: %w", err) }
        r.kbdFile = f
        go r.readKeyboard()
    }
    if mousePath != "" {
        f, err := os.Open(mousePath)
        if err != nil {
            if r.kbdFile != nil { r.kbdFile.Close() }
            return nil, fmt.Errorf("fare: %w", err)
        }
        r.mouseFile = f
        go r.readMouse()
    }
    return r, nil
}

func (r *Reader) readKeyboard() {
    var ev linuxInputEvent
    for {
        if err := binary.Read(r.kbdFile, binary.LittleEndian, &ev); err != nil { return }
        if ev.Type != evKey { continue }

        code := int(ev.Code)
        pressed := ev.Value != 0  // 1=pressed, 2=repeat, 0=released

        // Modifier takibi
        switch code {
        case KeyLeftShift, KeyRightShift: r.shiftDown = pressed
        case KeyLeftCtrl, KeyRightCtrl:  r.ctrlDown = pressed
        case KeyLeftAlt, KeyRightAlt:    r.altDown = pressed
        }

        ie := InputEvent{
            Type:    EvKey,
            KeyCode: code,
            Pressed: pressed,
        }

        // Baskı yapılabilir karakter
        if pair, ok := keycodeMap[code]; ok {
            if r.shiftDown {
                ie.Rune = pair[1]
            } else {
                ie.Rune = pair[0]
            }
        }

        r.ch <- ie
    }
}

func (r *Reader) readMouse() {
    var ev linuxInputEvent
    for {
        if err := binary.Read(r.mouseFile, binary.LittleEndian, &ev); err != nil { return }
        mx := int(atomic.LoadInt64(&r.mouseX))
        my := int(atomic.LoadInt64(&r.mouseY))

        switch ev.Type {
        case evRel:
            if ev.Code == relX {
                mx = clamp(mx+int(ev.Value), 0, r.screenW-1)
                atomic.StoreInt64(&r.mouseX, int64(mx))
            } else if ev.Code == relY {
                my = clamp(my+int(ev.Value), 0, r.screenH-1)
                atomic.StoreInt64(&r.mouseY, int64(my))
            }
            r.ch <- InputEvent{Type: EvMouseMove, MouseX: mx, MouseY: my}

        case evKey:
            if ev.Code == BtnLeft || ev.Code == BtnRight {
                btn := 1
                if ev.Code == BtnRight { btn = 2 }
                r.ch <- InputEvent{
                    Type:    EvMouseButton,
                    Button:  btn,
                    Pressed: ev.Value != 0,
                    MouseX:  mx,
                    MouseY:  my,
                }
            }
        }
    }
}

func (r *Reader) Events() <-chan InputEvent { return r.ch }

func (r *Reader) MousePos() (int, int) {
    return int(atomic.LoadInt64(&r.mouseX)), int(atomic.LoadInt64(&r.mouseY))
}

func (r *Reader) Close() {
    if r.kbdFile != nil { r.kbdFile.Close() }
    if r.mouseFile != nil { r.mouseFile.Close() }
}

func clamp(v, min, max int) int {
    if v < min { return min }
    if v > max { return max }
    return v
}
```

---

### Adım 6: Ağ Tarayıcı

**Dosyalar:** `internal/scanner/scanner.go`

```go
// internal/scanner/scanner.go
package scanner

import (
    "context"
    "net"
    "sync"
    "sync/atomic"
    "time"

    "github.com/diggyen/SimpleClient/internal/domain"
    "github.com/diggyen/SimpleClient/internal/network"
)

type NetworkScanner struct {
    concurrency int
    timeout     time.Duration
    mu          sync.RWMutex
    hostMap     map[string]domain.Host
    cancelMu    sync.Mutex
    cancel      context.CancelFunc
}

func New(concurrency int, timeout time.Duration) *NetworkScanner {
    return &NetworkScanner{
        concurrency: concurrency,
        timeout:     timeout,
        hostMap:     make(map[string]domain.Host),
    }
}

func (s *NetworkScanner) Start(ctx context.Context, cidrs []string) <-chan domain.ScanEvent {
    s.Cancel()

    var allIPs []net.IP
    for _, cidr := range cidrs {
        ips, err := ExpandCIDR(cidr)
        if err != nil { continue }
        allIPs = append(allIPs, ips...)
    }

    scanCtx, cancel := context.WithCancel(ctx)
    s.cancelMu.Lock()
    s.cancel = cancel
    s.cancelMu.Unlock()

    // Yeni taramada listeyi temizle
    s.mu.Lock()
    s.hostMap = make(map[string]domain.Host)
    s.mu.Unlock()

    events := make(chan domain.ScanEvent, 64)
    total := len(allIPs)
    var scanned int64

    go func() {
        defer close(events)
        defer cancel()

        sem := make(chan struct{}, s.concurrency)
        var wg sync.WaitGroup
        start := time.Now()

        for _, ip := range allIPs {
            select {
            case <-scanCtx.Done():
                wg.Wait()
                return
            case sem <- struct{}{}:
            }

            wg.Add(1)
            go func(target net.IP) {
                defer wg.Done()
                defer func() { <-sem }()

                n := int(atomic.AddInt64(&scanned, 1))
                dialStart := time.Now()
                conn, err := net.DialTimeout("tcp", target.String()+":3389", s.timeout)
                if err == nil {
                    conn.Close()
                    latency := time.Since(dialStart).Milliseconds()
                    hostname := reverseDNS(scanCtx, target)

                    host := domain.Host{
                        IP:        cloneIP(target),
                        Hostname:  hostname,
                        LatencyMs: latency,
                    }
                    s.mu.Lock()
                    s.hostMap[target.String()] = host
                    s.mu.Unlock()

                    select {
                    case events <- domain.ScanEvent{Type: domain.EventHostFound, Host: &host}:
                    case <-scanCtx.Done():
                        return
                    }
                }

                if n%10 == 0 || n == total {
                    select {
                    case events <- domain.ScanEvent{
                        Type:    domain.EventScanProgress,
                        Scanned: n,
                        Total:   total,
                    }:
                    default:
                    }
                }
            }(ip)
        }
        wg.Wait()

        events <- domain.ScanEvent{
            Type:       domain.EventScanComplete,
            Scanned:    total,
            Total:      total,
            DurationMs: time.Since(start).Milliseconds(),
        }
    }()

    return events
}

func (s *NetworkScanner) Cancel() {
    s.cancelMu.Lock()
    defer s.cancelMu.Unlock()
    if s.cancel != nil { s.cancel(); s.cancel = nil }
}

func (s *NetworkScanner) Hosts() []domain.Host {
    s.mu.RLock()
    defer s.mu.RUnlock()
    result := make([]domain.Host, 0, len(s.hostMap))
    for _, h := range s.hostMap { result = append(result, h) }
    return result
}

func reverseDNS(ctx context.Context, ip net.IP) string {
    ctx2, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
    defer cancel()
    names, err := net.DefaultResolver.LookupAddr(ctx2, ip.String())
    if err != nil || len(names) == 0 { return "" }
    return strings.TrimSuffix(names[0], ".")
}

func cloneIP(ip net.IP) net.IP { c := make(net.IP, len(ip)); copy(c, ip); return c }

// strings import için
import "strings"
```

---

### Adım 7: Draw Primitifleri

**Dosyalar:** `internal/ui/colors.go`, `internal/ui/draw.go`

```go
// internal/ui/colors.go
package ui

import "image/color"

var (
    ColorBG        = color.RGBA{26, 26, 46, 255}
    ColorPanel     = color.RGBA{22, 33, 62, 255}
    ColorAccent    = color.RGBA{233, 69, 96, 255}
    ColorText      = color.RGBA{224, 224, 224, 255}
    ColorSubText   = color.RGBA{136, 136, 136, 255}
    ColorHighlight = color.RGBA{15, 52, 96, 255}
    ColorSelected  = color.RGBA{35, 72, 116, 255}
    ColorGreen     = color.RGBA{100, 221, 100, 255}
    ColorError     = color.RGBA{255, 80, 80, 255}
    ColorBorder    = color.RGBA{30, 40, 70, 255}
    ColorCursor    = color.RGBA{255, 255, 100, 255}
    ColorInputBG   = color.RGBA{10, 15, 30, 255}
    ColorInputActive = color.RGBA{30, 60, 100, 255}
)
```

```go
// internal/ui/draw.go
package ui

import (
    "fmt"
    "image"
    "image/color"
    "image/draw"

    "golang.org/x/image/font"
    "golang.org/x/image/font/basicfont"
    "golang.org/x/image/math/fixed"
)

func FillRect(img *image.RGBA, r image.Rectangle, c color.RGBA) {
    draw.Draw(img, r, &image.Uniform{C: c}, image.Point{}, draw.Src)
}

func DrawBorder(img *image.RGBA, r image.Rectangle, c color.RGBA) {
    // 1 piksel çerçeve
    FillRect(img, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+1), c)
    FillRect(img, image.Rect(r.Min.X, r.Max.Y-1, r.Max.X, r.Max.Y), c)
    FillRect(img, image.Rect(r.Min.X, r.Min.Y, r.Min.X+1, r.Max.Y), c)
    FillRect(img, image.Rect(r.Max.X-1, r.Min.Y, r.Max.X, r.Max.Y), c)
}

func DrawText(img *image.RGBA, x, y int, text string, c color.RGBA) {
    d := &font.Drawer{
        Dst:  img,
        Src:  &image.Uniform{C: c},
        Face: basicfont.Face7x13,
        Dot:  fixed.P(x, y+13), // y: baseline
    }
    d.DrawString(text)
}

// DrawTextLarge: basicfont'u 2x büyüterek çizer (başlık için)
func DrawTextLarge(img *image.RGBA, x, y int, text string, c color.RGBA) {
    // Her karakteri 2x2 piksel bloklarla render et
    tmp := image.NewRGBA(image.Rect(0, 0, len(text)*7, 13))
    DrawText(tmp, 0, 0, text, c)
    for py := 0; py < 13; py++ {
        for px := 0; px < tmp.Bounds().Dx(); px++ {
            src := tmp.RGBAAt(px, py)
            if src.A > 0 {
                img.SetRGBA(x+px*2,   y+py*2,   src)
                img.SetRGBA(x+px*2+1, y+py*2,   src)
                img.SetRGBA(x+px*2,   y+py*2+1, src)
                img.SetRGBA(x+px*2+1, y+py*2+1, src)
            }
        }
    }
}

func DrawProgressBar(img *image.RGBA, r image.Rectangle, pct float64, fg, bg color.RGBA) {
    FillRect(img, r, bg)
    if pct > 0 {
        filled := int(float64(r.Dx()) * pct)
        FillRect(img, image.Rect(r.Min.X, r.Min.Y, r.Min.X+filled, r.Max.Y), fg)
    }
}

// DrawInputField: metin girişi alanı (active=true ise vurgulanmış kenarlık)
func DrawInputField(img *image.RGBA, r image.Rectangle, text string, active bool, masked bool) {
    if active {
        FillRect(img, r, ColorInputActive)
        DrawBorder(img, r, ColorAccent)
    } else {
        FillRect(img, r, ColorInputBG)
        DrawBorder(img, r, ColorBorder)
    }
    displayText := text
    if masked {
        displayText = ""
        for range text { displayText += "●" }
    }
    if active { displayText += "_" }
    DrawText(img, r.Min.X+8, r.Min.Y+4, displayText, ColorText)
}

// DrawCursor: fare imleci (küçük ok)
func DrawCursor(img *image.RGBA, x, y int) {
    // 12x12 ok şekli
    for i := 0; i < 12; i++ {
        if x+i < img.Bounds().Dx() && y < img.Bounds().Dy() {
            img.SetRGBA(x, y+i, ColorCursor)
        }
        if i < 12-i && x+i < img.Bounds().Dx() && y+i < img.Bounds().Dy() {
            img.SetRGBA(x+i, y+i, ColorCursor)
        }
    }
}

func TextWidth(text string) int { return len(text) * 7 }

func Sprintf(format string, a ...interface{}) string { return fmt.Sprintf(format, a...) }
```

---

### Adım 8: UI State Machine

**Dosyalar:** `internal/ui/state.go`

```go
// internal/ui/state.go
package ui

import "github.com/diggyen/SimpleClient/internal/domain"

type Screen int
const (
    ScreenDiscovery  Screen = iota
    ScreenModal
    ScreenConnecting
    ScreenSession
)

const rowHeight = 44  // piksel cinsinden her sunucu satırı
const topBarH   = 48
const botBarH   = 52

type ModalState struct {
    Fields   [3]string  // 0=username, 1=password, 2=domain
    FocusIdx int        // 0-2=alanlar, 3=bağlan, 4=iptal
    Error    string
}

type SessionState struct {
    Target    domain.Host
    Connected bool
}

type UIState struct {
    Screen       Screen
    Hosts        []domain.Host
    SelectedIdx  int
    ScrollOffset int
    ScanProgress float64
    ScanDone     bool
    ScanFound    int
    ScanTotal    int
    Modal        ModalState
    Session      *SessionState
    ErrorMsg     string
    LocalIP      string
}

func NewUIState(localIP string) *UIState {
    return &UIState{
        Screen:  ScreenDiscovery,
        LocalIP: localIP,
    }
}

func (s *UIState) Transition(to Screen) { s.Screen = to }

func (s *UIState) SelectedHost() *domain.Host {
    if len(s.Hosts) == 0 || s.SelectedIdx >= len(s.Hosts) { return nil }
    return &s.Hosts[s.SelectedIdx]
}

func (s *UIState) HandleScanEvent(ev domain.ScanEvent) {
    switch ev.Type {
    case domain.EventHostFound:
        if ev.Host != nil { s.Hosts = append(s.Hosts, *ev.Host) }
    case domain.EventScanProgress:
        if ev.Total > 0 {
            s.ScanProgress = float64(ev.Scanned) / float64(ev.Total)
            s.ScanFound = ev.Scanned
            s.ScanTotal = ev.Total
        }
    case domain.EventScanComplete:
        s.ScanProgress = 1.0
        s.ScanDone = true
        s.ScanFound = len(s.Hosts)
    }
}

func (s *UIState) MoveSelection(delta int) {
    if len(s.Hosts) == 0 { return }
    s.SelectedIdx = clampInt(s.SelectedIdx+delta, 0, len(s.Hosts)-1)
}

func (s *UIState) VisibleRowCount(screenH int) int {
    available := screenH - topBarH - botBarH
    return available / rowHeight
}

// ModalAddChar: modal'daki aktif alana karakter ekle
func (s *UIState) ModalAddChar(r rune) {
    if s.Modal.FocusIdx < 3 {
        s.Modal.Fields[s.Modal.FocusIdx] += string(r)
    }
}

// ModalBackspace: aktif alandan son karakteri sil
func (s *UIState) ModalBackspace() {
    if s.Modal.FocusIdx < 3 && len(s.Modal.Fields[s.Modal.FocusIdx]) > 0 {
        f := s.Modal.Fields[s.Modal.FocusIdx]
        s.Modal.Fields[s.Modal.FocusIdx] = f[:len(f)-1]
    }
}

func clampInt(v, min, max int) int {
    if v < min { return min }
    if v > max { return max }
    return v
}
```

---

### Adım 9: Render Fonksiyonları

**Dosyalar:** `internal/ui/render.go`, `internal/ui/render_discovery.go`, `internal/ui/render_modal.go`, `internal/ui/render_connecting.go`

```go
// internal/ui/render.go
package ui

import (
    "image"
    "github.com/diggyen/SimpleClient/internal/framebuffer"
)

type FBInterface interface {
    Blit(img *image.RGBA)
    Bounds() image.Rectangle
}

func RenderFrame(fb FBInterface, state *UIState, mouseX, mouseY int) {
    bounds := fb.Bounds()
    img := image.NewRGBA(bounds)

    switch state.Screen {
    case ScreenDiscovery:
        renderDiscovery(img, state, mouseX, mouseY)
    case ScreenModal:
        renderDiscovery(img, state, mouseX, mouseY)
        renderModal(img, state, bounds)
    case ScreenConnecting:
        renderDiscovery(img, state, mouseX, mouseY)
        renderConnecting(img, state, bounds)
    case ScreenSession:
        // Session ekranında UI render atlanır; RDP FrameWriter direkt fb.Blit() yapar
        return
    }

    // Fare imleci
    DrawCursor(img, mouseX, mouseY)
    fb.Blit(img)
}
```

```go
// internal/ui/render_discovery.go
package ui

import (
    "fmt"
    "image"
    "image/color"
)

func renderDiscovery(img *image.RGBA, state *UIState, mouseX, mouseY int) {
    w := img.Bounds().Dx()
    h := img.Bounds().Dy()

    // Arka plan
    FillRect(img, img.Bounds(), ColorBG)

    // ── Üst Bar ──────────────────────────────────────────────────────
    topBar := image.Rect(0, 0, w, topBarH)
    FillRect(img, topBar, ColorPanel)
    DrawBorder(img, topBar, ColorBorder)

    // Logo
    DrawTextLarge(img, 16, 8, "SimpleClient", ColorAccent)

    // IP adresi
    ipText := "IP: " + state.LocalIP
    DrawText(img, 200, 18, ipText, ColorSubText)

    // Tarama durumu
    var statusText string
    if state.ScanDone {
        statusText = fmt.Sprintf("Tamamlandı — %d sunucu bulundu", len(state.Hosts))
    } else if state.ScanTotal > 0 {
        pct := int(state.ScanProgress * 100)
        statusText = fmt.Sprintf("Tarıyor... %d/%d (%d%%)", state.ScanFound, state.ScanTotal, pct)
    } else {
        statusText = "Başlatılıyor..."
    }
    sw := TextWidth(statusText)
    DrawText(img, w-sw-16, 18, statusText, ColorSubText)

    // ── Sunucu Listesi ───────────────────────────────────────────────
    listTop := topBarH + 8
    listBot := h - botBarH
    visibleRows := (listBot - listTop) / rowHeight

    if len(state.Hosts) == 0 {
        msg := "Sunucu bulunamadı"
        if !state.ScanDone { msg = "Ağ taranıyor..." }
        DrawText(img, w/2-TextWidth(msg)/2, h/2, msg, ColorSubText)
    } else {
        for i := 0; i < visibleRows; i++ {
            idx := i + state.ScrollOffset
            if idx >= len(state.Hosts) { break }

            host := state.Hosts[idx]
            rowY := listTop + i*rowHeight
            rowRect := image.Rect(8, rowY, w-8, rowY+rowHeight-2)

            // Seçili satır vurgusu
            if idx == state.SelectedIdx {
                FillRect(img, rowRect, ColorSelected)
                DrawBorder(img, rowRect, ColorAccent)
                DrawText(img, 16, rowY+14, "▶", ColorAccent)
            } else {
                // Fare hover
                if mouseY >= rowY && mouseY < rowY+rowHeight {
                    FillRect(img, rowRect, ColorHighlight)
                }
            }

            // IP
            DrawText(img, 40, rowY+14, host.IP.String(), ColorText)
            // Hostname
            hn := host.DisplayName()
            if host.Hostname == "" { hn = "[bilinmiyor]" }
            DrawText(img, 200, rowY+14, hn, ColorSubText)
            // Gecikme
            latStr := fmt.Sprintf("%d ms", host.LatencyMs)
            lw := TextWidth(latStr)
            DrawText(img, w-lw-24, rowY+14, latStr, ColorGreen)

            // Satır ayırıcı
            FillRect(img, image.Rect(8, rowY+rowHeight-1, w-8, rowY+rowHeight), ColorBorder)
        }

        // Kaydırma göstergesi
        if len(state.Hosts) > visibleRows {
            scrollBarH := listBot - listTop
            thumbH := scrollBarH * visibleRows / len(state.Hosts)
            thumbY := listTop + scrollBarH*state.ScrollOffset/len(state.Hosts)
            FillRect(img, image.Rect(w-6, listTop, w-2, listBot), ColorBorder)
            FillRect(img, image.Rect(w-6, thumbY, w-2, thumbY+thumbH), ColorAccent)
        }
    }

    // ── Alt Bar ──────────────────────────────────────────────────────
    botBar := image.Rect(0, h-botBarH, w, h)
    FillRect(img, botBar, ColorPanel)
    DrawBorder(img, botBar, ColorBorder)

    hints := "↑↓ Gezin    ENTER Bağlan    F5 Yeniden Tara"
    DrawText(img, 16, h-botBarH+10, hints, ColorSubText)

    // İlerleme çubuğu
    progressRect := image.Rect(0, h-4, w, h)
    DrawProgressBar(img, progressRect, state.ScanProgress, ColorAccent, ColorBorder)
}
```

```go
// internal/ui/render_modal.go
package ui

import (
    "image"
    "image/color"
    "image/draw"
)

func renderModal(img *image.RGBA, state *UIState, bounds image.Rectangle) {
    w, h := bounds.Dx(), bounds.Dy()

    // Yarı-saydam overlay
    overlay := image.NewUniform(color.RGBA{0, 0, 0, 160})
    draw.Draw(img, bounds, overlay, image.Point{}, draw.Over)

    // Modal boyutları
    mw, mh := 400, 320
    mx := (w - mw) / 2
    my := (h - mh) / 2
    modal := image.Rect(mx, my, mx+mw, my+mh)

    FillRect(img, modal, ColorPanel)
    DrawBorder(img, modal, ColorAccent)

    host := state.SelectedHost()
    if host == nil { return }

    // Başlık
    title := host.IP.String()
    if host.Hostname != "" { title = host.Hostname + " (" + host.IP.String() + ")" }
    DrawText(img, mx+16, my+14, title, ColorText)
    FillRect(img, image.Rect(mx+8, my+36, mx+mw-8, my+37), ColorBorder)

    // Input alanları
    labels := []string{"Kullanıcı Adı", "Şifre", "Domain (isteğe bağlı)"}
    masked := []bool{false, true, false}
    fieldH := 36
    for i, label := range labels {
        fy := my + 48 + i*70
        DrawText(img, mx+16, fy, label, ColorSubText)
        fieldRect := image.Rect(mx+16, fy+18, mx+mw-16, fy+18+fieldH)
        DrawInputField(img, fieldRect, state.Modal.Fields[i], state.Modal.FocusIdx == i, masked[i])
    }

    // Butonlar
    btnY := my + mh - 56
    connectBtn := image.Rect(mx+16, btnY, mx+mw/2-8, btnY+36)
    cancelBtn := image.Rect(mx+mw/2+8, btnY, mx+mw-16, btnY+36)

    connectBG := ColorAccent
    if state.Modal.FocusIdx == 3 { connectBG = color.RGBA{180, 40, 60, 255} }
    FillRect(img, connectBtn, connectBG)
    DrawBorder(img, connectBtn, ColorText)
    DrawText(img, connectBtn.Min.X+connectBtn.Dx()/2-TextWidth("Bağlan")/2, connectBtn.Min.Y+10, "Bağlan", ColorText)

    FillRect(img, cancelBtn, ColorHighlight)
    DrawBorder(img, cancelBtn, ColorBorder)
    DrawText(img, cancelBtn.Min.X+cancelBtn.Dx()/2-TextWidth("İptal")/2, cancelBtn.Min.Y+10, "İptal", ColorText)

    // Hata mesajı
    if state.Modal.Error != "" {
        DrawText(img, mx+16, btnY+44, state.Modal.Error, ColorError)
    }
}
```

```go
// internal/ui/render_connecting.go
package ui

import (
    "fmt"
    "image"
    "image/color"
    "image/draw"
    "time"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func renderConnecting(img *image.RGBA, state *UIState, bounds image.Rectangle) {
    draw.Draw(img, bounds, &image.Uniform{C: color.RGBA{0, 0, 0, 128}}, image.Point{}, draw.Over)

    host := state.SelectedHost()
    if host == nil { return }

    w, h := bounds.Dx(), bounds.Dy()
    spinnerIdx := int(time.Now().UnixMilli()/100) % len(spinnerFrames)
    spinner := spinnerFrames[spinnerIdx]
    msg := fmt.Sprintf("%s Bağlanıyor... %s", spinner, host.IP.String())
    DrawText(img, w/2-TextWidth(msg)/2, h/2, msg, ColorText)
}
```

---

### Adım 10: RDP İstemcisi ve FrameWriter

**Dosyalar:** `internal/rdp/client.go`, `internal/rdp/framewriter.go`

```go
// internal/rdp/client.go
package rdp

import (
    "fmt"
    "image"
    "time"

    "github.com/tomatome/grdp/protocol/pdu"
    "github.com/tomatome/grdp/rdp"
)

type Credentials struct {
    Username string
    Password string
    Domain   string
}

type Client struct {
    g      *rdp.Client
    frames chan image.Image
}

func New(addr string, creds Credentials, width, height int) (*Client, error) {
    c := &Client{frames: make(chan image.Image, 4)}

    var err error
    c.g, err = rdp.NewClient(addr, &rdp.ClientInfo{
        Width:    width,
        Height:   height,
        UserName: creds.Username,
        Password: creds.Password,
        Domain:   creds.Domain,
    })
    if err != nil {
        return nil, fmt.Errorf("RDP client oluşturulamadı: %w", err)
    }

    c.g.On("bitmap", func(rect pdu.BitmapData) {
        img := bitmapToImage(rect)
        if img == nil { return }
        select {
        case c.frames <- img:
        default:
            // Eski frame at, yenisini al
            select { case <-c.frames: default: }
            c.frames <- img
        }
    })

    done := make(chan error, 1)
    go func() { done <- c.g.Login() }()

    select {
    case err := <-done:
        if err != nil {
            return nil, fmt.Errorf("RDP bağlantısı başarısız: %w", err)
        }
    case <-time.After(10 * time.Second):
        c.g.Close()
        return nil, fmt.Errorf("RDP bağlantısı zaman aşımı (10s)")
    }

    return c, nil
}

func (c *Client) Frames() <-chan image.Image { return c.frames }

// SendKey: Linux keycode → RDP scan code + flag
func (c *Client) SendKey(linuxKeycode int, down bool) error {
    sc := linuxToRDPScanCode(linuxKeycode)
    if sc == 0 { return nil }
    flags := uint16(0)
    if !down { flags = pdu.KBDFLAGS_RELEASE }
    return c.g.SendKeyEvent(&pdu.KeyboardEvent{
        KeyboardFlags: flags,
        KeyCode:       uint8(sc),
    })
}

func (c *Client) SendMouse(x, y int, btns uint16) error {
    return c.g.SendPointerEvent(&pdu.PointerEvent{
        PointerFlags: btns,
        XPos:         uint16(x),
        YPos:         uint16(y),
    })
}

func (c *Client) Close() error { return c.g.Close() }

// bitmapToImage: grdp BitmapData → image.RGBA
func bitmapToImage(b pdu.BitmapData) image.Image {
    // grdp'nin kendi decompress metodunu kullan
    // Not: grdp API'si sürüme göre değişebilir; gerekirse grdp source'u incele
    data := b.BitmapDataStream
    if data == nil { return nil }
    rect := image.Rect(int(b.DestLeft), int(b.DestTop), int(b.DestRight+1), int(b.DestBottom+1))
    img := image.NewRGBA(rect)
    // grdp bitmap'i BGR formatında verir; RGBA'ya çevir
    for i := 0; i+2 < len(data); i += 3 {
        x := rect.Min.X + (i/3)%rect.Dx()
        y := rect.Min.Y + (i/3)/rect.Dx()
        img.SetRGBA(x, y, color.RGBA{data[i+2], data[i+1], data[i], 255})
    }
    return img
}

// linuxToRDPScanCode: evdev keycode → RDP PC/AT scan code
func linuxToRDPScanCode(code int) int {
    table := map[int]int{
        1: 0x01, // ESC
        28: 0x1C, // ENTER
        14: 0x0E, // BACKSPACE
        15: 0x0F, // TAB
        57: 0x39, // SPACE
        // Alfanümerik (Linux → AT scan code 1:1 çoğunlukla)
        16: 0x10, 17: 0x11, 18: 0x12, 19: 0x13, 20: 0x14,
        21: 0x15, 22: 0x16, 23: 0x17, 24: 0x18, 25: 0x19,
        30: 0x1E, 31: 0x1F, 32: 0x20, 33: 0x21, 34: 0x22,
        35: 0x23, 36: 0x24, 37: 0x25, 38: 0x26,
        44: 0x2C, 45: 0x2D, 46: 0x2E, 47: 0x2F, 48: 0x30,
        49: 0x31, 50: 0x32,
        // Rakamlar
        2: 0x02, 3: 0x03, 4: 0x04, 5: 0x05, 6: 0x06,
        7: 0x07, 8: 0x08, 9: 0x09, 10: 0x0A, 11: 0x0B,
        // Ok tuşları
        103: 0x48, 108: 0x50, 105: 0x4B, 106: 0x4D,
        // Shift/Ctrl/Alt
        42: 0x2A, 54: 0x36, 29: 0x1D, 56: 0x38,
        // F tuşları
        59: 0x3B, 60: 0x3C, 61: 0x3D, 62: 0x3E, 63: 0x3F,
    }
    if sc, ok := table[code]; ok { return sc }
    return 0
}
```

```go
// internal/rdp/framewriter.go
package rdp

import (
    "image"
    "image/draw"

    xdraw "golang.org/x/image/draw"
)

type FBInterface interface {
    Blit(img *image.RGBA)
    Bounds() image.Rectangle
}

type FrameWriter struct {
    FB FBInterface
}

func (fw *FrameWriter) Write(src image.Image) {
    if src == nil { return }
    bounds := fw.FB.Bounds()
    dst := image.NewRGBA(bounds)

    // Ölçekle
    xdraw.BiLinear.Scale(dst, bounds, src, src.Bounds(), xdraw.Over, nil)
    fw.FB.Blit(dst)
}
```

---

### Adım 11: Config ve Ana Döngü

**Dosyalar:** `internal/config/config.go`, `internal/ui/loop.go`, `cmd/simpleclient/main.go`

```go
// internal/config/config.go
package config

import (
    "flag"
    "time"
)

type Config struct {
    FBDevice     string
    KbdDevice    string
    MouseDevice  string
    ScanTimeout  time.Duration
    MaxWorkers   int
}

func Load() Config {
    var cfg Config
    flag.StringVar(&cfg.FBDevice,    "fb",      "/dev/fb0", "Framebuffer cihazı")
    flag.StringVar(&cfg.KbdDevice,   "kbd",     "",         "Klavye cihazı (boş = otomatik)")
    flag.StringVar(&cfg.MouseDevice, "mouse",   "",         "Fare cihazı (boş = otomatik)")
    flag.DurationVar(&cfg.ScanTimeout, "timeout", 500*time.Millisecond, "Tarama zaman aşımı")
    flag.IntVar(&cfg.MaxWorkers, "workers", 256, "Eşzamanlı tarama sayısı")
    flag.Parse()
    return cfg
}
```

```go
// internal/ui/loop.go
package ui

import (
    "context"
    "time"

    "github.com/diggyen/SimpleClient/internal/config"
    "github.com/diggyen/SimpleClient/internal/domain"
    "github.com/diggyen/SimpleClient/internal/inputdev"
    "github.com/diggyen/SimpleClient/internal/network"
    "github.com/diggyen/SimpleClient/internal/rdp"
)

type RDPFactory func(addr string, creds rdp.Credentials, w, h int) (*rdp.Client, error)

func Run(fb FBInterface, input *inputdev.Reader, scan domain.Scanner, cfg config.Config) {
    localIP, _ := network.DetectIP()
    state := NewUIState(localIP)

    cidr, err := network.DetectCIDR()
    if err != nil { cidr = "192.168.1.0/24" }

    scanCtx, cancelScan := context.WithCancel(context.Background())
    scanCh := scan.Start(scanCtx, []string{cidr})

    ticker := time.NewTicker(16 * time.Millisecond) // ~60 FPS cap
    dirty := true

    for {
        select {
        case ev, ok := <-scanCh:
            if ok {
                state.HandleScanEvent(ev)
                dirty = true
            }

        case ev := <-input.Events():
            mx, my := input.MousePos()
            handleInput(state, ev, fb, scan, cfg, &scanCtx, &cancelScan, mx, my)
            dirty = true

        case <-ticker.C:
            if dirty && state.Screen != ScreenSession {
                mx, my := input.MousePos()
                RenderFrame(fb, state, mx, my)
                dirty = false
            }
        }
    }
}

func handleInput(state *UIState, ev inputdev.InputEvent,
    fb FBInterface, scan domain.Scanner, cfg config.Config,
    ctx *context.Context, cancel *context.CancelFunc, mx, my int) {

    // Session modunda: tüm girdiler RDP'ye gider
    if state.Screen == ScreenSession && state.Session != nil && state.Session.rdpClient != nil {
        switch ev.Type {
        case inputdev.EvKey:
            // Ctrl+Alt+End: bağlantıyı kes
            if ev.KeyCode == inputdev.KeyEnd && state.ctrlDown && state.altDown {
                state.Session.rdpClient.Close()
                state.Session = nil
                state.Transition(ScreenDiscovery)
                return
            }
            state.Session.rdpClient.SendKey(ev.KeyCode, ev.Pressed)
        case inputdev.EvMouseMove:
            state.Session.rdpClient.SendMouse(ev.MouseX, ev.MouseY, 0)
        case inputdev.EvMouseButton:
            flags := uint16(0)
            if ev.Button == 1 {
                if ev.Pressed { flags = 0x1001 } else { flags = 0x1000 } // PTRFLAGS_DOWN | BTN1
            }
            state.Session.rdpClient.SendMouse(ev.MouseX, ev.MouseY, flags)
        }
        return
    }

    // Modifier takibi (tüm ekranlarda)
    // (state'de ctrlDown, altDown tutulur — bu örnekte state'e ekle)

    switch state.Screen {
    case ScreenDiscovery:
        handleDiscoveryInput(state, ev, scan, cfg, ctx, cancel, mx, my, fb)
    case ScreenModal:
        handleModalInput(state, ev, fb, cfg)
    }
}

func handleDiscoveryInput(state *UIState, ev inputdev.InputEvent,
    scan domain.Scanner, cfg config.Config,
    ctx *context.Context, cancel *context.CancelFunc, mx, my int, fb FBInterface) {

    if ev.Type != inputdev.EvKey && ev.Type != inputdev.EvMouseButton { return }

    switch {
    case ev.Type == inputdev.EvKey && ev.Pressed:
        switch ev.KeyCode {
        case inputdev.KeyUp:
            state.MoveSelection(-1)
            adjustScroll(state, fb.Bounds().Dy())
        case inputdev.KeyDown:
            state.MoveSelection(1)
            adjustScroll(state, fb.Bounds().Dy())
        case inputdev.KeyPageUp:
            state.MoveSelection(-state.VisibleRowCount(fb.Bounds().Dy()))
            adjustScroll(state, fb.Bounds().Dy())
        case inputdev.KeyPageDown:
            state.MoveSelection(state.VisibleRowCount(fb.Bounds().Dy()))
            adjustScroll(state, fb.Bounds().Dy())
        case inputdev.KeyEnter:
            if state.SelectedHost() != nil {
                state.Modal = ModalState{}
                state.Transition(ScreenModal)
            }
        case inputdev.KeyF5:
            state.Hosts = nil
            state.SelectedIdx = 0
            state.ScrollOffset = 0
            state.ScanDone = false
            state.ScanProgress = 0
            (*cancel)()
            newCtx, newCancel := context.WithCancel(context.Background())
            *ctx = newCtx
            *cancel = newCancel
            cidr, _ := network.DetectCIDR()
            newCh := scan.Start(newCtx, []string{cidr})
            // scanCh güncellenmeli — loop.go'da kanal referansı güncelle
        }

    case ev.Type == inputdev.EvMouseButton && ev.Button == 1 && ev.Pressed:
        // Fare ile satır seçimi
        listTop := topBarH + 8
        rowIdx := (my-listTop)/rowHeight + state.ScrollOffset
        if rowIdx >= 0 && rowIdx < len(state.Hosts) {
            state.SelectedIdx = rowIdx
            state.Modal = ModalState{}
            state.Transition(ScreenModal)
        }
    }
}

func handleModalInput(state *UIState, ev inputdev.InputEvent, fb FBInterface, cfg config.Config) {
    if ev.Type == inputdev.EvKey && ev.Pressed {
        switch ev.KeyCode {
        case inputdev.KeyEsc:
            state.Transition(ScreenDiscovery)
        case inputdev.KeyTab:
            state.Modal.FocusIdx = (state.Modal.FocusIdx + 1) % 5
        case inputdev.KeyEnter:
            if state.Modal.FocusIdx == 4 { // İptal
                state.Transition(ScreenDiscovery)
            } else { // Bağlan
                go connectRDP(state, fb, cfg)
            }
        case inputdev.KeyBackspace:
            state.ModalBackspace()
        default:
            if ev.Rune != 0 {
                state.ModalAddChar(ev.Rune)
            }
        }
    }
}

func connectRDP(state *UIState, fb FBInterface, cfg config.Config) {
    host := state.SelectedHost()
    if host == nil { return }

    creds := rdp.Credentials{
        Username: state.Modal.Fields[0],
        Password: state.Modal.Fields[1],
        Domain:   state.Modal.Fields[2],
    }

    state.Transition(ScreenConnecting)

    client, err := rdp.New(host.AddrRDP(), creds, fb.Bounds().Dx(), fb.Bounds().Dy())
    if err != nil {
        state.Modal.Error = simplifyRDPError(err)
        state.Transition(ScreenModal)
        return
    }

    writer := &rdp.FrameWriter{FB: fb}
    state.Session = &SessionState{
        Target:    *host,
        rdpClient: client,
    }
    state.Transition(ScreenSession)

    // RDP frame döngüsü (bu goroutine session bitene kadar çalışır)
    for frame := range client.Frames() {
        writer.Write(frame)
    }

    // Frames() kapandı = bağlantı kesildi
    state.Session = nil
    state.Transition(ScreenDiscovery)
    state.ErrorMsg = "Bağlantı kesildi"
}

func simplifyRDPError(err error) string {
    msg := err.Error()
    if contains(msg, "timeout")   { return "Zaman aşımı: sunucuya ulaşılamıyor" }
    if contains(msg, "auth")      { return "Kimlik doğrulama başarısız" }
    if contains(msg, "refused")   { return "Bağlantı reddedildi" }
    return "Bağlantı hatası: " + msg
}

func adjustScroll(state *UIState, screenH int) {
    visible := state.VisibleRowCount(screenH)
    if state.SelectedIdx < state.ScrollOffset {
        state.ScrollOffset = state.SelectedIdx
    }
    if state.SelectedIdx >= state.ScrollOffset+visible {
        state.ScrollOffset = state.SelectedIdx - visible + 1
    }
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsHelper(s, sub)) }
func containsHelper(s, sub string) bool {
    for i := 0; i <= len(s)-len(sub); i++ {
        if s[i:i+len(sub)] == sub { return true }
    }
    return false
}
```

State'e `ctrlDown`, `altDown`, `rdpClient` eklenmelidir. `SessionState`:
```go
type SessionState struct {
    Target    domain.Host
    rdpClient *rdp.Client
}
```

```go
// cmd/simpleclient/main.go
package main

import (
    "log"

    "github.com/diggyen/SimpleClient/internal/config"
    "github.com/diggyen/SimpleClient/internal/framebuffer"
    "github.com/diggyen/SimpleClient/internal/inputdev"
    "github.com/diggyen/SimpleClient/internal/scanner"
    "github.com/diggyen/SimpleClient/internal/ui"
)

func main() {
    cfg := config.Load()

    fb, err := framebuffer.Open(cfg.FBDevice)
    if err != nil { log.Fatalf("Framebuffer açılamadı: %v\nÇalıştırırken /dev/fb0 gereklidir.", err) }
    defer fb.Close()

    kbdPath := cfg.KbdDevice
    if kbdPath == "" {
        kbdPath, err = inputdev.DetectKeyboard()
        if err != nil { log.Printf("Klavye bulunamadı: %v (devam ediliyor)", err) }
    }

    mousePath := cfg.MouseDevice
    if mousePath == "" {
        mousePath, err = inputdev.DetectMouse()
        if err != nil { log.Printf("Fare bulunamadı: %v (devam ediliyor)", err) }
    }

    input, err := inputdev.New(kbdPath, mousePath, fb.Width, fb.Height)
    if err != nil { log.Fatalf("Input cihazı açılamadı: %v", err) }
    defer input.Close()

    scan := scanner.New(cfg.MaxWorkers, cfg.ScanTimeout)

    // ui.Run asla dönmez (kiosk döngüsü)
    ui.Run(fb, input, scan, cfg)
}
```

**🔍 Checkpoint:** `CGO_ENABLED=0 go build ./cmd/simpleclient` → statik binary. QEMU'da `qemu-system-x86_64 -m 256M -cdrom SimpleClient.iso -display sdl` çalıştırıldığında ekranda SimpleClient UI görünmeli.

---

### Adım 12: ISO Build

**Dosyalar:** `build/init`, `build/grub.cfg`, `build/Makefile`, `build/Dockerfile`

**`build/init`:**
```sh
#!/bin/sh
mount -t proc proc /proc
mount -t sysfs sysfs /sys
mount -t devtmpfs devtmpfs /dev

echo 0 > /proc/sys/kernel/printk

# BusyBox symlink'leri
/bin/busybox --install /bin 2>/dev/null

# Ağ
ip link set eth0 up 2>/dev/null
udhcpc -i eth0 -n -q 2>/dev/null || ip addr add 169.254.100.100/16 dev eth0

# Kiosk döngüsü
while true; do
    /sbin/SimpleClient
    sleep 2
done
```

**`build/grub.cfg`:**
```
set timeout=1
set default=0
menuentry "SimpleClient" {
    linux /boot/vmlinuz quiet loglevel=0 console=tty0 panic=5 vt.global_cursor_default=0
    initrd /boot/initramfs.cpio.gz
}
```

**`build/Makefile`:**
```makefile
BINARY  := build/rootfs/sbin/SimpleClient
ISO     := SimpleClient.iso
ROOTFS  := build/rootfs
ISO_ROOT:= build/iso-root

.PHONY: all binary busybox initramfs iso clean docker-build

all: binary initramfs iso

binary:
	@mkdir -p $(ROOTFS)/sbin $(ROOTFS)/bin $(ROOTFS)/dev $(ROOTFS)/proc $(ROOTFS)/sys $(ROOTFS)/etc
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	    go build -ldflags '-s -w -extldflags "-static"' \
	    -o $(BINARY) ./cmd/simpleclient
	@echo "Binary: $$(du -sh $(BINARY) | cut -f1)"

initramfs: binary
	@cp build/init $(ROOTFS)/init && chmod +x $(ROOTFS)/init
	@cd $(ROOTFS) && find . | cpio -o -H newc 2>/dev/null | gzip -9 > ../initramfs.cpio.gz
	@echo "initramfs: $$(du -sh build/initramfs.cpio.gz | cut -f1)"

iso: initramfs
	@mkdir -p $(ISO_ROOT)/boot/grub
	@cp build/vmlinuz $(ISO_ROOT)/boot/ 2>/dev/null || (echo "HATA: build/vmlinuz bulunamadı. Önce kernel derleyin." && exit 1)
	@cp build/initramfs.cpio.gz $(ISO_ROOT)/boot/
	@cp build/grub.cfg $(ISO_ROOT)/boot/grub/grub.cfg
	@grub-mkrescue -o $(ISO) $(ISO_ROOT) 2>/dev/null
	@echo "ISO: $$(du -sh $(ISO) | cut -f1)"

clean:
	rm -rf $(ROOTFS) build/initramfs.cpio.gz $(ISO_ROOT) $(ISO) SimpleClient

docker-build:
	docker build -f build/Dockerfile -t SimpleClient-builder .
	docker run --rm -v "$$(pwd)":/workspace -w /workspace SimpleClient-builder make all
```

---

## Kalite Kontrolleri

Tüm adımlar tamamlandıktan sonra doğrula:

- [ ] `go vet ./...` → 0 uyarı
- [ ] `go test ./...` → 0 hata
- [ ] `CGO_ENABLED=0 go build ./cmd/simpleclient` → hatasız
- [ ] `ldd SimpleClient` → "not a dynamic executable"
- [ ] `file SimpleClient` → "ELF 64-bit, statically linked"
- [ ] `unsafe.Sizeof(framebuffer.fbVarScreenInfo{})` == 160
- [ ] `scanner.ExpandCIDR("192.168.1.0/24")` → 254 IP
- [ ] MockFB ile render testleri geçer
- [ ] QEMU SDL modda UI görünür ve klavye girdisi çalışır
- [ ] `make iso` → SimpleClient.iso < 60 MB
- [ ] `file SimpleClient.iso` → "ISO 9660 CD-ROM filesystem data"
- [ ] Gerçek veya sanal Windows makinesiyle RDP bağlantısı kurulur
- [ ] Ctrl+Alt+End → session kapatılır, liste ekranına dönülür
- [ ] Binary çöktüğünde init onu yeniden başlatır
