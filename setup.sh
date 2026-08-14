#!/usr/bin/env bash
# setup.sh — SimpleClient build, test and packaging helper.
#
# Kullanım / Usage: bash setup.sh [--iso] [--qemu] [--headless]
#   --iso       Docker ile bootable ISO üret (Docker gerektirir)
#   --qemu      ISO'yu QEMU'da aç (pencereli)
#   --headless  ISO'yu QEMU'da açıp ekran görüntüsü al, sonra kapat
#
# vendor/ depoda commit'li ve yamalı olduğu için ayrıca bağımlılık indirmeye
# gerek yok — bkz. build/patches/ ve build/vendor-patch.sh.

set -euo pipefail

cd "$(dirname "$0")"

BOLD='\033[1m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

log()  { echo -e "${BOLD}[simpleclient]${NC} $*"; }
ok()   { echo -e "${GREEN}✓${NC} $*"; }
warn() { echo -e "${YELLOW}⚠${NC} $*"; }
fail() { echo -e "${RED}✗${NC} $*"; exit 1; }

BUILD_ISO=0
RUN_QEMU=0
HEADLESS=0
for arg in "$@"; do
  case "$arg" in
    --iso)      BUILD_ISO=1 ;;
    --qemu)     RUN_QEMU=1 ;;
    --headless) BUILD_ISO=1; HEADLESS=1 ;;
    *) fail "bilinmeyen seçenek: $arg" ;;
  esac
done

# ── Ortam kontrolleri ─────────────────────────────────────────────────────────
log "Ortam kontrol ediliyor..."

command -v go &>/dev/null || fail "Go bulunamadı. https://go.dev/dl adresinden Go 1.23+ kurun."

GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
NEEDED="1.23"
if [[ "$(printf '%s\n' "$NEEDED" "$GO_VERSION" | sort -V | head -n1)" != "$NEEDED" ]]; then
  fail "Go $GO_VERSION bulundu ama $NEEDED+ gerekli."
fi
ok "Go $GO_VERSION"

[[ -d vendor ]] || fail "vendor/ yok. 'bash build/vendor-patch.sh' çalıştırın."
ok "vendor/ mevcut"

# ── Vet ───────────────────────────────────────────────────────────────────────
log "go vet çalıştırılıyor..."
go vet -mod=vendor ./...
ok "go vet: uyarı yok"

# ── Testler ───────────────────────────────────────────────────────────────────
log "Testler çalıştırılıyor (-race)..."
go test -mod=vendor -race ./... -timeout 120s
ok "Tüm testler geçti"

# ── Binary build ──────────────────────────────────────────────────────────────
log "Statik binary derleniyor (linux/amd64)..."
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -mod=vendor -ldflags '-s -w' -o dist/simpleclient ./cmd/simpleclient

if file dist/simpleclient | grep -q "statically linked"; then
  ok "dist/simpleclient: statik binary ($(du -h dist/simpleclient | cut -f1))"
else
  fail "Binary statik değil — CGO_ENABLED=0 ile derlenmemiş olabilir."
fi

echo ""
echo -e "${GREEN}${BOLD}✓ Derleme ve testler tamam.${NC}"
echo ""

# ── ISO Build (Docker) ────────────────────────────────────────────────────────
if [[ $BUILD_ISO -eq 1 ]]; then
  log "ISO build başlıyor (Docker gerekli)..."
  command -v docker &>/dev/null || fail "Docker bulunamadı. https://docs.docker.com/get-docker/"
  docker info &>/dev/null || fail "Docker daemon çalışmıyor. Docker Desktop'ı başlatın."

  docker build \
    --target export \
    --output "type=local,dest=dist" \
    -f build/Dockerfile \
    .

  [[ -f dist/simpleclient.iso ]] || fail "ISO oluşturulamadı."
  ok "dist/simpleclient.iso ($(du -h dist/simpleclient.iso | cut -f1))"
fi

# ── QEMU ──────────────────────────────────────────────────────────────────────
if [[ $HEADLESS -eq 1 ]]; then
  log "QEMU headless boot + ekran görüntüsü..."
  bash build/test-qemu.sh --headless --fake-rdp
elif [[ $RUN_QEMU -eq 1 ]]; then
  [[ -f dist/simpleclient.iso ]] || fail "ISO bulunamadı. Önce --iso ile build edin."
  log "QEMU başlatılıyor..."
  bash build/test-qemu.sh
fi

echo ""
echo -e "${BOLD}Sonraki adımlar / Next steps:${NC}"
echo "  1. USB'ye yaz : sudo dd if=dist/simpleclient.iso of=/dev/diskX bs=4m status=progress"
echo "  2. QEMU test  : bash setup.sh --iso --qemu"
echo "  3. Release    : git tag v0.1.0 && git push --tags  (GitHub Actions ISO'yu yükler)"
