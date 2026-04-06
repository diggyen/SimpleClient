#!/usr/bin/env bash
# rdpboot/setup.sh — Proje kurulum, bağımlılık, test ve binary build scripti.
# Kullanım: bash setup.sh [--iso] [--qemu]
#   --iso   : Docker ile tam ISO build (Docker gerektirir)
#   --qemu  : ISO build sonrası QEMU ile başlat

set -euo pipefail
BOLD='\033[1m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

log()  { echo -e "${BOLD}[rdpboot]${NC} $*"; }
ok()   { echo -e "${GREEN}✓${NC} $*"; }
warn() { echo -e "${YELLOW}⚠${NC} $*"; }
fail() { echo -e "${RED}✗${NC} $*"; exit 1; }

BUILD_ISO=0
RUN_QEMU=0
for arg in "$@"; do
  case "$arg" in
    --iso)  BUILD_ISO=1 ;;
    --qemu) RUN_QEMU=1 ;;
  esac
done

# ── Ortam kontrolleri ─────────────────────────────────────────────────────────
log "Ortam kontrol ediliyor..."

if ! command -v go &>/dev/null; then
  fail "Go bulunamadı. https://go.dev/dl adresinden Go 1.23+ kurun."
fi

GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
NEEDED="1.23"
if [[ "$(printf '%s\n' "$NEEDED" "$GO_VERSION" | sort -V | head -n1)" != "$NEEDED" ]]; then
  fail "Go $GO_VERSION bulundu ama $NEEDED+ gerekli."
fi
ok "Go $GO_VERSION"

# ── Bağımlılıklar ─────────────────────────────────────────────────────────────
log "Bağımlılıklar indiriliyor (go mod tidy)..."
go mod tidy
ok "go.sum güncellendi"

# ── Vendor + upstream grdp cliprdr yaması ─────────────────────────────────────
# grdp/plugin/cliprdr/cliprdr.go, Windows'a özgü semboller (EmptyClipboard, vb.)
# kullanır ama //go:build windows etiketi eksik; bu upstream bir hata.
# Çözüm: bağımlılıkları vendor altına al ve söz konusu dosyaya build tag ekle.
log "Bağımlılıklar vendor altına alınıyor..."
go mod vendor
ok "vendor/ oluşturuldu"

CLIPRDR_DIR="vendor/github.com/tomatome/grdp/plugin/cliprdr"
if [[ -d "$CLIPRDR_DIR" ]]; then
  log "grdp cliprdr yaması uygulanıyor (upstream Linux build tag hatası)..."

  # Paketteki tüm .go dosyalarına //go:build windows ekle (henüz yoksa).
  for f in "${CLIPRDR_DIR}"/*.go; do
    [[ -f "$f" ]] || continue
    if ! head -3 "$f" | grep -q "go:build"; then
      TMP=$(mktemp)
      printf '//go:build windows\n// +build windows\n\n' | cat - "$f" > "$TMP"
      mv "$TMP" "$f"
    fi
  done

  # Linux'ta paket derlenmeli ama hiçbir şey yapmamalı.
  # t125/mcs.go, ChannelName ve ChannelOption sabitlerini kullanıyor; bunları stub'a ekle.
  cat > "${CLIPRDR_DIR}/stub_notwindows.go" << 'EOF'
//go:build !windows
// +build !windows

// Package cliprdr provides clipboard redirect (Windows-only; no-op on Linux).
// ChannelName and ChannelOption are exported so protocol/t125 can compile on Linux,
// but clipboard redirect is never actually activated in the kiosk.
package cliprdr

const (
	// ChannelName is the MS-RDPBCGR virtual channel name for clipboard redirect.
	ChannelName = "cliprdr"
	// ChannelOption is the channel initialisation flags (CHANNEL_OPTION_INITIALIZED |
	// CHANNEL_OPTION_ENCRYPT_RDP | CHANNEL_OPTION_COMPRESS_RDP).
	ChannelOption = uint32(0xC0000000)
)
EOF

  ok "cliprdr yaması uygulandı"
fi

# ── Vet ──────────────────────────────────────────────────────────────────────
log "go vet çalıştırılıyor..."
go vet -mod=vendor ./...
ok "go vet: uyarı yok"

# ── Testler ───────────────────────────────────────────────────────────────────
log "Testler çalıştırılıyor..."
go test -mod=vendor ./... -v -timeout 120s 2>&1 | tee /tmp/rdpboot_test.log
TEST_EXIT=${PIPESTATUS[0]}

if [[ $TEST_EXIT -ne 0 ]]; then
  fail "Bazı testler başarısız. Detay: /tmp/rdpboot_test.log"
fi
ok "Tüm testler geçti"

# ── Binary build ──────────────────────────────────────────────────────────────
log "Statik binary derleniyor..."
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -mod=vendor -ldflags '-s -w' -o dist/rdpboot ./cmd/rdpboot

if file dist/rdpboot | grep -q "statically linked"; then
  ok "dist/rdpboot: statik binary"
else
  warn "Binary oluşturuldu ama statik bağlı olmayabilir (cross-compile değilse normal)"
fi

echo ""
echo -e "${GREEN}${BOLD}✓ Temel kurulum tamamlandı!${NC}"
echo "  Binary: dist/rdpboot"
echo ""

# ── ISO Build (Docker) ───────────────────────────────────────────────────────
if [[ $BUILD_ISO -eq 1 ]]; then
  log "ISO build başlıyor (Docker gerekli)..."

  if ! command -v docker &>/dev/null; then
    fail "Docker bulunamadı. https://docs.docker.com/get-docker/ adresinden kurun."
  fi

  docker build \
    --target export \
    --output "type=local,dest=dist" \
    -f build/Dockerfile \
    .

  if [[ -f dist/rdpboot.iso ]]; then
    ISO_SIZE=$(du -sh dist/rdpboot.iso | cut -f1)
    ok "dist/rdpboot.iso oluşturuldu ($ISO_SIZE)"
    file dist/rdpboot.iso
  else
    fail "ISO oluşturulamadı."
  fi
fi

# ── QEMU test ─────────────────────────────────────────────────────────────────
if [[ $RUN_QEMU -eq 1 ]]; then
  if [[ ! -f dist/rdpboot.iso ]]; then
    fail "ISO bulunamadı. Önce --iso ile build edin."
  fi
  log "QEMU başlatılıyor..."
  bash build/test-qemu.sh
fi

echo ""
echo -e "${BOLD}Sonraki adımlar:${NC}"
echo "  1. USB'ye yaz : sudo dd if=dist/rdpboot.iso of=/dev/sdX bs=4M status=progress conv=fsync"
echo "  2. QEMU test  : bash setup.sh --iso --qemu"
echo "  3. CI         : .github/workflows/ci.yml otomatik çalışır"
