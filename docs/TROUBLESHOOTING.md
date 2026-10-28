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
simpleclient: warning: input: [/dev/input/event0 /dev/input/event1]
```

Where it stops tells you which stage failed.

---

## The USB stick does not boot

The machine behaves as though the drive were empty and moves on to the next boot
device.

### 1. Is Secure Boot on?

The stick boots with Secure Boot on: its boot chain is Debian's — shim signed by
Microsoft's third-party UEFI CA, then GRUB and the kernel signed by Debian. Some
machines trust only Microsoft's Windows CA out of the box — Secured-core PCs,
some Lenovo and HP business models — and refuse it *silently*: no message, no
menu, exactly like an empty stick.

In firmware setup, allow the third-party CA (Lenovo: *Allow Microsoft 3rd Party
UEFI CA*; HP: *Enable MS UEFI CA key*), or turn Secure Boot off.

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
`Apple_HFS SIMPLECLIENT`. After `dd`, a single FAT partition, or nothing, means
the write went to the wrong place.

Written with Rufus, either mode boots. DD Image mode writes the ISO as `dd`
does. ISO Image mode makes one FAT32 partition, labelled `SIMPLECLIEN`, with
the ISO's files on it: keep FAT32, and keep the label — GRUB, the kiosk's
settings and the installer find the stick by it.

A Rufus older than 4.13 carries GRUB 2.12 and asks, in ISO Image mode, to
download the files for the ISO's GRUB 2.14: answer *Yes*. It fetches the same
core image Rufus 4.13 and later carry. *No* puts 2.12 on the stick, which
cannot load 2.14's modules, and the stick does not boot. Releases before 0.7.2
had Rufus ask about 2.12 instead, and there *Yes* made a stick that restarts
the machine as soon as GRUB starts; answer *No* with those.

Some BIOSes offer a USB stick only when its MBR has a partition marked active.
The ISO has one; a stick written with an older release in DD mode is not
listed on such a machine at all — write that one in ISO Image mode.

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

The kiosk starts on the firmware's framebuffer, at the 1024×768×32 mode GRUB
sets, and then loads the GPU's own driver (Intel i915/xe, AMD amdgpu/radeon,
NVIDIA nouveau, and the virtual GPUs), which takes the display over at its
native mode. The console says which way it went:

```
simpleclient: display adapters "8086:46a6", native graphics
[simpleclient-init] drm: card0-HDMI-A-1=connected card0-DP-1=disconnected
[simpleclient-init] fb0: 1920,1080 bpp=32 driver=i915drmfb
```

- **A driver that leaves no screen** — missing firmware, a GPU it hangs on —
  is dealt with on its own: the boot restarts (or, after a hang, the next one
  does) with that GPU left on the firmware framebuffer, `safe graphics`, and
  the USB stick or disk remembers this for that GPU — after two such boots in
  a row, since one may have been the machine switched off just then. A
  different GPU tries its driver afresh. Two may still have been the plug, so
  the footer says `Video: CPU (safe graphics)`, `F4` offers **Try driver**,
  and every tenth boot in safe graphics has the next one try the driver by
  itself. Each of these is a single try: a boot that dies again puts the GPU
  straight back.
- **Dark screen, nothing to blame** — pick **Guvenli grafik / safe graphics**
  in the boot menu, which skips the driver for that boot.
  **ekran surucusunu yeniden dene / retry display driver** forgets what was
  remembered.
- **No `fb0:` line** — neither the firmware nor a driver gave a framebuffer.
- **`fb0:` present, screen blank** — the UI is drawing somewhere you are not
  looking. Some machines with multiple outputs light the wrong one; try a
  different port, or the only display attached at boot.
- **Wrong colours or a skewed image** — the framebuffer is not 32bpp. The
  renderer assumes it. The line reports the actual depth.

NVIDIA's RTX 40 series and later always stay on the firmware framebuffer:
nouveau drives them only with NVIDIA's GSP firmware, 100 MB that is not
shipped.

---

## The keyboard does not work (the mouse does)

Read the `input:` line on the console — the nodes the kiosk reads:

```
simpleclient: warning: input: [/dev/input/event0 /dev/input/event1]
```

Every keyboard and pointer is read, built in or USB, including one plugged in
later.

- **None of them is your keyboard** — nothing on this machine was recognised
  as one. Send the `input caps:` line with a bug report; it holds exactly what
  the kernel said about every device.
- **Recognised, but typing does nothing** — pin the node: press `e` in the
  boot menu and add `kbddev=/dev/input/eventN` (and `mousedev=` likewise) to
  the `linux` line. The `input caps:` dump tells which `eventN` is the real
  keyboard: the one whose `B: KEY=` bitmap covers ordinary letters, not a power
  button or a video bus.

### Typing works on the host list but not in the credential dialog

If arrows, F2 and F5 respond but letters do not appear in the username or
password field, the keyboard is being read and the problem is elsewhere:

- **On a slow machine, before v0.3.3**, the render loop spun at 100% of a core
  once the first scan finished, and every keystroke needs a repaint to show. It
  looked exactly like a dead field. Update.
- **The number pad produced nothing before v0.3.3.** Try the digit row.
- **Check the focus.** Text only goes into a focused field — `Tab` moves between
  username, password, domain and the two buttons, and clicking a field focuses
  it. With a button focused there is nowhere for a letter to go.

> This class of bug is why the logging exists. Detection used to match the word
> "keyboard" in the device name; most real keyboards do not contain it, so the
> kiosk came up on hardware with a working mouse and a dead keyboard while every
> test passed. Devices are now classified by capability.

### The wrong characters appear

Check the left-hand cap in the top-right corner — it is the active keyboard
layout. `F3` cycles US, TR-Q and TR-F. Choosing Türkçe selects TR-Q, which is
wrong if your keyboard is a Turkish F or a US board with a Turkish UI.

Only the credential dialog is affected. Inside a session the keycode is
forwarded raw and the remote machine decides, so a session that types the wrong
characters is the remote layout, not this one.

---

## No servers are found

The kiosk probes TCP 3389 across **its own subnet only**.

1. **Check the `DHCP on eth0:` line.** If it shows a `169.254.x.x` address there
   was no DHCP server and it fell back to link-local — it is scanning a subnet
   with nothing on it.
2. **No `DHCP` line at all, and an empty `interfaces:` line**, means no
   network interface came up: the NIC has no driver in Debian's kernel, or no
   firmware in the image. A USB network adapter gets round it.
3. **A server on a different subnet** is not discovered — the scan only reaches
   the local one — but it can be added by hand: put `rdphost=<ip>` on the GRUB
   command line and it appears in the list ready to connect.
4. Confirm the target really listens: `nc -vz <ip> 3389` from another machine.

Press **F5** to rescan.

---

## The kiosk does not join the wireless network

1. **No *Wireless* row under F8.** The kiosk found no wireless interface: the
   `interfaces:` line has no `wlan0`. The card has no driver in Debian's kernel
   or no firmware in the image — the families covered are in the
   [README](../README.md#requirements). A USB stick from one of them gets round
   it.
2. **The network is not listed.** F5 scans again. A network on 5 GHz only needs
   a card that does 5 GHz; a hidden one is joined by name from the last row.
3. **"The network did not accept the password".** The password, or on an
   enterprise network the user name, is wrong. The kiosk stays on the network
   it had.
4. **Connected, but "waiting for an address".** The network let the kiosk on
   and no DHCP server answered. A guest network that wants a sign-in page in a
   browser cannot be used; on one without DHCP, set a fixed address under F8.
5. **`wireless: no address after 15 s` at boot.** The kept network was not
   there: out of range, or switched off. The kiosk starts without it and keeps
   trying; when the network appears it gets its address and scans it.

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

## The session types the wrong characters

Inside a session the key is forwarded as it is and the server applies its own
keyboard layout: the wrong characters there are the server's layout setting,
not the kiosk's. A Linux server set up with the script has the Turkish layouts,
which xrdp does not map by itself.

Only on the built-in client — which runs a session when the engine cannot
drive the display — do the arrow cluster and the navigation keys arrive as
their number-pad equivalents, and do what the server's Num Lock says.

---

## The session connects but the screen stays black

The connection is up and the server has nothing to show: its desktop did not
start. On a Linux server it is usually one of these:

- **A desktop that runs only on Wayland** — the newest GNOME, Plasma 6.8 and
  later — which xrdp cannot start. The server script says so as it runs; MATE
  or Xfce for remote sessions gets round it.
- **The same user logged in at the server's console.** GNOME and KDE in
  particular do not start a second session for a user who has one; log out
  there.
- **Anything else** is in `~/.xsession-errors` in the user's home on the
  server.

---

## The bottom line says *Video: CPU* on a machine with an Intel or AMD GPU

The kiosk tried the GPU's video decoder at start, and it did not decode the
test stream; or it never tried, as the machine has less than 1.5 GB of memory,
or the boot menu's safe graphics left the GPU's driver unloaded. The serial
console says which: `video:` gives the GPU drivers, the memory and the decoders
kept at boot, and a line for each GPU tried starts with its render node,
`sc-rdp: /dev/dri/renderD128 (i915): ...`, with what went wrong.
Sessions still work: the CPU decodes their video. A machine whose only GPU is
an NVIDIA always says *CPU*; see [the README](../README.md#linux-servers).

---

## The update does not install

The update dialog (`F9` on an installed kiosk) says what stopped it:

- **The release server cannot be reached** — the kiosk needs HTTPS (port 443)
  to `github.com` and to `release-assets.githubusercontent.com`, where GitHub
  keeps release files, and a DNS server that resolves them; it cannot go
  through a proxy. A network without that can have a mirror instead: the
  release's `SHA256SUMS` and `simpleclient-update.tar` under
  `download/<tag>/`, and a file `latest` holding the tag, on any HTTPS server
  the kiosks reach, with its address in `simpleclient/update-url` on the
  kiosk's data partition. A mirror whose certificate is from a CA of its own
  needs that CA, or the certificate itself, in `simpleclient/update-ca.pem`
  beside it, PEM.
- **The release server is not on HTTPS** — the address in `update-url` starts
  `http://`, or the mirror redirects to one. The kiosk runs what it downloads,
  and takes it over HTTPS only.
- **certificate ... is not valid** — the machine's clock is far out. A clock
  that is behind is allowed for; one so far ahead that the certificate has
  expired is not. Set the date in the firmware setup.
- **This start-up is still being checked** — the boot has to prove itself
  first, 20 seconds after the kiosk is up; until then the other slot is the
  way back, and it is not written over.
- **The download was damaged / stopped** — try again; a download that sends
  nothing for a minute is given up.

When the update installs but the new version does not start, the machine comes
back on the version it had, and the server list says so in amber. The release
stays on offer, but is not announced again. The serial console of the boot that
failed is what tells why (see the top of this page). The GRUB menu's
*SimpleClient (previous version)* boots the other slot by hand.

---

## Reporting something not covered here

Open an issue with:

- the full serial console output from the debug entry — this is the single most
  useful thing, and only you can capture it
- the version, which the kiosk prints along the bottom of the discovery screen,
  and the `sha256` of the ISO you wrote
- the machine: make/model, firmware mode (UEFI or legacy), Secure Boot state
