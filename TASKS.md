# SimpleClient — Tasks

> Kiosk framebuffer mimarisine göre güncellenmiş görev listesi.
> Web sunucu / WebSocket / SSE kaldırıldı; framebuffer + evdev eklendi.

---

## Özet

| Metrik | Değer |
|--------|-------|
| Toplam Görev | 13 |
| Faz | 5 |
| Tahmini Efor | 45–65 saat |
| MVP (QEMU'da çalışır) | Görev 10 sonrası |
| Tam Sürüm (ISO) | Görev 13 sonrası |

---

## Faz 1: Proje Temeli

> Proje iskeleti, domain tipleri, ağ ve config altyapısı.

---

### Görev 1: Proje İskeleti ve Go Modülü

**Go modülü, dizin yapısı ve tüm boş stub'ları oluştur.**

**Oluşturulacak Dosyalar:**
- `go.mod` — `module github.com/diggyen/SimpleClient`, Go 1.23
- `cmd/simpleclient/main.go` — boş main()
- `internal/domain/host.go` — stub
- `internal/domain/events.go` — stub
- `internal/domain/interfaces.go` — stub
- `internal/framebuffer/fb.go` — stub
- `internal/framebuffer/fb_info.go` — stub
- `internal/inputdev/reader.go` — stub
- `internal/inputdev/keycodes.go` — stub
- `internal/ui/state.go` — stub
- `internal/ui/loop.go` — stub
- `internal/ui/render.go` — stub
- `internal/ui/render_discovery.go` — stub
- `internal/ui/render_modal.go` — stub
- `internal/ui/render_connecting.go` — stub
- `internal/ui/draw.go` — stub
- `internal/ui/colors.go` — stub
- `internal/ui/fonts.go` — stub
- `internal/scanner/scanner.go` — stub
- `internal/scanner/cidr.go` — stub
- `internal/rdp/client.go` — stub
- `internal/rdp/framewriter.go` — stub
- `internal/network/iface.go` — stub
- `internal/config/config.go` — stub
- `build/Makefile` — stub
- `build/Dockerfile` — stub
- `build/init` — stub
- `build/kernel.config` — stub
- `build/grub.cfg` — stub
- `.gitignore`
- `README.md`

**Komutlar:**
```bash
go mod init github.com/diggyen/SimpleClient
go get github.com/tomatome/grdp@latest
go get github.com/google/uuid@latest
go get golang.org/x/image@latest
go mod tidy
```

**Kabul Kriterleri:**
- [ ] `go build ./...` hatasız
- [ ] `go vet ./...` uyarı yok
- [ ] `go.mod` 3 harici bağımlılık içerir (grdp, uuid, x/image)
- [ ] Tüm dizinler IMPLEMENTATION.md §3.1 yapısıyla eşleşir

**Bağımlılıklar:** Yok
**Efor:** 1–2 saat

---

### Görev 2: Domain Tipleri ve Interface'ler

**Host entity, ScanEvent tipleri ve Scanner interface'ini tanımla.**

**Düzenlenecek Dosyalar:**
- `internal/domain/host.go`
- `internal/domain/events.go`
- `internal/domain/interfaces.go`

```go
// host.go
type Host struct {
    IP           net.IP
    Hostname     string
    LatencyMs    int64
    DiscoveredAt time.Time
}
func (h Host) AddrRDP() string { return h.IP.String() + ":3389" }
func (h Host) DisplayName() string {
    if h.Hostname != "" { return h.Hostname }
    return h.IP.String()
}

// events.go
type ScanEventType string
const (
    EventHostFound    ScanEventType = "host_found"
    EventScanProgress ScanEventType = "scan_progress"
    EventScanComplete ScanEventType = "scan_complete"
)
type ScanEvent struct {
    Type       ScanEventType
    Host       *Host   // EventHostFound için
    Scanned    int
    Total      int
    DurationMs int64   // EventScanComplete için
}

// interfaces.go
type Scanner interface {
    Start(ctx context.Context, cidrs []string) <-chan ScanEvent
    Cancel()
    Hosts() []Host  // mevcut bulunan hostlar
}
```

**Kabul Kriterleri:**
- [ ] `go build ./internal/domain/...` hatasız
- [ ] `Host.DisplayName()` — hostname varsa hostname, yoksa IP döner
- [ ] Unit testler geçer

**Bağımlılıklar:** Görev 1
**Efor:** 1–2 saat

---

### Görev 3: Ağ Arayüzü Tespiti + CIDR

**Aktif ağ arayüzünü tespit et ve CIDR bloğunu genişlet.**

**Düzenlenecek Dosyalar:**
- `internal/network/iface.go`
- `internal/network/iface_test.go`
- `internal/scanner/cidr.go`
- `internal/scanner/scanner_test.go` (CIDR kısmı)

```go
// network/iface.go
func DetectCIDR() (string, error)  // "192.168.1.0/24"
func DetectIP() (string, error)    // "192.168.1.105"

// scanner/cidr.go
func ExpandCIDR(cidr string) ([]net.IP, error)
// "192.168.1.0/24" → 254 IP (network+broadcast hariç)
```

**Kabul Kriterleri:**
- [ ] `ExpandCIDR("192.168.1.0/24")` → 254 IP
- [ ] `ExpandCIDR("10.0.0.0/30")` → 2 IP
- [ ] Loopback adresler sonuçlara dahil değil
- [ ] Unit testler geçer

**Bağımlılıklar:** Görev 1
**Efor:** 2 saat

---

## Faz 2: Framebuffer ve Input

> Ekran çıktısı ve klavye/fare girdisi. Bu faz olmadan UI mümkün değil.

---

### Görev 4: Framebuffer Sürücüsü

**`/dev/fb0` açma, mmap, piksel yazma implementasyonu.**

**Düzenlenecek Dosyalar:**
- `internal/framebuffer/fb.go`
- `internal/framebuffer/fb_info.go`
- `internal/framebuffer/fb_test.go`

**Tam implementasyon** (IMPLEMENTATION.md §4.1 kod iskeletini kullan):

`fb_info.go` — `fbVarScreenInfo` struct (Linux FBIOGET_VSCREENINFO'nun Go karşılığı, tam 160-byte yapı):
```go
// Tam struct tanımı: 40 alan (xres, yres, xres_virtual, yres_virtual, xoffset, yoffset,
// bits_per_pixel, grayscale, red/green/blue/transp bitfield yapıları, nonstd,
// activate, height, width, accel_flags, pixclock, left_margin, right_margin,
// upper_margin, lower_margin, hsync_len, vsync_len, sync, vmode, rotate,
// colorspace, reserved[4])
// Go'da: unsafe.Sizeof(fbVarScreenInfo{}) == 160 doğrulanmalı
```

`fb.go`:
- `Open(path string) (*FB, error)` — ioctl ile ekran bilgisi al, mmap et
- `Blit(img *image.RGBA)` — RGBA → BGRA dönüşümü ile tam ekran kopyala
- `BlitRect(img *image.RGBA, r image.Rectangle)` — kısmi güncelleme
- `Bounds() image.Rectangle`
- `Close() error`

**Mock FB** (test için `/dev/fb0` gerektirmeyen):
```go
// fb_test.go içinde veya ayrı dosyada
type MockFB struct {
    Img    *image.RGBA
    Width  int
    Height int
}
func (m *MockFB) Blit(img *image.RGBA) { ... }
```

**Kabul Kriterleri:**
- [ ] `unsafe.Sizeof(fbVarScreenInfo{})` → 160 byte
- [ ] `Blit()` RGBA kanallarını doğru sırayla (BGRA) yazar
- [ ] MockFB ile tüm render fonksiyonları test edilebilir (gerçek /dev/fb0 gerektirmez)
- [ ] Unit testler geçer (MockFB kullanarak)

**Bağımlılıklar:** Görev 1
**Efor:** 4 saat

---

### Görev 5: Input (evdev) Sürücüsü

**`/dev/input/event*` üzerinden klavye ve fare girdisi okuma.**

**Düzenlenecek Dosyalar:**
- `internal/inputdev/reader.go`
- `internal/inputdev/keycodes.go`
- `internal/inputdev/reader_test.go`

**`reader.go` implementasyonu:**
```go
// Linux input_event: 24 bytes
// Okuma: binary.Read(f, binary.LittleEndian, &ev)

type EventType int
const (
    EvKey   EventType = iota  // tuş basma/bırakma, buton tıklama
    EvMouseMove               // fare hareketi
    EvMouseButton             // fare tuşu
)

type InputEvent struct {
    Type    EventType
    KeyCode int    // Linux keycode (EvKey için)
    Rune    rune   // Baskı yapılabilir karakter (0 = özel tuş)
    DX, DY  int   // Fare göresel hareketi (EvMouseMove)
    MouseX, MouseY int  // Hesaplanan mutlak pozisyon
    Button  int   // 1=sol, 2=sağ, 3=orta
    Pressed bool
}

// DetectKeyboard: /proc/bus/input/devices veya /dev/input/event* deneme
func DetectKeyboard() (string, error)
// DetectMouse: aynı şekilde
func DetectMouse() (string, error)

type Reader struct { ... }
func New(kbdPath, mousePath string, screenW, screenH int) (*Reader, error)
func (r *Reader) Events() <-chan InputEvent
func (r *Reader) MousePos() (int, int)  // thread-safe
func (r *Reader) Close()
```

**`keycodes.go`** — Linux keycode → rune eşlemesi:
```go
// KEY_A(30) → 'a'/'A', KEY_ENTER(28) → özel, KEY_TAB(15) → özel vb.
// Shift durumu takibi (KEY_LEFTSHIFT, KEY_RIGHTSHIFT)
var linuxKeycodeToRune = map[int][]rune{
    30: {'a', 'A'},  // KEY_A
    48: {'b', 'B'},  // KEY_B
    // ... tam tablo
}

// Özel tuş sabitleri
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
    KeyCtrl      = 29
    KeyAlt       = 56
    KeyEnd       = 107
)
```

**Kabul Kriterleri:**
- [ ] `DetectKeyboard()` ve `DetectMouse()` /proc/bus/input/devices'ı parse eder
- [ ] Shift durumu takip edilir; büyük/küçük harf ayrımı doğru
- [ ] Fare pozisyonu `clamp(0, screenW-1)` ve `clamp(0, screenH-1)` içinde kalır
- [ ] Events() channel'ı goroutine-safe
- [ ] Unit testler geçer (pipe veya temp dosyayla simüle edilmiş input stream)

**Bağımlılıklar:** Görev 1
**Efor:** 5 saat

---

## Faz 3: Ağ Tarayıcı

---

### Görev 6: Ağ Tarayıcı

**Port 3389'u concurrent tarayan NetworkScanner implementasyonu.**

**Düzenlenecek Dosyalar:**
- `internal/scanner/scanner.go`
- `internal/scanner/scanner_test.go`

```go
type NetworkScanner struct {
    concurrency int
    timeout     time.Duration
    mu          sync.RWMutex
    hosts       map[string]domain.Host
    cancel      context.CancelFunc
}

func New(concurrency int, timeout time.Duration) *NetworkScanner
// domain.Scanner interface'ini implement eder:
func (s *NetworkScanner) Start(ctx context.Context, cidrs []string) <-chan domain.ScanEvent
func (s *NetworkScanner) Cancel()
func (s *NetworkScanner) Hosts() []domain.Host
```

Fan-out implementasyonu (IMPLEMENTATION.md §2.2 pattern):
- Semaphore channel (concurrency=256 slot)
- Her IP için goroutine: `net.DialTimeout("tcp", ip+":3389", timeout)`
- Başarılı: Host oluştur (reverse DNS 200ms timeout), `EventHostFound` gönder
- Her 10 IP'de: `EventScanProgress` gönder
- Tümü bitince: `EventScanComplete` gönder

**Kabul Kriterleri:**
- [ ] Mock TCP listener'a karşı tarama: host bulunur
- [ ] `Cancel()` sonrası goroutine leak yok
- [ ] 254 IP taraması < 10 sn (loopback test ağıyla)
- [ ] İki kez `Start()` çağrısı: önceki iptal edilir
- [ ] Unit testler geçer

**Bağımlılıklar:** Görev 2, Görev 3
**Efor:** 4 saat

---

## Faz 4: UI ve RDP

> Tam ekran framebuffer UI ve RDP bağlantısı.

---

### Görev 7: Draw Primitifleri ve Renk Sistemi

**UI için temel çizim araçları: dikdörtgen, metin, kenarlık, ilerleme çubuğu.**

**Düzenlenecek Dosyalar:**
- `internal/ui/colors.go`
- `internal/ui/draw.go`
- `internal/ui/fonts.go`

```go
// colors.go — IMPLEMENTATION.md §6.1 renk paleti
var (ColorBG, ColorPanel, ColorAccent, ColorText, ...)

// draw.go
func FillRect(img *image.RGBA, r image.Rectangle, c color.RGBA)
func DrawBorder(img *image.RGBA, r image.Rectangle, c color.RGBA)
func DrawText(img *image.RGBA, x, y int, text string, c color.RGBA)    // 7x13 basicfont
func DrawTextLarge(img *image.RGBA, x, y int, text string, c color.RGBA) // 14x26 (2x scale)
func DrawHLine(img *image.RGBA, x1, x2, y int, c color.RGBA)
func DrawProgressBar(img *image.RGBA, r image.Rectangle, pct float64, fg, bg color.RGBA)
func DrawCursor(img *image.RGBA, x, y int)  // fare imleci (küçük artı veya ok)
func TextWidth(text string, large bool) int  // piksel genişliği hesapla
func DrawInputField(img *image.RGBA, r image.Rectangle, text string, active bool)
// Aktif alan: vurgulanmış kenarlık + metin cursor '_'
```

**Test:** MockFB ile her fonksiyon çağrılır; en az bir piksel rengi doğrulanır.

**Kabul Kriterleri:**
- [ ] `DrawText` 1280x720 boş image'a "SimpleClient" yazar, ilgili bölge boş değil
- [ ] `DrawProgressBar` %0 → tamamen bg, %100 → tamamen fg
- [ ] `DrawInputField` aktif modda kenarlık rengi farklı
- [ ] Unit testler geçer (MockFB)

**Bağımlılıklar:** Görev 4
**Efor:** 4 saat

---

### Görev 8: UI State Machine ve Render

**UIState yapısı, ekran geçişleri ve tüm render fonksiyonları.**

**Düzenlenecek Dosyalar:**
- `internal/ui/state.go`
- `internal/ui/render.go`
- `internal/ui/render_discovery.go`
- `internal/ui/render_modal.go`
- `internal/ui/render_connecting.go`

**`state.go`:**
```go
type Screen int
const (
    ScreenDiscovery Screen = iota
    ScreenModal
    ScreenConnecting
    ScreenSession
)

type ModalState struct {
    Fields    [3]string  // Username, Password, Domain
    FocusIdx  int        // 0=user, 1=pass, 2=domain, 3=connect, 4=cancel
    Error     string
}

type UIState struct {
    Screen       Screen
    Hosts        []domain.Host
    SelectedIdx  int
    ScrollOffset int
    ScanProgress float64
    ScanDone     bool
    Modal        ModalState
    ErrorMsg     string
    MouseX, MouseY int
}

func (s *UIState) Transition(to Screen)
func (s *UIState) SelectedHost() *domain.Host
func (s *UIState) VisibleHosts(maxRows int) []domain.Host
func (s *UIState) HandleScanEvent(ev domain.ScanEvent)
```

**`render_discovery.go`:**
- Üst bar: logo + IP + tarama durumu
- Sunucu listesi: her satır IP, hostname, latency; seçili satır vurgulanmış
- Boş liste: "Taranıyor..." veya "Sunucu bulunamadı" merkezi mesaj
- Alt bar: klavye kısayolları + progress bar
- Fare imleci çizimi

**`render_modal.go`:**
- Yarı-saydam overlay (arka planı karartır)
- Modal kutu ekran merkezinde
- 3 input alanı + 2 buton
- Aktif alana göre vurgulama

**`render_connecting.go`:**
- Discovery ekranı üstünde overlay
- "Bağlanıyor... 192.168.1.50" mesajı + spinner animasyonu

**Kabul Kriterleri:**
- [ ] `renderDiscovery(MockFB, boş state)` → üst bar ve alt bar pikselleri doğrulanır
- [ ] `renderDiscovery(MockFB, 3 hostlu state)` → 3 satır render edilmiş
- [ ] Seçili satır farklı arka plan rengine sahip
- [ ] `renderModal(MockFB, state)` → modal bölgesi non-zero pikseller içeriyor
- [ ] Unit testler geçer (MockFB)

**Bağımlılıklar:** Görev 7
**Efor:** 8 saat

---

### Görev 9: RDP İstemcisi ve Framebuffer Yazıcı

**grdp sarmalayıcı + RDP bitmap güncellemelerini framebuffer'a yaz.**

**Düzenlenecek Dosyalar:**
- `internal/rdp/client.go`
- `internal/rdp/framewriter.go`
- `internal/rdp/client_test.go`

**`client.go`:**
```go
type Credentials struct { Username, Password, Domain string }

type Client struct {
    g      *grdp.Client
    frames chan image.Image  // boyut: 4
    done   chan struct{}
}

func New(addr string, creds Credentials, width, height int) (*Client, error)
// grdp.NewClient + Login() (10s timeout)
// Bitmap callback: c.frames channel'a gönder (non-blocking, eski frame drop)

func (c *Client) Frames() <-chan image.Image
func (c *Client) SendKey(linuxKeycode int, down bool) error
func (c *Client) SendMouse(x, y int, buttons uint16) error
func (c *Client) Close() error
```

**`framewriter.go`:**
```go
type FrameWriter struct {
    fb interface{ Blit(*image.RGBA); Bounds() image.Rectangle }
}

// Write: image.Image → ölçekle → RGBA'ya dönüştür → fb.Blit()
func (fw *FrameWriter) Write(img image.Image)

// scaleToFit: golang.org/x/image/draw ile BiLinear ölçekleme
func scaleToFit(src image.Image, w, h int) *image.RGBA
```

**Kabul Kriterleri:**
- [ ] `scaleToFit(1920x1080 image, 1280, 720)` → 1280x720 RGBA döner
- [ ] `Client.Close()` sonrası goroutine leak yok
- [ ] Frames() channel dolu olduğunda eski frame drop edilir (blocking olmaz)
- [ ] Unit testler geçer (mock grdp mümkün değilse encoder testleri yeterli)

**Bağımlılıklar:** Görev 4
**Efor:** 4 saat

---

### Görev 10: Ana UI Döngüsü ve main.go

**Tüm bileşenleri birleştiren ana döngü ve program girişi.**

**Düzenlenecek Dosyalar:**
- `internal/ui/loop.go`
- `internal/config/config.go`
- `cmd/simpleclient/main.go`

**`config.go`:**
```go
type Config struct {
    FBDevice    string        // varsayılan: /dev/fb0
    KbdDevice   string        // boş = otomatik tespit
    MouseDevice string        // boş = otomatik tespit
    ScanTimeout time.Duration // varsayılan: 500ms
    MaxWorkers  int           // varsayılan: 256
}
func Load() Config  // flag.Parse()
```

**`loop.go`:**
```go
// Run: ana uygulama döngüsü — çıkış yok (kiosk)
func Run(fb FBInterface, input *inputdev.Reader, scan domain.Scanner, cfg config.Config) {
    state := &UIState{}
    backBuf := image.NewRGBA(fb.Bounds())

    // İlk taramayı başlat
    ctx, cancelScan := context.WithCancel(context.Background())
    cidr, _ := network.DetectCIDR()
    scanCh := scan.Start(ctx, []string{cidr})

    ticker := time.NewTicker(16 * time.Millisecond)
    dirty := true

    for {
        select {
        case ev, ok := <-scanCh:
            if ok { state.HandleScanEvent(ev); dirty = true }
        case ev := <-input.Events():
            handleInput(state, ev, scan, cfg, &ctx, &cancelScan)
            dirty = true
        case <-ticker.C:
            if dirty && state.Screen != ScreenSession {
                renderAll(fb, backBuf, state, input)
                dirty = false
            }
        }
    }
}

// handleInput: InputEvent → UIState geçişi
// - EvKey Up/Down: liste gezinme
// - EvKey Enter: Modal aç veya bağlan
// - EvKey Esc: Modal kapat veya bağlantı kes
// - EvKey F5: Yeniden tara
// - EvKey Tab: Modal'da alan değiştir
// - EvKey Backspace: Metin silme
// - EvKey baskı karakteri: Modal alan'ına ekle
// - EvKey Ctrl+Alt+End: Session'dan çık
// - EvMouseMove: state.MouseX/Y güncelle
// - EvMouseButton sol tık: Keşif listesinde satır seç, modal buton tıkla
```

**`main.go`:**
```go
func main() {
    cfg := config.Load()

    fb, err := framebuffer.Open(cfg.FBDevice)
    if err != nil { log.Fatalf("Framebuffer: %v", err) }
    defer fb.Close()

    kbdPath := cfg.KbdDevice
    if kbdPath == "" { kbdPath, _ = inputdev.DetectKeyboard() }
    mousePath := cfg.MouseDevice
    if mousePath == "" { mousePath, _ = inputdev.DetectMouse() }

    input, err := inputdev.New(kbdPath, mousePath, fb.Width, fb.Height)
    if err != nil { log.Fatalf("Input: %v", err) }
    defer input.Close()

    scan := scanner.New(cfg.MaxWorkers, cfg.ScanTimeout)

    ui.Run(fb, input, scan, cfg)
    // ui.Run asla dönmez (kiosk)
}
```

**Kabul Kriterleri:**
- [ ] `go build -o SimpleClient ./cmd/simpleclient` hatasız
- [ ] MockFB + simüle edilmiş input stream ile loop testi: 10 ScanEvent → state.Hosts 10 eleman
- [ ] Enter tuşu → ScreenDiscovery'den ScreenModal'a geçiş
- [ ] Esc → ScreenModal'dan ScreenDiscovery'ye geçiş
- [ ] F5 → tarama yeniden başlar

**Bağımlılıklar:** Görev 6, Görev 8, Görev 9
**Efor:** 6 saat

**🔍 Checkpoint:** Bu noktada `go run ./cmd/simpleclient` QEMU'da çalıştırılabilir. Framebuffer ekranında keşif arayüzü görünmeli. Tarama başlamalı. (Gerçek RDP sunucusu yoksa liste boş olur, bu normal.)

---

### Görev 11: RDP Oturum Entegrasyonu

**UI state ile RDP Client'ı bağla: bağlanma, oturum görüntüleme, bağlantı kesme.**

**Düzenlenecek Dosyalar:**
- `internal/ui/loop.go` — session state + RDP entegrasyonu
- `internal/ui/state.go` — SessionState alanı eklenir

**Eklenmesi gerekenler:**

```go
// state.go'ya ekle
type SessionState struct {
    Host      domain.Host
    Client    *rdp.Client
    Writer    *rdp.FrameWriter
    Connected bool
    Error     string
}

// loop.go'da handleConnect() fonksiyonu:
func handleConnect(state *UIState, fb FBInterface, cfg config.Config) {
    host := state.SelectedHost()
    creds := rdp.Credentials{
        Username: state.Modal.Fields[0],
        Password: state.Modal.Fields[1],
        Domain:   state.Modal.Fields[2],
    }

    state.Transition(ScreenConnecting)
    // render "Bağlanıyor..."

    go func() {
        client, err := rdp.New(host.AddrRDP(), creds, fb.Width, fb.Height)
        if err != nil {
            state.Modal.Error = rdpErrToMessage(err)
            state.Transition(ScreenModal)
            return
        }

        writer := &rdp.FrameWriter{FB: fb}
        state.Session = &SessionState{Host: *host, Client: client, Writer: writer}
        state.Transition(ScreenSession)

        // RDP frame loop (session ekranında normal render atlanır)
        for frame := range client.Frames() {
            writer.Write(frame)
        }
        // Frames() kapandı = bağlantı kesildi
        state.Transition(ScreenDiscovery)
        state.ErrorMsg = "Bağlantı kesildi"
    }()
}

// Ctrl+Alt+End ile bağlantı kesme:
func handleDisconnect(state *UIState) {
    if state.Session != nil && state.Session.Client != nil {
        state.Session.Client.Close()
    }
    state.Session = nil
    state.Transition(ScreenDiscovery)
}
```

**RDP input iletimi** (session aktifken klavye/fare RDP'ye gider, UI'ya değil):
```go
// loop.go handleInput() içinde:
if state.Screen == ScreenSession && state.Session != nil {
    switch ev.Type {
    case inputdev.EvKey:
        // Ctrl+Alt+End: bağlantıyı kes
        if isCtrlAltEnd(ev) { handleDisconnect(state); return }
        state.Session.Client.SendKey(ev.KeyCode, ev.Pressed)
    case inputdev.EvMouseMove:
        state.Session.Client.SendMouse(ev.MouseX, ev.MouseY, 0)
    case inputdev.EvMouseButton:
        state.Session.Client.SendMouse(ev.MouseX, ev.MouseY, mouseFlags(ev))
    }
    dirty = false  // Session'da UI render edilmez; RDP frame direkt fb'ye gider
    return
}
```

**Kabul Kriterleri:**
- [ ] Geçerli kimlik bilgisiyle `handleConnect()` → ScreenSession geçişi
- [ ] RDP frame'leri framebuffer'a yazılır (QEMU + gerçek Windows hedef ile test)
- [ ] Ctrl+Alt+End → ScreenDiscovery'ye dönüş
- [ ] Yanlış kimlik bilgisi → Modal'da hata mesajı, ScreenModal'da kalır
- [ ] Bağlantı zaman aşımı → Modal'da "Hedefe ulaşılamadı"

**Bağımlılıklar:** Görev 9, Görev 10
**Efor:** 5 saat

---

## Faz 5: ISO Build ve Kiosk Kilidi

---

### Görev 12: ISO Build Sistemi

**Docker + Makefile tabanlı önyüklenebilir ISO üretimi.**

**Düzenlenecek Dosyalar:**
- `build/Makefile`
- `build/Dockerfile`
- `build/init`
- `build/kernel.config`
- `build/grub.cfg`

**`build/init`** (kiosk kilidi dahil):
```sh
#!/bin/sh
mount -t proc proc /proc
mount -t sysfs sysfs /sys
mount -t devtmpfs devtmpfs /dev

# Kernel mesajlarını sustur
echo 0 > /proc/sys/kernel/printk

# Ağ
ip link set eth0 up
udhcpc -i eth0 -n -q 2>/dev/null || ip addr add 169.254.100.100/16 dev eth0

# Kiosk döngüsü: çökerse yeniden başlat
while true; do
    /sbin/SimpleClient
    sleep 2
done
# Asla buraya ulaşılmaz
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

`panic=5`: kernel panic olursa 5 saniye sonra yeniden başlar.
`vt.global_cursor_default=0`: terminal cursor gizlenir.

**`build/kernel.config`** — IMPLEMENTATION.md §9 tam konfigürasyonu kullan. Kritikler:
- `CONFIG_FB_VESA=y`, `CONFIG_FB_EFI=y`
- `CONFIG_INPUT_EVDEV=y`
- `CONFIG_SYSRQ=n`, `CONFIG_MAGIC_SYSRQ=n`

**Makefile hedefleri:** `binary`, `initramfs`, `iso`, `all`, `clean`, `docker-build`

**Kabul Kriterleri:**
- [ ] `make binary` → statik binary; `ldd` → "not a dynamic executable"
- [ ] `make initramfs` → cpio.gz üretilir
- [ ] `make iso` → SimpleClient.iso üretilir; `file SimpleClient.iso` → ISO 9660
- [ ] ISO boyutu < 60 MB
- [ ] `file build/rootfs/sbin/SimpleClient` → "ELF 64-bit LSB executable, statically linked"

**Bağımlılıklar:** Görev 11
**Efor:** 6 saat

---

### Görev 13: QEMU Test, Entegrasyon ve README

**QEMU ile görsel doğrulama, CI pipeline, README.**

**Düzenlenecek Dosyalar:**
- `build/test-qemu.sh`
- `.github/workflows/ci.yml`
- `README.md`

**`build/test-qemu.sh`:**
```bash
#!/bin/bash
# QEMU'da ISO'yu başlat, ekranı SDL ile göster
qemu-system-x86_64 \
    -m 256M \
    -boot d \
    -cdrom SimpleClient.iso \
    -display sdl \
    -device e1000,netdev=net0 \
    -netdev user,id=net0 \
    -k tr
```

**CI (`ci.yml`):**
```yaml
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.23' }
      - run: go vet ./...
      - run: go test ./...
      - run: CGO_ENABLED=0 GOOS=linux go build -ldflags '-s -w' ./cmd/simpleclient
      - run: file SimpleClient | grep "statically linked"
```

**Kabul Kriterleri:**
- [ ] `go test ./...` → 0 hata
- [ ] `go vet ./...` → 0 uyarı
- [ ] Statik binary doğrulandı
- [ ] QEMU'da çalıştırıldığında framebuffer ekranında UI görünür (manuel doğrulama)
- [ ] README kurulum adımları çalışıyor

**Bağımlılıklar:** Görev 12
**Efor:** 3 saat

---

## Milestones

| Milestone | Görev Sonrası | Ne Elde Edildi | Demo? |
|-----------|--------------|----------------|-------|
| Temel Altyapı | Görev 3 | Derlenir, ağ tespiti çalışır | Birim test |
| Framebuffer + Input | Görev 5 | Ekrana piksel yazılır, girdiler okunur | MockFB test |
| Çalışan Tarayıcı | Görev 6 | Ağ taraması çalışır | CLI test |
| **UI MVP** | Görev 10 | QEMU'da tam ekran arayüz görünür | QEMU SDL |
| **RDP + Kiosk** | Görev 11 | Gerçek RDP bağlantısı çalışır | QEMU + Win |
| **ISO** | Görev 12 | USB'ye yazılabilir ISO | Gerçek boot |
| **Release** | Görev 13 | Test edilmiş, CI'da | Ship it |

---

## Bağımlılık Grafiği

```
[G1] → [G2] → [G3] → [G6] ──────────────────→ [G10] → [G11] → [G12] → [G13]
        ↓                                         ↑        ↑
       [G4] → [G7] → [G8] ──────────────────────→┘        │
               ↓                                           │
              [G5] ──────────────────────────────→ [G10]   │
               ↓                                           │
              [G9] ──────────────────────────────→ [G10]   │
                                                   ↓       │
                                                  [G11] ───┘
```
