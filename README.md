# SimpleClient

Minimal kiosk işletim sistemi. Boot açıldığında ağdaki RDP sunucularını tarar, tam ekran listeler ve tek tık ile RDP bağlantısı kurar. Başka hiçbir şey yapamaz.

## Özellikler

- Sıfır harici bağımlılık (tek statik binary)
- `/dev/fb0` tabanlı tam ekran UI (X11/Wayland yok)
- Evdev ile klavye + fare desteği
- 256 eş zamanlı worker ile port 3389 taraması
- grdp ile saf-Go RDP istemcisi
- BIOS + UEFI boot destekli ISO

## Hızlı Başlangıç

### Gereksinimler

- Go 1.23+
- Docker (ISO build için)
- QEMU (test için, isteğe bağlı)

### Binary Build

```bash
make binary
```

`dist/SimpleClient` statik binary oluşur.

### Tam ISO Build (Docker ile)

```bash
make docker-build
```

`dist/SimpleClient.iso` dosyası oluşur (< 60 MB).

### QEMU ile Test

```bash
make qemu
```

### USB'ye Yazma

```bash
# /dev/sdX = USB diskiniz (dikkatli olun!)
sudo dd if=dist/SimpleClient.iso of=/dev/sdX bs=4M status=progress conv=fsync
```

## Kullanım

Sistem boot olduktan sonra:

| Tuş | Eylem |
|-----|-------|
| ↑ ↓ | Sunucu listesinde gezin |
| Enter | Seçili sunucuya bağlan (credential modal) |
| F5 | Ağı yeniden tara |
| Esc | Modal'ı kapat |
| Tab | Modal'da alan değiştir |
| Ctrl+Alt+End | Aktif RDP oturumunu kes |

Fare desteği: Tıklayarak sunucu seçin, bağlan/iptal düğmelerine tıklayın.

## Proje Yapısı

```
cmd/simpleclient/          — Program girişi
internal/
  config/             — Flag-tabanlı yapılandırma
  domain/             — Host entity, ScanEvent, Scanner interface
  framebuffer/        — /dev/fb0 sürücüsü + MockFB
  inputdev/           — evdev klavye/fare okuyucu
  network/            — Ağ arayüzü / CIDR tespiti
  scanner/            — Eş zamanlı port 3389 tarayıcısı
  ui/                 — Framebuffer UI (state machine + render)
  rdp/                — grdp sarmalayıcı + frame yazıcı
build/
  Dockerfile          — Çok aşamalı build (Go + kernel + initramfs + ISO)
  Makefile            — Build hedefleri
  init                — Kiosk init script (BusyBox)
  kernel.config       — Minimal Linux kernel config
  grub.cfg            — GRUB bootloader config
  test-qemu.sh        — QEMU test yardımcısı
```

## Testler

```bash
make vet   # go vet
make test  # go test ./...
```

## Mimari Notlar

- **Ekran çıktısı**: `mmap` ile `/dev/fb0`'a doğrudan piksel yazma; X11/Wayland yok.
- **Girdi**: `/dev/input/event*` evdev aygıtlarından `binary.Read` ile Linux `input_event` struct okuma.
- **Render döngüsü**: 16ms ticker (~60 FPS), dirty flag ile gereksiz yeniden çizim önleme.
- **Tarayıcı**: 256 goroutine havuzu, `net.DialTimeout("tcp", ip+":3389", 500ms)`.
- **RDP**: `github.com/tomatome/grdp` saf-Go implementasyonu; bitmap callback → `frames` channel.
- **Kiosk kilidi**: Init script `while true` döngüsü; `CONFIG_MODULES=n`, `CONFIG_SYSRQ=n`.
