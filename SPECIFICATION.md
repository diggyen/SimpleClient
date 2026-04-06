# rdpboot — Specification

> Bağımlılıksız önyüklenebilir RDP kiosk sistemi: açılır, ağı tarar, seçilir, bağlanılır — başka hiçbir şey yapılamaz.

---

## 1. Overview

### 1.1 Nedir?

rdpboot, USB belleğe yazılan ve herhangi bir x86_64 bilgisayarı önyükleyen minimal bir Linux ISO'sudur (~50 MB). Önyükleme tamamlandığında ekran tamamen bu sisteme aittir: tek bir statik derlenmiş Go binary'si Linux framebuffer'ına (`/dev/fb0`) doğrudan piksel yazarak tam ekran grafiksel bir arayüz sunar. Kullanıcı klavye veya fare ile ağdaki RDP sunucularını listeleyen bu arayüzden bir hedef seçer, kimlik bilgilerini girer ve uzak masaüstü oturumu aynı ekranda açılır.

Sistem bir kiosk olarak çalışır: shell erişimi yoktur, başka uygulama çalışmaz, kullanıcı bu arayüzün dışına çıkamaz. X11, Wayland, masaüstü yöneticisi, web tarayıcısı veya herhangi bir grafik kütüphanesi bulunmaz. Tüm görsel çıktı saf Go kodu tarafından framebuffer'a yazılan piksellerden oluşur.

### 1.2 Hedef Kitle

- BT yöneticileri: ağdaki Windows makinelere hızla bağlanmak isteyen; kurulum gerektirmeyen, taşınabilir bir RDP istemcisine ihtiyaç duyanlar
- Sistem yöneticileri: bozuk bir işletim sistemi olan makinelere harici ortamdan bağlanmak isteyenler
- Güç kullanıcıları: özel, kilitli bir RDP erişim noktası oluşturmak isteyenler

### 1.3 Temel Farklılaştırıcılar

- **Tam kiosk modu**: Sistem sadece bu işi yapar; shell, dosya sistemi veya ağ yapılandırması erişimi yok
- **Sıfır grafik bağımlılığı**: X11, Wayland, SDL, Qt, GTK yok — sadece `/dev/fb0` ve `/dev/input/*`
- **Tek binary**: Tarama + UI render + RDP istemcisi tek bir statik Go binary'sinde
- **~50 MB ISO**: Kernel + BusyBox + binary; başka hiçbir şey yok
- **Saf Go RDP**: Sistem RDP binary'si gerekmez

### 1.4 Rekabet Ortamı

| Özellik | rdpboot | Tails + rdesktop | WinPE + mstsc | Kali Live |
|---------|---------|-----------------|---------------|-----------|
| ISO boyutu | ~50 MB | ~1.2 GB | ~500 MB | ~3 GB |
| Kiosk modu (kilitli) | ✅ | ❌ | ❌ | ❌ |
| Grafik bağımlılık yok | ✅ | ❌ | ❌ | ❌ |
| Otomatik tarama | ✅ | ❌ | ❌ | ❌ |
| Saf Go RDP | ✅ | ❌ | ❌ | ❌ |

---

## 2. Temel Kavramlar

| Kavram | Tanım |
|--------|-------|
| **Framebuffer (`/dev/fb0`)** | Linux'un doğrudan ekrana piksel yazılmasını sağlayan sanal cihaz; X11 gerektirmez |
| **evdev** | Linux'un klavye ve fare girdisini `/dev/input/event*` üzerinden sunan standart arayüzü |
| **Kiosk modu** | Kullanıcının uygulamanın dışına çıkamadığı kilitli çalışma modu |
| **Statik binary** | `CGO_ENABLED=0` ile derlenen, hiçbir paylaşımlı kütüphane bağımlılığı olmayan Go binary'si |
| **RDP (Remote Desktop Protocol)** | Microsoft'un uzak masaüstü protokolü; varsayılan port 3389 |
| **Subnet tarama** | Ağ arayüzünün CIDR bloğundaki tüm IP'lerde port 3389'u kontrol etme |
| **Hedef** | Ağda keşfedilen, port 3389 açık olan bir makine |
| **Oturum** | Aktif bir RDP bağlantısı; ekranın tamamını kaplar |
| **Bitmap güncelleme** | RDP sunucusunun gönderdiği ekran bölgesi güncelleme paketi |

---

## 3. Fonksiyonel Gereksinimler

### 3.1 Önyükleme ve Başlatma

#### 3.1.1 Sistem Başlatma

**Kullanıcı Hikayesi:** USB'yi takıp bilgisayarı başlattığımda, sistem otomatik olarak ağ yapılandırmasını almalı ve RDP tarama arayüzünü ekranda göstermeli.

**Açıklama:** init betiği sırayla şunu yapar: sanal dosya sistemlerini mount eder, DHCP ile IP alır, rdpboot binary'sini başlatır. Binary başladığında framebuffer'ı açar, tam ekran keşif arayüzünü render eder ve arka planda subnet taramasını başlatır.

**Kabul Kriterleri:**
- [ ] USB önyüklemesinden arayüzün görünmesine kadar geçen süre 15 saniyenin altında
- [ ] Ekranda sistem başlığı, yerel IP adresi ve tarama durumu görünür
- [ ] DHCP başarısız olursa link-local adres atanır ve ekranda gösterilir
- [ ] Başlatma sırasında herhangi bir terminal/shell çıktısı ekranda görünmez (çekirdek mesajları susturulur)

**Kenar Durumları:**
- Ağ arayüzü bulunamazsa ekranda "Ağ bulunamadı" mesajı gösterilir ve yeniden deneme döngüsü başlar
- Birden fazla aktif arayüz varsa ilk bulunana DHCP uygulanır

---

### 3.2 Keşif Ekranı

#### 3.2.1 Ana Arayüz — Server Listesi

**Kullanıcı Hikayesi:** Sistem başladığında ağdaki RDP sunucularının listesini görmek istiyorum; klavye veya fare ile seçim yapabilmeliyim.

**Açıklama:** Ekran üç bölümden oluşur:

**Üst bar**: `rdpboot` başlığı | Yerel IP adresi | Tarama durumu

**Orta alan — Sunucu listesi:**
```
┌──────────────────────────────────────────────────┐
│  ▶  192.168.1.50   WIN-SERVER01      12 ms       │  ← seçili (vurgulanmış)
│     192.168.1.82   DESKTOP-PC02      8 ms        │
│     192.168.1.101  bilinmiyor        45 ms       │
│     192.168.1.200  WIN-DC01          3 ms        │
└──────────────────────────────────────────────────┘
```

**Alt bar**: `↑↓ Gezin  ENTER Bağlan  F5 Yeniden Tara  ESC Çıkış`

Tarama devam ederken bulunan sunucular gerçek zamanlı olarak listeye eklenir. Liste görünür alandan büyükse kaydırılabilir.

**Kabul Kriterleri:**
- [ ] Arayüz framebuffer açıldığında render edilir (X11 veya herhangi bir grafik kütüphanesi olmadan)
- [ ] Bulunan her sunucu listeye anlık eklenir (tarama devam ederken)
- [ ] Üst/Alt ok tuşları listede gezinir; seçili satır vurgulanır
- [ ] Fare ile liste satırına tıklamak o sunucuyu seçer ve vurgular
- [ ] F5 tuşu veya ekran butonu yeni tarama başlatır (liste sıfırlanır)
- [ ] Liste: IP, hostname (varsa), gecikme (ms) gösterir
- [ ] Alt tarama durumu çubuğu: ilerleme yüzdesi veya "Tamamlandı — N sunucu"
- [ ] Liste boşsa "Taranıyor... / Sunucu bulunamadı" mesajı gösterilir

**Kenar Durumları:**
- Çok fazla sunucu: liste kaydırılabilir (PageUp/PageDown desteklenir)
- Hostname çok uzunsa kırpılır ve `...` eklenir

#### 3.2.2 Yeniden Tarama

**Açıklama:** F5 veya "Yeniden Tara" tıklaması mevcut taramayı iptal eder, listeyi temizler ve yeni tarama başlatır. Ek CIDR girilemez (kiosk tasarımı); sadece mevcut subnet otomatik taranır.

---

### 3.3 Kimlik Bilgisi Girişi

#### 3.3.1 Bağlantı İletişim Kutusu

**Kullanıcı Hikayesi:** Bir sunucuya Enter tuşu veya fare tıklaması ile seçtiğimde, kullanıcı adı ve şifremi girebileceğim bir diyalog kutusu açılmalı.

**Açıklama:** Sunucu seçildiğinde ekranın üzerine bir modal diyalog açılır:

```
┌─────────────────────────────────┐
│  192.168.1.50 — WIN-SERVER01    │
│                                 │
│  Kullanıcı Adı: [_____________] │
│  Şifre:         [*************] │
│  Domain:        [_____________] │
│                                 │
│      [ Bağlan ]  [ İptal ]      │
└─────────────────────────────────┘
```

Metin alanları klavye ile doldurulur. Tab ile alanlar arasında geçilir. Enter ile "Bağlan" tetiklenir. Şifre alanında karakterler `*` olarak gösterilir.

**Kabul Kriterleri:**
- [ ] Modal ekranın merkezinde render edilir
- [ ] Tab tuşu alanlar arasında döngüsel geçiş yapar (KullanıcıAdı → Şifre → Domain → Bağlan)
- [ ] Şifre karakterleri maskeli gösterilir
- [ ] Enter tuşu odaklanan butonu tetikler
- [ ] ESC veya "İptal" diyalogu kapatır ve sunucu listesine döner
- [ ] Bağlantı denenirken butonlar devre dışı kalır ve "Bağlanıyor..." gösterilir
- [ ] Kimlik bilgisi hatası: diyalogda kırmızı hata mesajı gösterilir, diyalog açık kalır
- [ ] Bağlantı zaman aşımı: hata mesajı gösterilir

---

### 3.4 RDP Oturumu

#### 3.4.1 Tam Ekran RDP Görüntüsü

**Kullanıcı Hikayesi:** Bağlandıktan sonra uzak masaüstü ekranın tamamını kaplamalı ve klavye ile fareyle etkileşim kurabilmeliyim.

**Açıklama:** Bağlantı kurulduğunda uzak masaüstü framebuffer'ın tamamını kaplar. Hiçbir kenar çubuğu veya araç çubuğu görünmez (tam immersif mod). Tüm klavye ve fare girdileri RDP sunucusuna iletilir. Tek istisna: bağlantıyı kesmek için özel tuş kombinasyonu.

**Kabul Kriterleri:**
- [ ] RDP bitmap güncellemeleri framebuffer'a gerçek zamanlı yazılır (≥10 FPS)
- [ ] Tüm klavye tuşları (alfanümerik, fonksiyon, özel) RDP sunucusuna iletilir
- [ ] Fare hareketi, sol/sağ tıklama, kaydırma RDP sunucusuna iletilir
- [ ] **Ctrl+Alt+End** kombinasyonu bağlantıyı keser ve sunucu listesine döner
- [ ] Bağlantı kesilirse "Bağlantı kesildi" mesajı gösterilir, 2 saniye sonra liste ekranına dönülür
- [ ] RDP kimlik bilgisi hatalıysa uygun hata mesajı gösterilir
- [ ] Ekran boyutu: framebuffer çözünürlüğünden otomatik alınır

**Kenar Durumları:**
- RDP sunucusu oturumu kapatırsa: liste ekranına dön
- Ağ bağlantısı kesilirse: hata mesajı göster, 3 saniye sonra liste ekranına dön

---

### 3.5 Kiosk Kısıtlamaları

**Açıklama:** Sistem gerçek bir kiosk olarak çalışır:

- init betiği shell'e düşmez; binary sonlandıysa yeniden başlatır
- Ctrl+C, Ctrl+Z, Ctrl+Alt+F2 (sanal terminal geçişi) devre dışı bırakılır
- SysRq tuşu kernel konfigürasyonunda devre dışı
- Binary'nin yakalayamadığı panik durumunda sistem otomatik yeniden başlar (kernel `panic=5` parametresi)

**Kabul Kriterleri:**
- [ ] Kullanıcı keyfi bir shell'e erişemez
- [ ] Binary çökerse init 2 saniye içinde yeniden başlatır
- [ ] Ctrl+Alt+Fn sanal terminal geçişi çalışmaz

---

## 4. Mimari Genel Bakış

### 4.1 Sistem Bileşenleri

```
┌─────────────────────────────────────────────────────┐
│                  Bootable ISO (~50 MB)              │
│  ┌────────────────────────────────────────────┐    │
│  │  Linux Kernel (minimal, ~5 MB)             │    │
│  │  BusyBox initramfs (~2 MB)                 │    │
│  │  rdpboot binary (statik Go, ~20 MB)        │    │
│  └────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────┘
         │ önyükleme
         ▼
┌─────────────────────────────────────────────────────┐
│              rdpboot binary                         │
│                                                     │
│  ┌──────────────┐   ┌──────────────────────────┐   │
│  │ Ağ Tarayıcı  │   │ Framebuffer Renderer      │   │
│  │ (goroutine   │──▶│ /dev/fb0 direkt yazım     │   │
│  │  pool)       │   └──────────────────────────┘   │
│  └──────────────┘              ▲                    │
│                                │                    │
│  ┌──────────────┐   ┌──────────┴───────────────┐   │
│  │ Input Handler│──▶│ UI State Machine          │   │
│  │ /dev/input/* │   │ (Discovery/Modal/Session) │   │
│  └──────────────┘   └──────────┬───────────────┘   │
│                                │                    │
│  ┌─────────────────────────────▼───────────────┐   │
│  │         Saf Go RDP İstemcisi                │   │
│  │         (tomatome/grdp tabanlı)             │   │
│  └─────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────┘
         │               │
    /dev/fb0         /dev/input/*
    (ekran çıktı)    (klavye/fare girdi)
```

### 4.2 Bileşen Etkileşimleri

1. **Önyükleme**: Kernel → BusyBox init → DHCP → rdpboot binary
2. **Tarama**: Scanner goroutine pool → port 3389 → sonuçlar UI state'e gönderilir → framebuffer'a render edilir
3. **Girdi**: evdev event'leri → Input Handler → UI State Machine → state değişimi → yeniden render
4. **Bağlantı**: UI State Machine → RDP Client → TCP bağlantısı → bitmap güncellemeleri → framebuffer
5. **Kiosk döngüsü**: Binary sonsuz döngüde çalışır; çıkış yolu yoktur

### 4.3 Ekran Durumları (State Machine)

```
[Boot] → [Discovery] ⇄ [Modal] → [Connecting] → [Session]
             ↑                                        │
             └────────────── Disconnect ──────────────┘
```

---

## 5. Veri Modeli

### 5.1 Çalışma Zamanı Varlıkları (bellek içi)

#### Host

| Alan | Tür | Açıklama |
|------|-----|----------|
| IP | `net.IP` | Hedef IP adresi |
| Hostname | `string` | Reverse DNS (boş olabilir) |
| LatencyMs | `int64` | Port bağlantı gecikmesi |
| DiscoveredAt | `time.Time` | Keşif zamanı |

#### UIState

| Alan | Tür | Açıklama |
|------|-----|----------|
| Screen | `ScreenType` | `Discovery` / `Modal` / `Connecting` / `Session` |
| Hosts | `[]Host` | Keşfedilen sunucular |
| SelectedIdx | `int` | Listede seçili satır indeksi |
| ScrollOffset | `int` | Liste kaydırma pozisyonu |
| ScanProgress | `float64` | 0.0–1.0 |
| ModalInput | `ModalFields` | Kullanıcı adı, şifre, domain, odak |
| ErrorMsg | `string` | Gösterilecek hata (boş = yok) |

#### Session

| Alan | Tür | Açıklama |
|------|-----|----------|
| Target | `Host` | Bağlı hedef |
| RDPClient | `*rdp.Client` | Aktif bağlantı |
| State | `SessionState` | Connecting / Active / Disconnected |

---

## 6. Güvenlik Modeli

### 6.1 Kiosk Kilidi

- Binary çıkmaz; tüm hataları içeride yakalar ve arayüzde gösterir
- init betiği binary'yi `while true; do /sbin/rdpboot; sleep 2; done` döngüsünde çalıştırır
- Kernel: `console=tty0 quiet loglevel=0 sysrq_always_enabled=0`
- Sanal terminal geçişi: kernel config'de `CONFIG_VT_CONSOLE_SLEEP=n`, init'te `kbd_mode -a` ile raw mode

### 6.2 Veri Koruma

- Kimlik bilgileri bellek içinde tutulur, diske yazılmaz (read-only ISO)
- Kimlik bilgileri log'a yazılmaz

---

## 7. Dağıtım Modeli

### 7.1 Hedef Ortam

x86_64, BIOS veya UEFI, minimum 128 MB RAM, Ethernet adaptörü.

### 7.2 Dağıtım Yöntemi

Tek ISO → `dd if=rdpboot.iso of=/dev/sdX bs=4M` veya Balena Etcher ile USB'ye yaz.

### 7.3 Sistem Gereksinimleri

| Kaynak | Minimum |
|--------|---------|
| CPU | x86_64, 1 çekirdek |
| RAM | 128 MB |
| Ağ | Ethernet (kablolu) |
| Ekran | VESA framebuffer destekli herhangi bir monitör |

---

## 8. Performans Gereksinimleri

| Metrik | Hedef |
|--------|-------|
| Önyükleme → arayüz görünür | < 10 saniye |
| /24 subnet tarama süresi | < 10 saniye |
| Framebuffer render hızı | ≥ 30 FPS (idle UI), ≥ 10 FPS (RDP session) |
| Binary boyutu | < 25 MB |
| ISO boyutu | < 60 MB |
| Bellek kullanımı | < 64 MB (RDP oturumu dahil) |

---

## 9. Kapsam Dışı

- **Web arayüzü veya HTTP sunucu**: Yok — sadece yerel framebuffer
- **Wi-Fi desteği**: Yalnızca kablolu Ethernet (v1.0)
- **Ses aktarımı**: RDP ses kanalı yok
- **Pano paylaşımı**: Uzak/yerel pano senkronizasyonu yok
- **Dosya transferi**: Disk yönlendirme yok
- **Eşzamanlı çoklu oturum**: Bir seferde tek bağlantı
- **Kalıcı ayarlar**: Tercihler kaydedilmez (read-only ISO)
- **IPv6 tarama**: Yalnızca IPv4
- **ARM desteği**: Yalnızca x86_64 (v1.0)
- **Manuel CIDR girişi**: Kiosk modunda yok; sadece otomatik subnet tarama
- **RDP Gateway / RemoteApp**: Yalnızca tam masaüstü oturumu

---

## 10. Gelecek Planlar

- **v1.1**: Wi-Fi desteği (kernel Wi-Fi sürücüleri + SSID/şifre girişi arayüze eklenir)
- **v1.2**: Pano paylaşımı
- **v1.3**: Birden fazla monitör desteği
- **v2.0**: ARM64 desteği (Raspberry Pi)
