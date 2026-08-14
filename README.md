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

### Try it in QEMU

```sh
bash build/test-qemu.sh                        # interactive window
bash build/test-qemu.sh --headless --fake-rdp  # boot, screenshot, exit
```

`--fake-rdp` opens port 3389 on the host, which QEMU's user-mode networking
presents to the guest at `10.0.2.2`, so the scan finds something to list.

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
