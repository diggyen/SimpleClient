# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

rdpboot is a minimal kiosk OS (~50 MB bootable ISO) that boots on x86_64 hardware, scans the local network for RDP servers on port 3389, and provides a fullscreen framebuffer UI for one-click RDP connections. There is no X11, Wayland, web server, HTTP, or shell access. A single statically-compiled Go binary writes pixels directly to `/dev/fb0` and reads input from `/dev/input/event*` via evdev.

Language: Go 1.23 | Module: `github.com/kullanici/rdpboot` | UI language: Turkish

## Build & Test Commands

```bash
# Static binary (requires Linux or cross-compile)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags '-s -w' -o dist/rdpboot ./cmd/rdpboot

# Via Makefile (preferred)
make binary       # build static binary to dist/
make test         # run go test ./... -v
make vet          # run go vet ./...
make iso          # build full bootable ISO (needs vmlinuz + grub-mkrescue)
make docker-build # full build in Docker container
make qemu         # test ISO in QEMU with SDL display
make clean        # remove dist/

# Run a single test
go test ./internal/scanner/... -run TestExpandCIDR -v
```

CI runs on Ubuntu: `go vet ./...` → `go test ./... -v -timeout 120s` → static binary build → verify "statically linked".

Note: `cmd/rdpboot/main.go` has `//go:build linux` — the binary only compiles on Linux. Tests for other packages work on any OS via `MockFB`.

## Architecture

### Screen State Machine

```
[Boot] → [Discovery] ⇄ [Modal] → [Connecting] → [Session]
             ↑                                        │
             └────────────── Disconnect ──────────────┘
```

UI screens (`internal/ui/state.go`): `ScreenDiscovery`, `ScreenModal`, `ScreenConnecting`, `ScreenSession`. During `ScreenSession`, the normal UI render loop is bypassed — RDP frames go directly to the framebuffer via `rdp.FrameWriter`.

### Main Loop (`internal/ui/loop.go`)

Event-driven `select` loop with three sources:
1. **Scan events** (`<-chan domain.ScanEvent`) — from `scanner.NetworkScanner`
2. **Input events** (`<-chan inputdev.InputEvent`) — from `inputdev.Reader`
3. **16ms ticker** — renders only when `dirty` flag is set (~60 FPS cap)

The loop never returns (kiosk mode). `connect()` runs in a goroutine; the RDP frame loop blocks until the connection closes.

### Package Dependency Graph

```
cmd/rdpboot → config, framebuffer, inputdev, scanner, ui
ui/loop     → ui/state, ui/render, scanner (domain.Scanner), inputdev, rdp, network, config
ui/render   → ui/draw, ui/colors, framebuffer (Device interface)
scanner     → domain, network
rdp         → grdp/client, framebuffer (Device interface)
framebuffer → syscall, mmap (Linux-specific)
inputdev    → syscall, evdev (Linux-specific)
domain      → stdlib only (Host, ScanEvent, Scanner interface)
```

### Key Interfaces

- **`framebuffer.Device`** (`internal/framebuffer/interface.go`) — `Blit`, `BlitRect`, `WritePixel`, `Bounds`, `Width`, `Height`, `Close`. The concrete `FB` type uses `mmap` on `/dev/fb0`. `MockFB` in `mock.go` is used for tests.
- **`domain.Scanner`** (`internal/domain/interfaces.go`) — `Start(ctx, cidrs)`, `Cancel()`, `Hosts()`. Implemented by `scanner.NetworkScanner`.
- **`ui.FBInterface`** (render) — subset of framebuffer methods used by render code.

### grdp Integration Notes

The RDP client wraps `github.com/tomatome/grdp/client.Client`. Key grdp API:
- `client.NewClient(addr, user, pass, TC_RDP, setting)` — create
- `g.Login()` — connect (run in goroutine, it blocks)
- `g.OnSuccess()`, `g.OnError()`, `g.OnClose()`, `g.OnBitmap()` — event callbacks
- `g.KeyDown(sc, name)`, `g.KeyUp(sc, name)` — keyboard input
- `g.MouseMove(x, y)`, `g.MouseDown(btn, x, y)`, `g.MouseUp(btn, x, y)` — mouse input
- **No `Close()` on `*client.Client`** — close is on the internal `Control` interface. Use `g.OnClose()` callback to detect disconnection; the `done` channel pattern is used for lifecycle management.

### Framebuffer Pixel Format

Linux framebuffer uses BGRA byte order (little-endian). The `Blit()` method swaps RGBA→BGRA during copy. Tests should verify this channel swap.

### Scanner Concurrency

`NetworkScanner` uses a semaphore channel (default 256 slots) for fan-out scanning. Each IP gets a goroutine with `net.DialTimeout("tcp", ip+":3389", timeout)`. Progress events are sent every 10 IPs. `Cancel()` drains the semaphore and cancels context.

### Kiosk Init System

`build/init` is a BusyBox shell script that mounts virtual filesystems, configures networking (DHCP with link-local fallback), and runs rdpboot in a `while true` loop. Kernel params: `quiet loglevel=0 panic=5 vt.global_cursor_default=0`. SysRq is disabled in kernel config.

## Important Patterns

- **MockFB for testing**: All render tests use `framebuffer.MockFB` (in-memory `image.RGBA`) instead of real `/dev/fb0`. Tests verify pixel values at specific coordinates.
- **Dirty flag rendering**: The UI only re-renders when state changes (scan event or input event). The 16ms ticker checks the dirty flag.
- **Frame dropping in RDP**: The `frames` channel (cap 4) drops old frames when full to avoid blocking the RDP bitmap callback.
- **Modifier key tracking**: `inputdev.Reader` tracks Shift/Ctrl/Alt state internally for keycode→rune mapping. The UI loop checks for Ctrl+Alt+End combo to disconnect RDP.
- **Vendor directory**: Dependencies are vendored (`vendor/`). Use `go mod vendor` after dependency changes.
