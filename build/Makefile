## simpleclient — Build System
## Targets: binary, initramfs, iso, all, clean, docker-build, test, qemu

.PHONY: all binary initramfs iso clean docker-build test qemu

BINARY     := simpleclient
OUT_DIR    := dist
DOCKER_IMG := simpleclient-builder

GO         := go
GOOS       := linux
GOARCH     := amd64
CGO_ENABLED := 0
LDFLAGS    := -s -w -extldflags '-static'

all: binary initramfs iso

## ── Go binary ────────────────────────────────────────────────────────────────
binary:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
	$(GO) build -ldflags '$(LDFLAGS)' -o $(OUT_DIR)/$(BINARY) ./cmd/simpleclient
	@echo "Built: $(OUT_DIR)/$(BINARY)"
	@file $(OUT_DIR)/$(BINARY)

## ── Tests ────────────────────────────────────────────────────────────────────
test:
	$(GO) test ./... -v -timeout 120s

vet:
	$(GO) vet ./...

## ── Initramfs ────────────────────────────────────────────────────────────────
# Requires: cpio, gzip, busybox-static (or equivalent)
initramfs: binary
	@mkdir -p $(OUT_DIR)/rootfs/sbin $(OUT_DIR)/rootfs/bin \
	           $(OUT_DIR)/rootfs/proc $(OUT_DIR)/rootfs/sys \
	           $(OUT_DIR)/rootfs/dev $(OUT_DIR)/rootfs/tmp \
	           $(OUT_DIR)/rootfs/etc
	@cp $(OUT_DIR)/$(BINARY) $(OUT_DIR)/rootfs/sbin/$(BINARY)
	@cp build/init $(OUT_DIR)/rootfs/init
	@chmod +x $(OUT_DIR)/rootfs/init $(OUT_DIR)/rootfs/sbin/$(BINARY)
	@if which busybox-static > /dev/null 2>&1; then \
	    cp $$(which busybox-static) $(OUT_DIR)/rootfs/bin/busybox; \
	    $(OUT_DIR)/rootfs/bin/busybox --install -s $(OUT_DIR)/rootfs/bin; \
	fi
	@cd $(OUT_DIR)/rootfs && find . | cpio -oH newc | gzip > ../initramfs.cpio.gz
	@echo "Built: $(OUT_DIR)/initramfs.cpio.gz"

## ── ISO ──────────────────────────────────────────────────────────────────────
# Requires: a pre-built kernel vmlinuz + grub-mkrescue + xorriso
iso: initramfs
	@mkdir -p $(OUT_DIR)/iso/boot/grub
	@[ -f $(OUT_DIR)/vmlinuz ] || (echo "ERROR: $(OUT_DIR)/vmlinuz not found. Build kernel first." && exit 1)
	@cp $(OUT_DIR)/vmlinuz $(OUT_DIR)/iso/boot/vmlinuz
	@cp $(OUT_DIR)/initramfs.cpio.gz $(OUT_DIR)/iso/boot/initramfs.cpio.gz
	@cp build/grub.cfg $(OUT_DIR)/iso/boot/grub/grub.cfg
	@grub-mkrescue -o $(OUT_DIR)/simpleclient.iso $(OUT_DIR)/iso -- -volid SIMPLECLIENT
	@echo "Built: $(OUT_DIR)/simpleclient.iso"
	@ls -lh $(OUT_DIR)/simpleclient.iso

## ── Docker build (full: binary + kernel + iso) ───────────────────────────────
docker-build:
	@mkdir -p $(OUT_DIR)
	docker build \
	    --target export \
	    --output type=local,dest=$(OUT_DIR) \
	    -f build/Dockerfile .
	@echo "Artifacts in $(OUT_DIR)/"

## ── QEMU test ────────────────────────────────────────────────────────────────
qemu:
	@[ -f $(OUT_DIR)/simpleclient.iso ] || (echo "Build iso first: make docker-build" && exit 1)
	qemu-system-x86_64 \
	    -m 256M \
	    -boot d \
	    -cdrom $(OUT_DIR)/simpleclient.iso \
	    -vga std \
	    -display vnc=:1 \
	    -device e1000,netdev=net0 \
	    -netdev user,id=net0 \
	    -drive if=pflash,format=raw,readonly=on,file=/opt/homebrew/share/qemu/edk2-x86_64-code.fd

## ── Clean ────────────────────────────────────────────────────────────────────
clean:
	rm -rf $(OUT_DIR)
