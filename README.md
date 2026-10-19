# SimpleClient

A bootable remote-desktop kiosk. Boot it on a machine, it scans the local
network for RDP hosts, you pick one, and it connects — full screen, no desktop
environment underneath.

![The host picker](docs/screenshots/02-list.png)

It draws straight to the Linux framebuffer (`/dev/fb0`) and reads raw evdev
input. There is no X11 and no Wayland: the whole system is a kernel, busybox and
one static Go binary in an initramfs, about 40 MB of ISO. Every pixel above —
the mark, the lettering, the signal bars — is drawn by the binary itself; there
is no toolkit and no image asset in the ISO.

The UI ships in **English** (default) and **Türkçe**. Pick one from the boot
menu, or press **F2** at any time to switch.

![The same screen in Turkish](docs/screenshots/08-turkish.png)

Every screen is in [`docs/screenshots/`](docs/screenshots), including the
credential dialog, the empty and error states, and a capture of the real thing
booting under QEMU.

## Get it

Download `simpleclient.iso` from the
[latest release](https://github.com/diggyen/SimpleClient/releases/latest) and
write it to a USB stick:

```sh
# Linux
sudo dd if=simpleclient.iso of=/dev/sdX bs=4M status=progress conv=fsync

# macOS  (diskutil list to find the number; use rdiskN, not diskN)
diskutil unmountDisk /dev/diskN
sudo dd if=simpleclient.iso of=/dev/rdiskN bs=4m
sync
```

Verify it first if you like: `shasum -a 256 -c SHA256SUMS`.

Boots on UEFI and legacy BIOS machines. **Secure Boot must be off** — the
bootloader is unsigned, and a machine enforcing Secure Boot refuses the stick
without printing anything, which looks exactly like an empty drive. If it does
not boot, [TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md#the-usb-stick-does-not-boot)
walks through the causes in order of likelihood.

## Use it

| Key | Action |
| --- | --- |
| `↑` `↓` `PgUp` `PgDn` | Move through the host list |
| `Enter` | Open the credential dialog for the selected host |
| `Tab` | Move between username / password / domain / buttons |
| `Esc` | Close the dialog |
| `F5` | Rescan the network |
| `F2` | Switch language (English ⇄ Türkçe), and the keyboard layout with it |
| `F3` | Switch keyboard layout on its own (US ⇄ TR-Q ⇄ TR-F) |
| `Ctrl`+`Alt`+`End` | Disconnect from an active session |

The scan probes TCP port 3389 across the subnet the machine got from DHCP. If
there is no DHCP server it falls back to a link-local address and scans that.
Each host is listed with the round trip of that handshake, graded onto four
signal bars, so a link too slow to work as a desktop session is visible before
you connect to it rather than after.

The two caps in the top-right corner show the active keyboard layout and
language. Choosing Türkçe — from the boot menu or with `F2` — selects the
Turkish Q layout with it; `F3` overrides that if your keyboard says otherwise.
The layout applies to the credential dialog only: inside a session the raw
keycode is forwarded and the remote machine applies its own.

Once a host has been connected to, its username and domain are filled in the
next time you open its dialog, with the cursor in the password field. That
memory lives in RAM and is gone after a power cycle — the image boots read-only
and has nowhere to write. The password is never stored.

The kiosk reaches **its own subnet only**. A server on another subnet is not
discovered, but it can be added by hand: `rdphost=<ip>` on the GRUB command line
puts it in the list ready to connect.

## Linux servers

Windows serves the kiosk as it is. A Linux desktop needs xrdp, and the one
distributions package (0.9 on Ubuntu 24.04 and Linux Mint 22) sends the screen
as bitmaps: no H.264, a fraction of the frame rate. One script, run once on the
server, builds xrdp 0.10 with H.264 and sets it up for the kiosks — up to 120
fps, sound both ways through PipeWire or PulseAudio, the Turkish keyboards
(which xrdp does not map), the machine's own desktop for every user, no polkit
prompts, port 3389 open:

```sh
curl -fsSLO https://raw.githubusercontent.com/diggyen/SimpleClient/iyilestirme/server/simpleclient-server.sh
sudo bash simpleclient-server.sh
```

Tested on Ubuntu 24.04 and 22.04 (the bases of Linux Mint 22 and 21) and
Fedora 42; Debian, openSUSE and Arch are attempted. Fedora's own repositories
have no x264, so there it builds with Cisco's OpenH264, which encodes about a
quarter fewer frames (55 fps against 75 on the same machine); with RPM Fusion
enabled it takes x264. Run it again after a distribution upgrade.
`--session NAME` picks the desktop when there are several (`cinnamon`, `mate`,
`xfce`, `plasmax11`, ...). A desktop that runs only on Wayland cannot be served
by xrdp.

## Build it

Needs Docker. The kernel, initramfs and ISO are produced inside the build
container, so the result is the same on macOS, Linux and CI.

```sh
make -f build/Makefile iso     # -> dist/simpleclient.iso
make -f build/Makefile binary  # -> dist/simpleclient (static, linux/amd64)
make -f build/Makefile test
```

You do not need spare hardware to try it — QEMU boots the same image that goes
on the stick:

```sh
bash build/test-qemu.sh                  # opens a window
bash build/test-qemu.sh --fake-rdp       # plus a host that appears in the list
```

[DEVELOPMENT.md](docs/DEVELOPMENT.md) covers the rest: iterating on the UI
without booting anything, the four boot combinations to run before trusting an
image, testing against a real RDP server, and cutting a release.

## Documentation

- **[ARCHITECTURE.md](docs/ARCHITECTURE.md)** — how the image boots, how the UI
  is drawn, how devices are found, and the decisions behind each
- **[DEVELOPMENT.md](docs/DEVELOPMENT.md)** — building, testing, releasing
- **[TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md)** — symptom to diagnosis, and
  how to read the serial console

## Known limitations

Design boundaries, not bugs waiting to be filed:

- **Secure Boot is not supported.** The bootloader is unsigned.
- **No RemoteFX/GFX codecs.** A server sending only those shows a black screen
  on a session that is genuinely live. Every Windows server tested falls back to
  legacy bitmap updates, which work.
- **Colour depth is whatever the server picks.** Windows commonly negotiates
  16bpp, which is what the tile decoder is tuned for.
- **Resolution follows the framebuffer** the kernel hands over; there is no
  override.
- **No in-UI host entry.** A server off the local subnet has to be named at boot
  with `rdphost=<ip>`; there is no box to type one into while running.
- **No clipboard, audio or drive redirection.**
- **Extended keys are not flagged.** The arrow cluster and the navigation keys
  reach a session as their numpad equivalents; right Ctrl and right Alt are not
  mapped.
- **One keyboard and one pointer.** On a machine with two keyboards, the first
  recognised wins.

## Repository layout

```
cmd/simpleclient/     entry point: opens the framebuffer, wires everything up
internal/ui/          render loop, screens, layout, the 8-bit mark and face
internal/scanner/     concurrent TCP scan of the subnet for port 3389
internal/rdp/         RDP client wrapper around tomatome/grdp
internal/framebuffer/ mmap'd /dev/fb0
internal/inputdev/    evdev keyboard and mouse reader
internal/i18n/        message catalogue (en, tr)
build/                Dockerfile, init, GRUB config, Makefile, vendor patches
docs/                 architecture, development, troubleshooting, screenshots
```

`vendor/` is committed on purpose: `tomatome/grdp` does not compile for Linux as
published, and three local patches in `build/patches/` fix it. CI fails if they
go missing. See [ARCHITECTURE.md](docs/ARCHITECTURE.md#rdp).

## Requirements

- x86-64 machine, UEFI or BIOS, Secure Boot off
- 512 MB RAM
- A wired or wireless NIC with a driver in Alpine's `linux-lts` kernel
- Somewhere on the same subnet actually listening on 3389
