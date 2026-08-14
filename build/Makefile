## SimpleClient — Build System
## Run from the repository root:  make -f build/Makefile <target>
##
## Targets: binary, test, vet, iso (alias for docker-build), qemu, vendor, clean

.PHONY: all binary test vet iso docker-build qemu qemu-headless vendor clean help

BINARY     := simpleclient
OUT_DIR    := dist

GO          := go
GOOS        := linux
GOARCH      := amd64
CGO_ENABLED := 0
# vendor/ is committed and patched (see build/patches/), so every go invocation
# must use it rather than the module cache.
GOFLAGS     := -mod=vendor
LDFLAGS     := -s -w

help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //'

all: binary iso

## ── Go binary ────────────────────────────────────────────────────────────────
binary:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
	$(GO) build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(OUT_DIR)/$(BINARY) ./cmd/simpleclient
	@echo "Built: $(OUT_DIR)/$(BINARY)"
	@file $(OUT_DIR)/$(BINARY)

## ── Tests ────────────────────────────────────────────────────────────────────
test:
	$(GO) test $(GOFLAGS) -race ./... -timeout 120s

vet:
	$(GO) vet $(GOFLAGS) ./...

## ── ISO ──────────────────────────────────────────────────────────────────────
# The kernel, initramfs and ISO are all produced inside the build container so
# the result is identical on macOS, Linux and CI. See build/Dockerfile.
iso: docker-build

docker-build:
	@mkdir -p $(OUT_DIR)
	docker build \
	    --target export \
	    --output type=local,dest=$(OUT_DIR) \
	    -f build/Dockerfile .
	@echo "Artifacts in $(OUT_DIR)/"
	@ls -lh $(OUT_DIR)/

## ── Vendor ───────────────────────────────────────────────────────────────────
# Only needed after changing a dependency in go.mod.
vendor:
	bash build/vendor-patch.sh

## ── QEMU ─────────────────────────────────────────────────────────────────────
qemu:
	bash build/test-qemu.sh

# Boot the ISO with no window and capture a screenshot — used to verify the UI
# actually renders. See build/test-qemu.sh --headless.
qemu-headless:
	bash build/test-qemu.sh --headless

## ── Clean ────────────────────────────────────────────────────────────────────
clean:
	rm -rf $(OUT_DIR)
