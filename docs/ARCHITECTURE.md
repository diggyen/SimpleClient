# Architecture

SimpleClient is a bootable image whose entire userspace is one static Go binary.
There is no display server, no session manager, no desktop. The binary owns the
framebuffer, reads input devices directly, and draws every pixel itself.

This document covers the parts that are decisions rather than code — the things
you cannot recover by reading the files.

## The stack

```
┌──────────────────────────────────────────────┐
│ GRUB          picks a language, sets a mode  │
├──────────────────────────────────────────────┤
│ Linux         linux-lts, no modules loaded   │
│               beyond the list /init insmods  │
├──────────────────────────────────────────────┤
│ /init         busybox sh: mounts, network,   │
│               waits for input, execs us      │
├──────────────────────────────────────────────┤
│ simpleclient  one static binary, PID ~1      │
│   ├ framebuffer  mmap'd /dev/fb0             │
│   ├ inputdev     /dev/input/event*           │
│   ├ scanner      TCP 3389 across the subnet  │
│   ├ ui           render loop + screens       │
│   └ rdp          grdp, patched               │
└──────────────────────────────────────────────┘
```

Nothing above `simpleclient` persists state. There is no writable root: the
image boots into an initramfs held in RAM, so every boot is identical and
pulling the power is a supported way to turn it off.

## Boot

`build/init` runs as PID 1. In order:

1. Mounts `/proc`, `/sys`, `/dev`.
2. `insmod`s a fixed list of modules — framebuffer, evdev, the common NIC
   drivers, `atkbd`, `psmouse`, `usbhid`. There is no udev and no autoloading;
   a NIC outside that list is a NIC the image cannot use.
3. Waits (briefly) for a pointer device to appear. `psmouse` takes a moment to
   probe, and without the wait the kiosk can reach the UI before the mouse
   exists and come up keyboard-only. The wait is capped — a machine with no
   mouse must still boot.
4. Brings up the network with `udhcpc`, falling back to link-local.
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

Only one keyboard and one pointer are read. On a machine with both an internal
and a USB keyboard, the first match wins. `-kbd` and `-mouse` override.

### Remembered accounts

A host that connects successfully has its username and domain kept against its
address, so reopening its dialog fills them in and puts the cursor in the
password field. Only the successful attempt is recorded: an account that was
rejected is not one to offer back.

This is a map in `UIState` and nothing more. The image boots from a read-only
initramfs, so it does not survive a power cycle, and making it do so would mean
adding a writable partition to the image. The password is deliberately never
part of it.

## Discovery

`internal/scanner` opens a TCP connection to port 3389 across the subnet the
machine got from DHCP, bounded by a worker semaphore. A host that completes the
handshake is a host; nothing speaks RDP to check.

The handshake is timed and reported as the latency shown in the list, measured
before the reverse DNS lookup so a slow resolver is not charged to the host. It
never reports zero for a live host: a switched LAN answers in well under a
millisecond, and the list reads zero as "not measured" and draws nothing.

The kiosk scans **its own subnet only**. There is no box to type an address
into. A server on another subnet cannot be reached at all — see
[Known limitations](#known-limitations).

## RDP

`internal/rdp` wraps `github.com/tomatome/grdp`, which is vendored and patched.
The patches are in `build/patches/` and CI fails if they go missing:

| Patch | Why |
| --- | --- |
| `0001` | grdp has a vestigial `import "C"` that forces cgo and blocks static linking |
| `0002` | a `cliprdr` source file uses Windows-only symbols with no build tag, so the package does not compile for Linux |
| `0003` | the client's close path is not exported, so a session cannot be torn down cleanly |

Incoming bitmap updates are decoded and written straight into the framebuffer.
Once a session is live the UI stops drawing entirely — RDP frames go to
`/dev/fb0` directly, and `Render` returns early for `ScreenSession`.

## Known limitations

These are design boundaries, not bugs waiting to be filed:

- **Secure Boot is not supported.** `BOOTX64.EFI` comes from `grub-mkrescue`
  and is unsigned. A machine enforcing Secure Boot refuses the stick and
  reports nothing, which looks exactly like an empty drive.
- **No RemoteFX/GFX codecs.** grdp ignores surface commands; servers that send
  only those show nothing. Every Windows server tested falls back to legacy
  bitmap updates.
- **Colour depth is whatever the server picks.** Windows commonly negotiates
  16bpp, which is what the tile decoder is tuned for.
- **Resolution follows the framebuffer** the kernel hands over. There is no
  override, and the session opens at that size.
- **No manual host entry**, no clipboard, no audio, no drive redirection.
