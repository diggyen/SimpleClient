# Architecture

SimpleClient is a bootable image with no display server, no session manager and
no desktop. One static Go binary owns the framebuffer, reads the input devices
directly and draws every pixel of its screens itself; a session is drawn by
`sc-rdp`, FreeRDP drawing straight to the display through KMS.

This document covers the parts that are decisions rather than code — the things
you cannot recover by reading the files.

## The stack

```
┌──────────────────────────────────────────────┐
│ GRUB          picks a language and a kernel  │
├──────────────────────────────────────────────┤
│ Linux         Debian's, signed: the newest   │
│               and an LTS; drivers loaded by  │
│               the hardware's modaliases      │
├──────────────────────────────────────────────┤
│ /init         busybox sh: drivers, network,  │
│               waits for input, runs us       │
├──────────────────────────────────────────────┤
│ simpleclient  one static Go binary           │
│   ├ framebuffer  mmap'd /dev/fb0             │
│   ├ inputdev     /dev/input/event*           │
│   ├ scanner      TCP 3389 across the subnet  │
│   ├ ui           render loop + screens       │
│   └ rdp          runs sc-rdp, or grdp        │
├──────────────────────────────────────────────┤
│ sc-rdp        FreeRDP 3, FFmpeg for H.264,   │
│               drawing through KMS            │
└──────────────────────────────────────────────┘
```

There is no writable root: the image boots into an initramfs held in RAM, so
every boot is identical and pulling the power is a supported way to turn it
off. What the kiosk keeps — the network settings, the wireless network, the
startup server, and on an installed kiosk the servers and accounts it has
connected to — is a few small files under `simpleclient/`: on the data
partition of an installed kiosk, on the EFI partition of the USB stick a live
one runs from.

## Boot

`build/init` runs as PID 1. In order:

1. Mounts `/proc`, `/sys`, `/dev`.
2. Loads the drivers the hardware asks for: every device's modalias goes
   through `modprobe`, as udev would do it — there is no udev. The graphics
   driver comes first, with its firmware, so the UI starts on it.
3. Waits (briefly) for a pointer device to appear. `psmouse` takes a moment to
   probe, and without the wait the kiosk can reach the UI before the mouse
   exists and come up keyboard-only. The wait is capped — a machine with no
   mouse must still boot.
4. Brings up the network with `udhcpc`, falling back to link-local, and joins
   the kept wireless network with `wpa_supplicant`.
5. Logs the framebuffer geometry, the input devices and their capability
   bitmaps to the console, then `exec`s `/sbin/simpleclient`.

If the binary exits, init restarts it after two seconds. A crash is a flicker,
not a dead kiosk.

### Why the console logging matters

The kiosk has no shell, no logs on disk and no way to tell you anything except
what it draws. When it comes up wrong — no keyboard, no network, a blank screen
— the serial console is the only evidence, and it can only be captured from the
machine that failed. That is why `/init` prints the input capability table and
why `simpleclient` prints which device nodes it chose. Both exist because a real
machine came up with a working mouse and a dead keyboard and there was nothing
to look at.

The GRUB menu's debug entry adds `console=ttyS0` verbosity and the full RDP
handshake trace. For the same reason the build version is drawn along the bottom
of the discovery screen: it is the only way to tell which image is running
without taking the stick out.

## Disk install and updates

`F9` on a kiosk running from a USB stick or CD installs it (`build/sc-install`):
onto an internal disk, or onto the stick it runs from. The system runs from RAM,
so for the stick the installer copies what it installs into memory first, then
wipes it. The layout: an ESP with shim, GRUB, `grub.cfg` and `grubenv`; two
system slots, `SC_A` and `SC_B`, each holding the kernels, the initrds,
`slot.cfg` and `version`; and `SC_DATA` for what the kiosk keeps. Every install
carries the same labels, so `sc-install` records the partitions' UUIDs in
`grubenv`, and GRUB, `/init` (`sc.data=`) and the kiosk (`sc.esp=`) go by those:
an installed stick left in a machine with an installed SSD boots its own slots
and keeps its own settings.

GRUB (`build/grub-disk.cfg`) boots the slot `sc_boot` names, or once the slot
`sc_try` names, the way that slot's `slot.cfg` says: each release brings its own
kernel command line and initrds, and an update changes them without touching the
configuration both slots share. A kernel that will not load falls back to the
LTS kernel, then to the other slot.

On an installed kiosk `F9` is the update (`internal/update`). It asks the release
server — the project's GitHub releases, or a mirror whose base URL is in
`/data/simpleclient/update-url`, on a stick `simpleclient/update-url` of its
FAT partition — which release is the newest: `<base>/latest`
redirects to its tag (GitHub) or holds it (a mirror), and
`<base>/download/<tag>/SHA256SUMS` has the checksum of `simpleclient-update.tar`,
the files of one slot. A check also runs two minutes after start-up and daily
after that; it only asks, and the foot of the server list says when there is a
new release. *Update* streams the tar into the slot not running — nothing is
held in RAM, nothing in the running slot is touched — checks its SHA-256 and
that its `version` is the release asked for, gives the files their names, sets
`sc_try` and restarts. GRUB clears the try before the new kernel starts; 20 s
after the kiosk comes up, `bootenv.MarkGood` makes the slot `sc_boot`, and a
boot that dies before then is followed by one of the old slot. The server list
says which happened: the update in green, or in amber that the release did not
start on this machine and the kiosk went back.

A USB stick the kiosk runs from updates itself the same way, with `F11`: `F9`
there stays the install. The ISO's own files stay as they were; releases go
into two slots beside them, the directories `slots/A` and `slots/B` of a FAT
file system — on a stick Rufus wrote in ISO mode its one partition, on a stick
the ISO was written to as it is (dd, Rufus's DD mode) a partition the first
update adds in the free space behind the ISO (`internal/gpt`, up to 2 GiB,
FAT32, `SC_SLOTS`, its volume ID kept in `grubenv` as `sc_slots`). The table is
written backup first, primary last, and the ISO's partitions and protective MBR
stay as they were. The ISO's `grub.cfg` picks the slot from the stick's
`grubenv` as `grub-disk.cfg` does, boots it through its `slot.cfg` with
`sc.live=1`, and falls back to the ISO's files when the slot will not load; the
boot menu offers the ISO's release and the previous one by hand. A stick that
has been updated installs the release it runs.

Everything comes over HTTPS, and a redirect to plain HTTP is refused: over HTTP
whoever is on the way could replace the update and its checksum alike.
Certificates are checked against the image's CA bundle, and against the PEM
certificates in `/data/simpleclient/update-ca.pem` when there is one, for a
mirror whose certificate is from a CA of its own. The kiosk has no time
server, and a clock running behind the build's own date is taken to be wrong:
the build date stands in for it. The update has no signature yet. What it is
checked against comes over HTTPS from the same release, so whoever can publish
a release of the repository can publish an update. Signing it would take an
ed25519 key kept offline, out of the repository and CI: its public half built
into the kiosk, its private half signing `SHA256SUMS` for each release, and a
kiosk with a public key refusing a `SHA256SUMS` without a valid signature.

## Rendering

`internal/ui` owns a back buffer (`*image.RGBA`) and blits it to the framebuffer
when something changes. The loop is dirty-flag driven: `UIState.Dirty` is set by
anything that changes what is on screen, and only then does a frame get drawn.
Anything that mutates state from another goroutine — the scan feed, the connect
goroutine — must set it, or its result is computed and never shown.

### Layout is computed once, in one place

`layoutDiscoveryRows` returns every rectangle the discovery screen uses, and
both the renderer and the mouse hit-test derive from it. This is deliberate: the
two used to compute positions separately, and a click could select a different
host from the one under the pointer. If you add a region, add it to the layout
struct rather than computing it at the draw site.

### Drawing primitives

Everything is drawn from `internal/ui/draw.go` — filled rectangles, lines,
notched panels, meters. There is no anti-aliasing anywhere, on purpose: the mark
is a pixel sprite and softened edges next to it look like a rendering fault.

Two typefaces are in play:

- **Go Mono**, from `golang.org/x/image`, for all body text. It replaced
  `basicfont.Face7x13` because the UI is localised and that face is ASCII-only:
  Turkish needs `ğ ı İ ş ç ö ü`. Sizes are chosen so the advance width lands on
  exact pixel multiples of the 7px cell — 11pt and 23pt at 72 DPI. Anything in
  between rounds differently and breaks the column grid.
- **A hand-authored 5×7 bitmap face** (`internal/ui/pixelfont.go`) for the
  wordmark and small all-caps labels. An outline face scaled up is just body
  text set larger; a bitmap face scaled by whole pixels keeps square edges at
  any size, which is what makes the mark read as 8-bit.

The mark itself is a 16×16 sprite in `internal/ui/logo.go`, written as strings
with one character per pixel. `TestScreenshots` renders it at 12× so it can be
reviewed pixel by pixel.

### Colour

Surface steps are wider than a desk monitor needs. Kiosk panels are frequently
cheap TN screens viewed off-axis, where a two-value difference between surfaces
disappears entirely. If you tune the palette, judge it on the target hardware —
a laptop screen will tell you it is too contrasty, and it is not.

## Input

`internal/inputdev` reads `struct input_event` from evdev nodes directly.

Device classification uses the **capability bitmaps** the kernel exports in
`/proc/bus/input/devices`, not the device name. The name is the USB product
string, so a keyboard is as likely to call itself `Logitech USB Receiver` or
`HID 046a:0011` as anything containing the word "keyboard". Matching the name
worked under QEMU — whose emulated keyboard is called `AT Translated Set 2
keyboard` — and failed on hardware.

Matching the handler list does not work either: a power button, a lid switch and
an ACPI video bus are all `EV_KEY` devices that the kernel gives a `kbd`
handler, and on real hardware they enumerate ahead of the keyboard. A keyboard
is identified by carrying ordinary letter keys; a pointer by being a relative
device with both axes and a left button, or a touchpad.

Layouts (US, Turkish Q, Turkish F) are override tables over the US map, so a key
that a layout does not move is carried once. They affect the credential dialog
only: inside a session the raw keycode is forwarded and the remote machine
applies its own layout, which is what anyone connecting to a Windows desktop
expects. The layout is paired with the UI language wherever the language is set
— the boot flag and `F2` both — and `F3` overrides it.

Every key transition is emitted — presses, repeats and releases, modifiers
included. The local screens ignore releases; a live session forwards all of
them, and must, because a remote that is sent presses without releases has
every key held down and a remote with no Shift, Ctrl or Alt cannot be driven.

The engine sends RDP's *extended* flag with the keys that carry it. The
built-in client cannot: grdp's KeyDown/KeyUp take a bare scancode, so on it the
arrow cluster and the navigation keys arrive as their numpad equivalents.

Every keyboard and pointer is read, including ones plugged in later. `-kbd` and
`-mouse` (`kbddev=` and `mousedev=` on the GRUB line) pin the nodes instead.

### Remembered accounts

A host that connects successfully has its username and domain kept against its
address, so reopening its dialog fills them in and puts the cursor in the
password field. Only the successful attempt is recorded: an account that was
rejected is not one to offer back.

A kiosk running from a USB stick or CD keeps them in RAM until it is switched
off; an installed one keeps them on its data partition. The password is not
part of it, unless `F10` asks for it (see the README).

## Discovery

`internal/scanner` opens a TCP connection to port 3389 across the subnet the
machine got from DHCP, bounded by a worker semaphore. A host that completes the
handshake is a host; nothing speaks RDP to check.

The handshake is timed and reported as the latency shown in the list, measured
before the reverse DNS lookup so a slow resolver is not charged to the host. It
never reports zero for a live host: a switched LAN answers in well under a
millisecond, and the list reads zero as "not measured" and draws nothing.

The kiosk scans **its own subnet only**. A server on another subnet is not
discovered: `F7` connects to it by address or name, and once connected to it
stays in the list. `rdphost=<ip>` on the kernel command line seeds the list with
a hand-picked address before the scan; the scanner deduplicates by IP, so if
that address is also local the scan just fills in its latency.

## RDP

Sessions run in `sc-rdp` (`engine/`), a FreeRDP 3 client built for the kiosk.
It draws straight to the display through KMS — page flips at the display's own
refresh rate, a hardware cursor where the display has one, up to four displays
side by side as one desktop — and decodes RemoteFX and H.264 (AVC420 and
AVC444, with an FFmpeg built for nothing else) through the graphics pipeline.
H.264 goes to the GPU's decoder where the machine has one that works
(`hwdec.c`): Intel's through iHD or i965, AMD's through Mesa's radeonsi or r600,
over VA-API, as found by having each GPU decode a test stream in a child
process at start. Elsewhere, NVIDIA included, the CPU decodes it.
Sound, the microphone, drives, USB devices and smart cards go through
FreeRDP's own channels. The kiosk starts one per session and talks to it over
two pipes, described at the top of `sc-rdp.c`; the password never goes on its
command line. With no DRM device it draws into the fbdev framebuffer instead.

FreeRDP is built from its release with the patches in `build/patches/freerdp/`,
measured on a 1080p session (the Dockerfile has the numbers):

| Patch | Why |
| --- | --- |
| `0001` | the planar codec's buffers were sized for the whole desktop, twice: 345 MB at 3440x1440 |
| `0002` | the progressive (RemoteFX) codec's inverse DWT had no SSE2 version, and was a third of decoding video |
| `0003` | progressive tiles went to the thread pool one at a time |
| `0004` | H.264 was decoded on one thread; a frame's slices now go side by side |
| `0005` | progressive tiles took their scratch buffers from one pool, locked from every thread |
| `0006` | a GPU driver that offered its pictures as YUV420P and then failed to give them lost every frame; they now come in its own format, and swscale converts them |

Where the engine cannot run — it is missing, or finds no display at all —
`internal/rdp` falls back to a built-in client, `github.com/tomatome/grdp`,
which is vendored and patched. It decodes legacy bitmap updates only, into the
framebuffer. The patches are in `build/patches/` and CI fails if they go
missing:

| Patch | Why |
| --- | --- |
| `0001` | grdp has a vestigial `import "C"` that forces cgo and blocks static linking |
| `0002` | a `cliprdr` source file uses Windows-only symbols with no build tag, so the package does not compile for Linux |
| `0003` | the client's close path is not exported, so a session cannot be torn down cleanly |

## Known limitations

See the README's [Known limitations](../README.md#known-limitations).
