# SimpleClient

A bootable remote-desktop kiosk. Boot it on a machine, it scans the local
network for RDP hosts, you pick one, and it connects — full screen, no desktop
environment underneath.

It draws straight to the Linux framebuffer (`/dev/fb0`) and reads raw evdev
input. There is no X11 and no Wayland: the whole system is a kernel, busybox and
one static Go binary in an initramfs, about 40 MB of ISO.

The UI ships in **English** (default) and **Türkçe**. Pick one from the boot
menu, or press **F2** at any time to switch.

## Get it

Download `simpleclient.iso` from the
[latest release](https://github.com/diggyen/SimpleClient/releases/latest) and
write it to a USB stick:

```sh
# Linux
sudo dd if=simpleclient.iso of=/dev/sdX bs=4M status=progress conv=fsync

# macOS  (diskutil list to find the number; use rdiskN, not diskN)
sudo dd if=simpleclient.iso of=/dev/rdiskN bs=4m
```

Verify first if you like:

```sh
sha256sum -c SHA256SUMS
```

Boots on both UEFI and legacy BIOS machines.

## Use it

| Key | Action |
| --- | --- |
| `↑` `↓` `PgUp` `PgDn` | Move through the host list |
| `Enter` | Open the credential dialog for the selected host |
| `Tab` | Move between username / password / domain / buttons |
| `Esc` | Close the dialog |
| `F5` | Rescan the network |
| `F2` | Switch language (English ⇄ Türkçe) |
| `Ctrl`+`Alt`+`End` | Disconnect from an active session |

The scan probes TCP port 3389 across the subnet the machine got from DHCP. If
there is no DHCP server it falls back to a link-local address and scans that.

## Build it from source

Everything below runs from the repository root.

### Bootable ISO

Needs Docker. The kernel, initramfs and ISO are all produced inside the build
container, so the result is the same on macOS, Linux and CI.

```sh
make -f build/Makefile iso     # -> dist/simpleclient.iso
```

### Binary only

```sh
make -f build/Makefile binary  # -> dist/simpleclient  (static, linux/amd64)
make -f build/Makefile test
```

Or run everything — vet, race tests, static build — with `bash setup.sh`.
Add `--iso` to build the image too, or `--headless` to also boot it in QEMU and
capture a screenshot.

### See it running

You do not need spare hardware — QEMU boots the ISO in a window, and it is the
same image that goes on a USB stick.

```sh
make -f build/Makefile iso     # if you have not built it yet
bash build/test-qemu.sh        # opens a window; watch it boot
```

You will get the GRUB menu (English / Türkçe / two debug entries), then the
discovery screen. Arrow keys select, Enter opens the login dialog, F2 switches
language. `Ctrl+Alt+G` releases the mouse back to your desktop.

On its own the guest sits on QEMU's private `10.0.2.0/24`, so it will find
nothing to connect to. Two ways to give it something:

```sh
# A host that appears in the list but will not complete a session:
bash build/test-qemu.sh --fake-rdp

# A real RDP server, bridged in so the guest can discover and use it:
bash build/test-qemu.sh --proxy-rdp=10.0.0.5:3389
```

`--proxy-rdp` exists because the kiosk only scans its own subnet, and the guest's
subnet is QEMU's, not yours. It forwards host port 3389 to your server, and the
guest sees it as `10.0.2.2` — pick that, type your credentials, and you get the
real desktop.

Other options: `--headless` boots and writes a screenshot instead of opening a
window, and `--entry=N` picks a boot menu entry (1 is Turkish, 2 logs the whole
RDP handshake to `dist/qemu-serial.log`).

If a connection fails and the on-screen message is not enough, boot the
**RDP handshake debug** entry from the GRUB menu; it prints every protocol step
to the serial console.

### Test against a real RDP server

Everything in `internal/rdp` is unit-tested against synthetic tiles except the
handshake itself. `TestLiveConnect` drives a real server — NLA authentication,
the session PDUs and the bitmap stream — and is skipped unless you point it at
one:

```sh
SIMPLECLIENT_RDP_ADDR=10.0.0.5:3389 \
SIMPLECLIENT_RDP_USER=administrator \
SIMPLECLIENT_RDP_PASS=... \
SIMPLECLIENT_RDP_SHOT=/tmp/desktop.png \
go test -mod=vendor ./internal/rdp -run TestLiveConnect -v
```

`SIMPLECLIENT_RDP_SHOT` composites the incoming tiles into a PNG, which is the
only way to confirm the decode is right: a session can be live and still send
nothing this decoder understands, and that looks exactly like a black screen.

## Known limitations

- **Colour depth is whatever the server picks.** Windows commonly negotiates
  16bpp, which is what the tile decoder is tuned for.
- **No RemoteFX/GFX codecs.** grdp ignores surface-command updates, so servers
  that send only those will show nothing. Every Windows server tested still
  falls back to legacy bitmap updates, which work.
- **No manual host entry.** The kiosk connects to hosts it discovers on its own
  subnet; there is no box to type an address into. A server on another subnet
  cannot be reached at all.
- **Resolution follows the framebuffer** the kernel hands over, and the session
  is opened at that size. There is no way to override it.
- **Clipboard, audio and drive redirection are not implemented.**

## Layout

```
cmd/simpleclient/     entry point: opens the framebuffer, wires everything up
internal/ui/          kiosk render loop, screens, input handling
internal/scanner/     concurrent TCP scan of the subnet for port 3389
internal/rdp/         RDP client wrapper around tomatome/grdp
internal/framebuffer/ mmap'd /dev/fb0
internal/inputdev/    evdev keyboard and mouse reader
internal/i18n/        message catalogue (en, tr)
build/                Dockerfile, init, GRUB config, Makefile, vendor patches
```

## A note on `vendor/`

The vendor directory is committed, which is unusual. It has to be:
`github.com/tomatome/grdp` does not compile for Linux as published. Two upstream
defects need patching — a `cliprdr` source file that uses Windows-only symbols
without a build tag, and a vestigial `import "C"` that forces cgo and so blocks
static linking. Both fixes live in `build/patches/`.

After changing a dependency in `go.mod`, regenerate the tree with:

```sh
bash build/vendor-patch.sh   # go mod vendor + re-apply patches + verify
```

CI fails if the patches go missing.

## Requirements

- x86-64 machine, UEFI or BIOS
- 512 MB RAM
- A wired or wireless NIC with a driver in Alpine's `linux-lts` kernel
- Somewhere on the network actually listening on 3389
