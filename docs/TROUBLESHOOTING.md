# Troubleshooting

The kiosk has no shell, no logs on disk and no way to report anything except
what it draws. Almost every question below is answered by the serial console, so
start there.

## Getting the serial console

Boot the **RDP handshake debug** entry from the GRUB menu. It sends everything
to `ttyS0` and traces the RDP handshake step by step.

On real hardware you need a serial port or a USB-serial adapter and another
machine running `screen /dev/ttyUSB0 115200`. Under QEMU it is simply a file:

```sh
bash build/test-qemu.sh --headless --entry=2
cat dist/qemu-serial.log
```

A healthy boot looks like this:

```
[simpleclient-init] fb0: 1280,800 bpp=32 driver=bochs-drmdrmfb
[simpleclient-init] input: /dev/input/event0 /dev/input/event1
[simpleclient-init] input caps: N: Name="..."|H: Handlers=...|B: EV=...|B: KEY=...
[simpleclient-init] entropy: 256 hwrng=virtio_rng.0
[simpleclient-init] DHCP on eth0: inet 10.0.2.15/24
simpleclient: warning: input: keyboard=/dev/input/event0 mouse=/dev/input/event1
```

Where it stops tells you which stage failed.

---

## The USB stick does not boot

The machine behaves as though the drive were empty and moves on to the next boot
device.

### 1. Is Secure Boot on?

**This is the most likely cause.** `BOOTX64.EFI` is produced by `grub-mkrescue`
and is unsigned, so a machine enforcing Secure Boot refuses it *silently* — no
message, no menu, exactly like an empty stick.

Turn Secure Boot off in firmware setup. There is no workaround in the image;
supporting it would mean building a signed `shim` chain.

### 2. Did the write actually succeed?

On macOS, `dd` fails outright if the disk is still mounted, and writing to a
*partition* (`/dev/disk4s1`) instead of the *disk* produces a stick that looks
written and is not.

```sh
diskutil unmountDisk /dev/diskN
sudo dd if=simpleclient.iso of=/dev/rdiskN bs=4m status=progress
sync
```

`rdiskN` — not `diskN`, and never `diskNs1`. Then check what landed:

```sh
diskutil list /dev/diskN
```

You should see several partitions including an `EFI` one and
`Apple_HFS SIMPLECLIENT`. A single FAT partition, or nothing, means the write
went to the wrong place.

Confirm the file itself first:

```sh
shasum -a 256 -c SHA256SUMS
```

### 3. Firmware settings

- **Fast Boot** skips USB enumeration on many machines. Turn it off.
- USB may be absent from the boot order, or behind a separate "USB Boot" switch.
- Some firmware only offers the stick from the one-shot boot menu (often F12).

### 4. Is the machine x86-64?

The image is x86-64 only. It will not boot on ARM, and under UTM or Parallels on
an Apple Silicon Mac you must choose **Emulate**, not **Virtualize** — a
virtualised VM there is ARM, and an x86 ISO in it fails in a way that looks
identical to a bad image.

---

## It boots, but the screen is blank or garbled

Check the `fb0:` line on the console.

- **No `fb0:` line** — the kernel gave no framebuffer. The GPU has no driver in
  the `linux-lts` kernel, or firmware handed over a mode the kernel rejected.
- **`fb0:` present, screen blank** — the UI is drawing somewhere you are not
  looking. Some machines with multiple outputs light the wrong one; try a
  different port, or the only display attached at boot.
- **Wrong colours or a skewed image** — the framebuffer is not 32bpp. The
  renderer assumes it. The line reports the actual depth.

The mode comes from `build/grub.cfg` (`gfxmode`), which asks for 1024×768×32 —
the mode essentially every implementation supports — and `gfxpayload=keep` hands
it to the kernel.

---

## The keyboard does not work (the mouse does)

Read the last line of the console:

```
simpleclient: warning: input: keyboard=/dev/input/event0 mouse=/dev/input/event1
```

- **`keyboard=(none)`** — no device on this machine was recognised as a
  keyboard. Send the `input caps:` line with a bug report; it holds exactly what
  the kernel said about every device.
- **A node is named but typing does nothing** — the wrong device was chosen.
  Override it:

  Add `-kbd /dev/input/eventN` to the command in `build/init`, rebuild, and use
  the `input caps:` dump to work out which `eventN` is the real keyboard: the
  one whose `B: KEY=` bitmap covers ordinary letters, not a power button or a
  video bus.

Only one keyboard is read. On a machine with both a built-in and a USB keyboard
the first match wins, and it may not be the one you are typing on.

> This class of bug is why the logging exists. Detection used to match the word
> "keyboard" in the device name; most real keyboards do not contain it, so the
> kiosk came up on hardware with a working mouse and a dead keyboard while every
> test passed. Devices are now classified by capability.

---

## No servers are found

The kiosk probes TCP 3389 across **its own subnet only**.

1. **Check the `DHCP on eth0:` line.** If it shows a `169.254.x.x` address there
   was no DHCP server and it fell back to link-local — it is scanning a subnet
   with nothing on it.
2. **No `DHCP` line at all** means the NIC has no driver. `/init` `insmod`s a
   fixed list (`e1000`, `e1000e`, `r8169`, `igb`, `igc`, `tg3`); there is no
   autoloading. A NIC outside that list cannot be used without adding it.
3. **A server on a different subnet cannot be reached.** There is no manual
   address entry. This is a design limit, not a fault.
4. Confirm the target really listens: `nc -vz <ip> 3389` from another machine.

Press **F5** to rescan.

---

## A host is listed but connecting fails

The on-screen message narrows it:

| Message | Meaning |
| --- | --- |
| Timeout: server unreachable | The port answered during the scan but the RDP handshake got no reply. Usually a firewall that allows the SYN and drops the rest. |
| Connection refused | Nothing is listening any more — the scan result is stale. Press F5. |
| Authentication failed | Credentials or domain rejected. Try the domain field empty, then `.` for a local account. |

If the message is not enough, boot the debug entry: it prints every protocol
step, and the last one to appear is the one that failed.

---

## The session connects but the screen stays black

Almost always a codec problem, not a connection problem.

grdp ignores surface-command updates, so a server sending **only** RemoteFX/GFX
displays nothing while the session is genuinely live. Every Windows server
tested falls back to legacy bitmap updates, which work. On the server, disable
"Use advanced RemoteFX graphics" / force legacy bitmap encoding.

Colour depth is whatever the server picks; the tile decoder is tuned for the
16bpp Windows commonly negotiates.

To confirm the decoder is at fault rather than the link, point the live test at
the same server and look at the PNG it writes — see
[DEVELOPMENT.md](DEVELOPMENT.md#testing-against-a-real-rdp-server).

---

## Reporting something not covered here

Open an issue with:

- the full serial console output from the debug entry — this is the single most
  useful thing, and only you can capture it
- the version, which the kiosk prints along the bottom of the discovery screen,
  and the `sha256` of the ISO you wrote
- the machine: make/model, firmware mode (UEFI or legacy), Secure Boot state
