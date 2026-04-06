# SimpleClient — Implementation Plan

> SPECIFICATION.md'den türetilmiş teknik blueprint. Kiosk framebuffer mimarisi.

---

## 1. Tech Stack

### 1.1 Stack Özeti

| Katman | Teknoloji | Versiyon | Gerekçe |
|--------|-----------|----------|---------|
| Dil | Go | 1.23 | Statik binary; syscall ile /dev/fb0 ve /dev/input erişimi |
| Framebuffer | stdlib syscall + mmap | — | /dev/fb0 direkt piksel yazım; harici kütüphane gerektirmez |
| Font rendering | `golang.org/x/image/font` + `golang.org/x/image/font/basicfont` | latest | Gömülü bitmap font; sıfır harici dep |
| Görüntü işleme | `image`, `image/draw`, `image/color` (stdlib) | — | UI compositing ve RDP bitmap işleme |
| Input | stdlib syscall (Linux evdev struct okuma) | — | /dev/input/event* direkt okuma |
| RDP istemcisi | `github.com/tomatome/grdp` | latest | Saf Go RDP; tek Go modülü |
| UUID | `github.com/google/uuid` | latest | Oturum ID üretimi |
| Build | Docker + Alpine | 3.20 | Tekrarlanabilir build |
| Kernel | Linux | 6.6 LTS | Minimal config, framebuffer desteği |
| Init sistemi | BusyBox | 1.36.1 | Tüm init araçları tek binary |
| Bootloader | GRUB2 | 2.12 | UEFI + BIOS |

**Web sunucu, WebSocket, HTTP: YOK.** Önceki versiyona kıyasla tamamen kaldırıldı.

### 1.2 Temel Teknik Kararlar

#### Karar: Framebuffer Erişim Yöntemi

- **Bağlam**: SPEC §3.2 — X11/Wayland olmadan ekrana piksel yazılmalı
- **Değerlendirilen Seçenekler**:
  1. **Direkt `/dev/fb0` syscall + mmap**: Sıfır bağımlılık. `/dev/fb0` açılır, `ioctl(FBIOGET_VSCREENINFO)` ile çözünürlük/bit-derinliği alınır, `mmap` ile belleğe eşlenir, `image.RGBA` çerçevesi kopyalanır. Pro: Tam kontrol, sıfır dep. Con: Platforma özgü struct'lar.
  2. **`github.com/nicowillis/framebuffer` vb.**: Küçük sarmalayıcı kütüphaneler. Con: Bakımı zayıf, ek bağımlılık.
  3. **Ebiten (game engine)**: KMS/DRM desteği var. Con: Büyük kütüphane, CGo gerektirebilir.
- **Seçim**: Direkt syscall + mmap
- **Gerekçe**: 50 satır Go kodu yeterli; ek bağımlılık yaratmaz; tam kontrol.

#### Karar: Input Okuma

- **Bağlam**: SPEC §3.2 — Klavye ve fare girdisi alınmalı
- **Değerlendirilen Seçenekler**:
  1. **Direkt evdev** (`/dev/input/event*` okuma): Linux `input_event` struct'ı (24 byte) doğrudan okunur. Sıfır dep.
  2. **`/dev/input/mice`** (PS/2 protokolü): Sadece fare için, klavye desteği yok.
  3. **`golang.org/x/term` + ncurses**: Terminal-based; framebuffer ile uyumsuz.
- **Seçim**: Direkt evdev
- **Gerekçe**: Tek mekanizma hem klavye hem fareyi kapsar; stdlib syscall ile okunur.

#### Karar: Font Rendering

- **Bağlam**: SPEC §3.2 — UI'da metin görüntülenmeli
- **Değerlendirilen Seçenekler**:
  1. **`golang.org/x/image/font/basicfont`**: Binary'ye gömülü 7x13 piksel bitmap font. Sıfır harici dep.
  2. **FreeType (`github.com/golang/freetype`)**: Vektörel font. Pro: Güzel görünüm. Con: TTF dosyası bundle gerekir, CGo.
  3. **Gömülü TrueType + `golang.org/x/image/font/opentype`**: Saf Go. Con: Daha büyük binary, font dosyası embed gerekir.
- **Seçim**: İki katman: `basicfont` (sistem bilgileri, UI etiketi) + Go'ya embed edilmiş tek TTF (başlık, sunucu isimleri)
- **Pratik seçim**: Sadece `basicfont` + büyük metin için 2x ölçekleme. Basit ve bağımlılıksız.

#### Karar: UI Render Stratejisi

- **Bağlam**: 30 FPS'de tüm ekranı yeniden çizmek maliyetli olabilir
- **Seçim**: **Dirty region render** — sadece değişen bölgeler güncellenir; framebuffer'a kopyalama dikdörtgen bazlı yapılır
- **Gerekçe**: 128 MB RAM kısıtında full-screen 32bpp double-buffer (~8 MB) makul; diff + partial copy daha verimli

### 1.3 Bağımlılık Envanteri

| Paket | Amaç | Lisans | Gerekçe |
|-------|------|--------|---------|
| `github.com/tomatome/grdp` | Saf Go RDP istemcisi | MIT | Mevcut tek saf Go RDP impl. |
| `github.com/google/uuid` | Oturum ID üretimi | BSD-3 | Stdlib'de UUID yok |
| `golang.org/x/image` | Bitmap font + image ops | BSD-3 | basicfont için gerekli |

**Toplam: 3 doğrudan bağımlılık.** Önceki versiyona göre `x/net/websocket` kaldırıldı.

---

## 2. Tasarım Desenleri

### 2.1 Mimari Desen: State Machine (Ekran Durumları)

**Neden**: SPEC §3 — 4 farklı ekran durumu var; geçişler kontrollü olmalı.

```go
// internal/ui/state.go
type Screen int
const (
    ScreenDiscovery Screen = iota  // Ana liste
    ScreenModal                    // Kimlik bilgisi diyalogu
    ScreenConnecting               // "Bağlanıyor..." bekleme
    ScreenSession                  // Aktif RDP oturumu
)

type UIState struct {
    Screen       Screen
    Hosts        []domain.Host
    SelectedIdx  int
    ScrollOffset int
    ScanProgress float64
    ScanDone     bool
    Modal        ModalState
    ErrorMsg     string
    Session      *SessionState
}

// Geçiş: her ekran sadece belirli geçişlere izin verir
func (s *UIState) Transition(to Screen) error {
    allowed := map[Screen][]Screen{
        ScreenDiscovery:  {ScreenModal},
        ScreenModal:      {ScreenDiscovery, ScreenConnecting},
        ScreenConnecting: {ScreenSession, ScreenModal},
        ScreenSession:    {ScreenDiscovery},
    }
    // ...
}
```

### 2.2 Desen: Fan-out/Fan-in — Ağ Tarama

**Neden**: SPEC §3.2 — /24 subnet < 10 saniyede taranmalı.

```go
// internal/scanner/scanner.go
func (s *NetworkScanner) Start(ctx context.Context, cidrs []string) <-chan domain.ScanEvent {
    events := make(chan domain.ScanEvent, 64)
    sem := make(chan struct{}, s.concurrency) // 256 slot

    go func() {
        defer close(events)
        var wg sync.WaitGroup
        for _, ip := range allIPs {
            sem <- struct{}{}
            wg.Add(1)
            go func(target net.IP) {
                defer wg.Done()
                defer func() { <-sem }()
                if h, ok := probeRDP(ctx, target, s.timeout); ok {
                    events <- domain.ScanEvent{Type: domain.EventHostFound, Host: &h}
                }
                events <- domain.ScanEvent{Type: domain.EventScanProgress, ...}
            }(ip)
        }
        wg.Wait()
        events <- domain.ScanEvent{Type: domain.EventScanComplete}
    }()
    return events
}
```

### 2.3 Desen: Render Loop (Event-driven)

**Neden**: UI'ın sadece state değiştiğinde yeniden render edilmesi gerekir; boşta CPU harcanmaz.

```go
// internal/ui/loop.go
func Run(fb *framebuffer.FB, input *inputdev.Reader, scanner domain.Scanner, rdpNew RDPFactory) {
    state := NewUIState()
    dirty := true

    // Kanallar: tarama olayları + input olayları birleştirilir
    scanEvents := scanner.Start(context.Background(), []string{autoDetectCIDR()})
    inputEvents := input.Events()

    ticker := time.NewTicker(16 * time.Millisecond) // ~60 FPS cap

    for {
        select {
        case ev := <-scanEvents:
            applyScancEvent(&state, ev)
            dirty = true
        case ev := <-inputEvents:
            applyInputEvent(&state, ev, rdpNew)
            dirty = true
        case <-ticker.C:
            if dirty {
                render(fb, &state)
                dirty = false
            }
        }
    }
}
```

### 2.4 Desen: Adapter — Framebuffer

**Neden**: `/dev/fb0` erişimini soyutlamak test ve potansiyel alternatif backend'ler için.

```go
// internal/framebuffer/fb.go
type FB struct {
    file       *os.File
    mem        []byte  // mmap
    Width      int
    Height     int
    BitsPerPix int
    LineLen    int     // bytes per row (stride)
}

func Open(path string) (*FB, error)        // /dev/fb0 aç, ioctl ile bilgi al, mmap et
func (fb *FB) Blit(img *image.RGBA)        // tüm ekranı kopyala
func (fb *FB) BlitRect(img *image.RGBA, r image.Rectangle)  // partial update
func (fb *FB) Close() error
```

### 2.5 Desen: Adapter — evdev Input

**Neden**: Linux `input_event` struct'ını domain event'lerine çevirmek.

```go
// internal/inputdev/reader.go
// Linux input_event: { time [16 bytes], type uint16, code uint16, value int32 }
type InputEvent struct {
    Type    EventType  // Key, RelMouse, AbsMouse
    KeyCode int        // Klavye: Linux keycode
    Rune    rune       // Baskı yapılabilir karakter (varsa)
    MouseDX int        // Fare göreli X hareketi
    MouseDY int        // Fare göreli Y hareketi
    Button  int        // Sol=1, Sağ=2, Orta=3
    Pressed bool       // true=basıldı, false=bırakıldı
}

type Reader struct {
    kbdFd   *os.File  // klavye cihazı
    mouseFd *os.File  // fare cihazı
    ch      chan InputEvent
}

func Open(kbdPath, mousePath string) (*Reader, error)
func (r *Reader) Events() <-chan InputEvent
func (r *Reader) Close()
```

### 2.6 Desen: Strategy — Render

**Neden**: Her ekran kendi render fonksiyonuna sahip; state machine hangi fonksiyonun çağrılacağını belirler.

```go
// internal/ui/render.go
func render(fb *framebuffer.FB, state *UIState) {
    // Back buffer: image.RGBA çiz, sonra fb.Blit()
    img := image.NewRGBA(image.Rect(0, 0, fb.Width, fb.Height))

    switch state.Screen {
    case ScreenDiscovery:  renderDiscovery(img, state)
    case ScreenModal:      renderDiscovery(img, state); renderModal(img, state)
    case ScreenConnecting: renderDiscovery(img, state); renderConnecting(img, state)
    case ScreenSession:    // RDP bitmap direkt fb.Blit ile gelir (render atlanır)
    }

    fb.Blit(img)
}
```

---

## 3. Proje Yapısı

### 3.1 Dizin Düzeni

```
SimpleClient/
├── cmd/
│   └── SimpleClient/
│       └── main.go              # Giriş noktası; wire-up, ana döngü
├── internal/
│   ├── domain/
│   │   ├── host.go              # Host entity
│   │   ├── events.go            # ScanEvent tipleri
│   │   └── interfaces.go        # Scanner interface
│   ├── framebuffer/
│   │   ├── fb.go                # /dev/fb0 aç, mmap, blit
│   │   ├── fb_info.go           # ioctl FBIOGET_VSCREENINFO struct binding
│   │   └── fb_test.go           # Mock FB testi (gerçek /dev/fb0 olmadan)
│   ├── inputdev/
│   │   ├── reader.go            # evdev /dev/input/event* okuma
│   │   ├── keycodes.go          # Linux keycode → rune/özel tuş eşlemesi
│   │   └── reader_test.go
│   ├── ui/
│   │   ├── state.go             # UIState struct, Screen enum, state transitions
│   │   ├── loop.go              # Ana UI döngüsü (scan events + input events → render)
│   │   ├── render.go            # render() dispatch fonksiyonu
│   │   ├── render_discovery.go  # Sunucu listesi render
│   │   ├── render_modal.go      # Kimlik bilgisi diyalogu render
│   │   ├── render_connecting.go # "Bağlanıyor..." render
│   │   ├── draw.go              # Temel çizim primitifleri: rect, text, line
│   │   ├── colors.go            # Renk paleti sabitleri
│   │   └── fonts.go             # basicfont + ölçekleme yardımcıları
│   ├── scanner/
│   │   ├── scanner.go           # NetworkScanner (fan-out goroutine pool)
│   │   ├── cidr.go              # CIDR parse, IP aralığı genişletme
│   │   └── scanner_test.go
│   ├── rdp/
│   │   ├── client.go            # grdp sarmalayıcı
│   │   ├── framewriter.go       # RDP bitmap → image.RGBA → framebuffer
│   │   └── client_test.go
│   ├── network/
│   │   ├── iface.go             # Ağ arayüzü keşfi, CIDR tespiti
│   │   └── iface_test.go
│   └── config/
│       └── config.go            # CLI flag'leri
├── build/
│   ├── Dockerfile               # Cross-compile + ISO build ortamı
│   ├── Makefile                 # Build hedefleri
│   ├── init                     # BusyBox init betiği
│   ├── kernel.config            # Minimal Linux kernel config
│   └── grub.cfg                 # GRUB konfigürasyonu
├── go.mod
├── go.sum
└── README.md
```

**Web/ dizini kaldırıldı. Handler/, middleware/, events/ kaldırıldı.**

### 3.2 Modül Bağımlılık Grafiği

```
cmd/simpleclient
    ├── ui/loop ──→ ui/state ──→ domain
    │           ├──→ ui/render ──→ framebuffer
    │           │              └──→ ui/draw
    │           ├──→ scanner ──→ domain
    │           ├──→ inputdev
    │           └──→ rdp ──→ framebuffer
    ├── network
    └── config

domain ──→ (stdlib only)
framebuffer ──→ (stdlib + syscall)
inputdev ──→ (stdlib + syscall)
```

---

## 4. Framebuffer Implementasyonu

### 4.1 Framebuffer Açma ve Bilgi Alma

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

// Linux FBIOGET_VSCREENINFO ioctl
const FBIOGET_VSCREENINFO = 0x4600

type fbVarScreenInfo struct {
    XRes          uint32
    YRes          uint32
    XResVirtual   uint32
    YResVirtual   uint32
    XOffset       uint32
    YOffset       uint32
    BitsPerPixel  uint32
    // ... (tam struct: 160 byte)
}

type FB struct {
    file       *os.File
    mem        []byte
    Width      int
    Height     int
    Stride     int // bytes per row
    BPP        int // bits per pixel
}

func Open(path string) (*FB, error) {
    f, err := os.OpenFile(path, os.O_RDWR, 0)
    if err != nil {
        return nil, fmt.Errorf("framebuffer açılamadı: %w", err)
    }

    var info fbVarScreenInfo
    _, _, errno := syscall.Syscall(syscall.SYS_IOCTL,
        f.Fd(), FBIOGET_VSCREENINFO, uintptr(unsafe.Pointer(&info)))
    if errno != 0 {
        f.Close()
        return nil, fmt.Errorf("FBIOGET_VSCREENINFO: %v", errno)
    }

    stride := int(info.XRes) * int(info.BitsPerPixel) / 8
    size := stride * int(info.YRes)

    mem, err := syscall.Mmap(int(f.Fd()), 0, size,
        syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
    if err != nil {
        f.Close()
        return nil, fmt.Errorf("mmap başarısız: %w", err)
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

// Blit: image.RGBA back buffer'ı framebuffer'a kopyalar
func (fb *FB) Blit(img *image.RGBA) {
    for y := 0; y < fb.Height; y++ {
        srcRow := img.Pix[y*img.Stride : y*img.Stride+fb.Width*4]
        dstOffset := y * fb.Stride
        // RGBA → BGR (framebuffer genellikle BGRA veya BGR sırasındadır)
        for x := 0; x < fb.Width; x++ {
            fb.mem[dstOffset+x*4+0] = srcRow[x*4+2] // B
            fb.mem[dstOffset+x*4+1] = srcRow[x*4+1] // G
            fb.mem[dstOffset+x*4+2] = srcRow[x*4+0] // R
            fb.mem[dstOffset+x*4+3] = 0xFF           // A
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

### 4.2 Draw Primitifleri

```go
// internal/ui/draw.go
package ui

import (
    "image"
    "image/color"
    "image/draw"
    "golang.org/x/image/font"
    "golang.org/x/image/font/basicfont"
    "golang.org/x/image/math/fixed"
)

// FillRect: dikdörtgeni tek renkle doldur
func FillRect(img *image.RGBA, r image.Rectangle, c color.RGBA) {
    draw.Draw(img, r, &image.Uniform{c}, image.Point{}, draw.Src)
}

// DrawText: metin çiz (basicfont kullanarak)
func DrawText(img *image.RGBA, x, y int, text string, c color.RGBA) {
    d := &font.Drawer{
        Dst:  img,
        Src:  &image.Uniform{c},
        Face: basicfont.Face7x13,
        Dot:  fixed.P(x, y),
    }
    d.DrawString(text)
}

// DrawTextLarge: 2x ölçekli metin (başlık için)
func DrawTextLarge(img *image.RGBA, x, y int, text string, c color.RGBA) {
    // basicfont karakterlerini 2x büyüterek çiz
    // Her karakteri 7x13 → 14x26 piksel olarak render et
}

// DrawHLine, DrawVLine: yatay/dikey çizgi
func DrawHLine(img *image.RGBA, x1, x2, y int, c color.RGBA) { ... }
func DrawVLine(img *image.RGBA, x, y1, y2 int, c color.RGBA)  { ... }

// DrawBorder: dikdörtgen çerçeve
func DrawBorder(img *image.RGBA, r image.Rectangle, c color.RGBA) { ... }

// ProgressBar: ilerleme çubuğu
func DrawProgressBar(img *image.RGBA, r image.Rectangle, pct float64, fg, bg color.RGBA) { ... }
```

---

## 5. Input (evdev) Implementasyonu

### 5.1 Evdev Okuma

```go
// internal/inputdev/reader.go
package inputdev

import (
    "encoding/binary"
    "os"
    "time"
)

// Linux input_event struct: 24 bytes total
// struct timeval (16 bytes) + type (2) + code (2) + value (4)
type linuxInputEvent struct {
    SecHi  uint32
    SecLo  uint32
    UsecHi uint32
    UsecLo uint32
    Type   uint16
    Code   uint16
    Value  int32
}

const (
    evKey = 0x01  // klavye/buton olayı
    evRel = 0x02  // fare göresel hareketi
    evSyn = 0x00  // sync (ignore)
)

// Cihaz dosyalarını otomatik bul
func DetectDevices() (kbdPath, mousePath string, err error) {
    // /proc/bus/input/devices parse et veya /dev/input/event* dene
    // Klavye: EV_KEY + KEY_A var
    // Fare: EV_REL var
}
```

### 5.2 Fare Pozisyonu Takibi

Fare girdisi göresel (relative) olduğundan mutlak pozisyon hesaplanır:

```go
type Reader struct {
    mouseX, mouseY int       // Mutlak pozisyon
    screenW, screenH int     // Sınır için
    ch chan InputEvent
}

func (r *Reader) loop() {
    for ev := range rawEvents {
        if ev.Type == evRel {
            if ev.Code == 0 { // REL_X
                r.mouseX = clamp(r.mouseX + int(ev.Value), 0, r.screenW-1)
            }
            if ev.Code == 1 { // REL_Y
                r.mouseY = clamp(r.mouseY + int(ev.Value), 0, r.screenH-1)
            }
        }
        // Fare imleci ekranda çizilir (UI render sırasında mouseX/mouseY kullanılır)
    }
}
```

---

## 6. UI Render Detayları

### 6.1 Renk Paleti

```go
// internal/ui/colors.go
var (
    ColorBG        = color.RGBA{26, 26, 46, 255}    // #1a1a2e koyu lacivert
    ColorPanel     = color.RGBA{22, 33, 62, 255}    // #16213e
    ColorAccent    = color.RGBA{233, 69, 96, 255}   // #e94560 kırmızı
    ColorText      = color.RGBA{224, 224, 224, 255}  // #e0e0e0
    ColorSubText   = color.RGBA{136, 136, 136, 255}  // #888
    ColorHighlight = color.RGBA{15, 52, 96, 255}    // #0f3460
    ColorGreen     = color.RGBA{100, 221, 100, 255}  // bağlı
    ColorError     = color.RGBA{255, 80, 80, 255}    // hata
    ColorBorder    = color.RGBA{30, 40, 70, 255}
    ColorCursor    = color.RGBA{255, 255, 100, 255}  // fare imleci
)
```

### 6.2 Keşif Ekranı Düzeni

```
┌──────────────── fb.Width ──────────────────┐
│ ▌SimpleClient▐    192.168.1.50    Tarıyor... 45%│  ← TopBar (40px)
├────────────────────────────────────────────┤
│                                            │
│  ▶ 192.168.1.50   WIN-SERVER01    12 ms    │  ← Seçili satır (vurgulanmış)
│    192.168.1.82   DESKTOP-PC02    8 ms     │
│    192.168.1.101  [bilinmiyor]    45 ms    │
│    192.168.1.200  WIN-DC01        3 ms     │
│                                            │  ← Liste alanı (fb.Height - 80px)
│                                            │
├────────────────────────────────────────────┤
│ ↑↓ Gezin  ENTER Bağlan  F5 Tara  ESC Çıkış│  ← BottomBar (40px)
│ ░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░  │  ← Progress bar (4px)
└────────────────────────────────────────────┘
```

### 6.3 Modal Düzeni

```
         ┌──────────────────────────────┐
         │  192.168.1.50 / WIN-SERVER01 │ başlık
         │ ────────────────────────────  │
         │  Kullanıcı Adı               │
         │  ┌────────────────────────┐  │
         │  │ Administrator_         │  │ aktif alan (cursor)
         │  └────────────────────────┘  │
         │  Şifre                       │
         │  ┌────────────────────────┐  │
         │  │ ●●●●●●●●               │  │
         │  └────────────────────────┘  │
         │  Domain (isteğe bağlı)       │
         │  ┌────────────────────────┐  │
         │  │ WORKGROUP_             │  │
         │  └────────────────────────┘  │
         │  [  Bağlan  ]  [  İptal  ]   │
         │                              │
         │  Hata: Kimlik doğrulama...   │ ← hata varsa
         └──────────────────────────────┘
```

---

## 7. RDP → Framebuffer Akışı

```go
// internal/rdp/framewriter.go
package rdp

// RDP bitmap güncellemesi geldiğinde doğrudan framebuffer'a yaz
// NOT: Bu, session aktifken render loop'u bypass eder —
// RDP frame'leri normal UI render'ı yerine doğrudan fb.Blit() ile yazılır

type FrameWriter struct {
    fb *framebuffer.FB
}

func (fw *FrameWriter) Write(img image.Image) {
    // 1. img'yi fb.Width x fb.Height'a ölçekle (RDP çözünürlüğü ≠ ekran)
    // 2. image.RGBA'ya dönüştür
    // 3. fb.Blit() ile framebuffer'a yaz
    scaled := scaleToFit(img, fw.fb.Width, fw.fb.Height)
    rgba := toRGBA(scaled)
    fw.fb.Blit(rgba)
}

func scaleToFit(src image.Image, w, h int) image.Image {
    // golang.org/x/image/draw ile bilinear ölçekleme
    dst := image.NewRGBA(image.Rect(0, 0, w, h))
    xdraw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)
    return dst
}
```

---

## 8. init Betiği (Kiosk Kilidi)

```sh
#!/bin/sh
# Sanal dosya sistemleri
mount -t proc proc /proc
mount -t sysfs sysfs /sys
mount -t devtmpfs devtmpfs /dev

# Çekirdek mesajlarını sustur
echo 0 > /proc/sys/kernel/printk

# Sanal terminal geçişini devre dışı bırak (kiosk kilidi)
# echo 1 > /sys/module/vt/parameters/default_utf8  # (gerekirse)

# Ağ başlatma
ip link set eth0 up
udhcpc -i eth0 -n -q -s /etc/udhcpc.script 2>/dev/null || {
    ip addr add 169.254.100.100/16 dev eth0
}

# Kiosk döngüsü: binary çökerse yeniden başlat
while true; do
    /sbin/SimpleClient
    sleep 2
done
```

---

## 9. Kernel Konfigürasyonu

```
# Framebuffer desteği (zorunlu)
CONFIG_FB=y
CONFIG_FB_VESA=y              # VESA framebuffer (geniş uyumluluk)
CONFIG_FB_EFI=y               # UEFI framebuffer
CONFIG_FRAMEBUFFER_CONSOLE=n  # Kernel mesajlarını framebuffer'a yazma (biz yapıyoruz)
CONFIG_VT=y
CONFIG_VT_CONSOLE=y

# Input (evdev)
CONFIG_INPUT=y
CONFIG_INPUT_EVDEV=y          # /dev/input/event* desteği
CONFIG_INPUT_KEYBOARD=y
CONFIG_INPUT_MOUSE=y
CONFIG_MOUSE_PS2=y
CONFIG_HID=y
CONFIG_HID_GENERIC=y
CONFIG_USB_HID=y              # USB klavye/fare

# Ağ
CONFIG_NET=y
CONFIG_INET=y
CONFIG_NETDEVICES=y
CONFIG_ETHERNET=y
CONFIG_E1000=y
CONFIG_E1000E=y
CONFIG_VIRTIO_NET=y           # Sanal makine

# Temel
CONFIG_64BIT=y
CONFIG_BLK_DEV_INITRD=y
CONFIG_RD_GZIP=y
CONFIG_TMPFS=y
CONFIG_DEVTMPFS=y
CONFIG_DEVTMPFS_MOUNT=y

# Devre dışı (kiosk = minimal)
CONFIG_SOUND=n
CONFIG_MEDIA_SUPPORT=n
CONFIG_USB_STORAGE=n
CONFIG_SYSRQ=n                # SysRq tuşu kiosk'ta kapalı
CONFIG_MAGIC_SYSRQ=n
```

---

## 10. Config

```go
// internal/config/config.go
type Config struct {
    FBDevice     string        // -fb /dev/fb0
    KbdDevice    string        // -kbd /dev/input/event0 (oto-detect bırakılırsa boş)
    MouseDevice  string        // -mouse /dev/input/event1 (oto-detect)
    ScanTimeout  time.Duration // -timeout 500ms
    MaxWorkers   int           // -workers 256
    ScreenWidth  int           // RDP bağlantı genişliği (fb'den otomatik)
    ScreenHeight int           // RDP bağlantı yüksekliği (fb'den otomatik)
    JPEGFPS      int           // RDP frame iletim FPS hedefi
}
```

---

## 11. Test Stratejisi

### 11.1 Test Piramidi

| Seviye | Araç | Kapsam |
|--------|------|--------|
| Unit | `testing` stdlib | Scanner, CIDR, evdev parse, render hesaplamaları |
| Integration | Mock FB (in-memory `image.RGBA`) | UI render çıktısı piksel bazlı doğrulama |
| Manual/Visual | QEMU KVM + VNC | Gerçek framebuffer çıktısı görsel doğrulama |

### 11.2 Mock Framebuffer

```go
// Gerçek /dev/fb0 gerektirmeden render testleri için:
type MockFB struct {
    Img *image.RGBA
}
func (m *MockFB) Blit(img *image.RGBA) { draw.Draw(m.Img, ..., img, ...) }
```

### 11.3 QEMU Test

```bash
qemu-system-x86_64 \
    -m 256M \
    -cdrom SimpleClient.iso \
    -display sdl \           # veya -display gtk
    -device e1000 \
    -netdev user,id=n0
```

QEMU'da framebuffer output SDL/GTK ekranında görülür; gerçek hardware önyüklemesi gerekmez.

---

## 12. Deployment (ISO Build)

### 12.1 Dockerfile

```dockerfile
FROM alpine:3.20 AS builder

RUN apk add --no-cache \
    go gcc musl-dev musl-utils \
    linux-headers make bc flex bison \
    xorriso grub grub-efi \
    cpio gzip wget

WORKDIR /build
COPY . .

# Statik Go binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags '-s -w -extldflags "-static"' \
    -o build/rootfs/sbin/SimpleClient ./cmd/simpleclient

# BusyBox (statik)
RUN wget -q https://busybox.net/downloads/busybox-1.36.1.tar.bz2 && \
    tar xf busybox-1.36.1.tar.bz2 && \
    make -C busybox-1.36.1 defconfig && \
    sed -i 's/# CONFIG_STATIC is not set/CONFIG_STATIC=y/' busybox-1.36.1/.config && \
    make -C busybox-1.36.1 -j$(nproc) && \
    cp busybox-1.36.1/busybox build/rootfs/bin/busybox

# initramfs
RUN cd build/rootfs && find . | cpio -o -H newc | gzip > ../initramfs.cpio.gz

# ISO
RUN mkdir -p build/iso-root/boot/grub && \
    cp build/vmlinuz build/iso-root/boot/ && \
    cp build/initramfs.cpio.gz build/iso-root/boot/ && \
    cp build/grub.cfg build/iso-root/boot/grub/ && \
    grub-mkrescue -o /build/SimpleClient.iso build/iso-root
```
