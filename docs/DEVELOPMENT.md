# Development

Everything here runs from the repository root.

## Requirements

- Go 1.23+
- Docker, for the ISO (the kernel, initramfs and ISO are all produced inside the
  build container, so the result is identical on macOS, Linux and CI)
- QEMU, to boot what you built
- On macOS, `brew install qemu` also supplies the UEFI firmware the boot tests
  need

## The loop

```sh
make -f build/Makefile test     # go test
make -f build/Makefile vet      # go vet
make -f build/Makefile binary   # -> dist/simpleclient (static, linux/amd64)
make -f build/Makefile iso      # -> dist/simpleclient.iso
```

`bash setup.sh` runs vet, race tests and the static build in one go. Add
`--iso` to build the image, `--headless` to also boot it and capture a
screenshot.

The binary only runs on Linux — it opens `/dev/fb0` and `/dev/input/event*`.
`cmd/simpleclient/main_other.go` is a stub so `go build ./...` and `go vet ./...`
still work on a developer's machine.

## Working on the UI without booting anything

`TestScreenshots` renders every screen to PNG. This is the fastest way to see a
change, and `docs/screenshots/` holds the committed set:

```sh
SIMPLECLIENT_UI_SHOTS=$PWD/docs/screenshots \
go test -mod=vendor ./internal/ui -run TestScreenshots -count=1
```

The path has to be absolute. The test runs with `internal/ui` as its working
directory, so a relative one silently writes there instead.

Regenerate them in place whenever you change anything that draws, and commit the
result: a rendering change is then reviewable as a diff of the pictures rather
than of the arithmetic behind them. `00-logo.png` blows the mark up 12× for
pixel-level review; `09-640x480.png` is the narrowest panel this boots on.

## Booting what you built

```sh
bash build/test-qemu.sh                  # interactive window
bash build/test-qemu.sh --headless       # boot, screenshot, exit
```

**Run all four combinations before trusting an image on real hardware:**

```sh
bash build/test-qemu.sh --headless
bash build/test-qemu.sh --headless --usb
bash build/test-qemu.sh --headless --uefi
bash build/test-qemu.sh --headless --usb --uefi
```

The default attaches the ISO to an emulated CD-ROM and boots it with SeaBIOS,
which exercises El Torito and legacy BIOS. But the image ships on a USB stick,
and most machines it goes into are UEFI — those use different structures
entirely (the hybrid MBR/GPT, the EFI system partition, `BOOTX64.EFI`). A green
CD-ROM run says nothing about them. This is not hypothetical: the CD-ROM run was
once treated as proof the stick would boot, and it never was.

Secure Boot is still not covered. The firmware here enrols no keys, so it never
enforces, and `BOOTX64.EFI` is unsigned.

### Giving the guest something to find

On its own the guest sits on QEMU's private `10.0.2.0/24` and finds nothing.

```sh
bash build/test-qemu.sh --fake-rdp                 # appears in the list, will not connect
bash build/test-qemu.sh --proxy-rdp=10.0.0.5:3389  # a real server, bridged in as 10.0.2.2
```

`--proxy-rdp` exists because the kiosk only scans its own subnet, and the
guest's subnet is QEMU's, not yours.

### Driving the keyboard

Detection and reading are unit-tested, but nothing about "does a keypress reach
the UI" is. To check it end to end, boot with a QEMU monitor socket and send a
key — `F2` is the best probe because switching language redraws the whole
screen:

```sh
qemu-system-x86_64 -m 1024 -machine q35 \
  -drive if=pflash,format=raw,unit=0,readonly=on,file=<OVMF_CODE.fd> \
  -drive if=pflash,format=raw,unit=1,file=<writable copy of OVMF_VARS.fd> \
  -drive if=none,id=usb,format=raw,readonly=on,file=dist/simpleclient.iso \
  -device qemu-xhci -device usb-storage,drive=usb,bootindex=0 \
  -display none -serial file:/tmp/serial.log \
  -monitor unix:/tmp/mon,server,nowait -no-reboot &

# once it has booted
printf 'sendkey f2\n'                | nc -U /tmp/mon
printf 'screendump /tmp/after.ppm\n' | nc -U /tmp/mon
```

## Testing against a real RDP server

Everything in `internal/rdp` is unit-tested against synthetic tiles except the
handshake. `TestLiveConnect` drives a real server and is skipped unless pointed
at one:

```sh
SIMPLECLIENT_RDP_ADDR=10.0.0.5:3389 \
SIMPLECLIENT_RDP_USER=administrator \
SIMPLECLIENT_RDP_PASS=... \
SIMPLECLIENT_RDP_SHOT=/tmp/desktop.png \
go test -mod=vendor ./internal/rdp -run TestLiveConnect -v
```

`SIMPLECLIENT_RDP_SHOT` composites the incoming tiles into a PNG. That is the
only way to confirm the decode is right: a session can be live and still send
nothing this decoder understands, and that looks exactly like a black screen.

## What CI enforces

Match these locally before pushing — `.github/workflows/ci.yml` is the source of
truth:

```sh
test -f vendor/github.com/tomatome/grdp/plugin/cliprdr/stub_notwindows.go
! grep -q '^import "C"' vendor/github.com/tomatome/grdp/plugin/channel.go
gofmt -l cmd internal            # must print nothing
go vet -mod=vendor ./...
go test -mod=vendor -race ./... -timeout 120s
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags '-s -w' -o /tmp/sc ./cmd/simpleclient
file /tmp/sc | grep 'statically linked'
```

The ISO is built on every push to `main` too — a broken Dockerfile should not
first surface at release time.

## The vendor directory

`vendor/` is committed, which is unusual. It has to be: `tomatome/grdp` does not
compile for Linux as published. See [ARCHITECTURE.md](ARCHITECTURE.md#rdp) for
what the three patches fix.

After changing a dependency:

```sh
bash build/vendor-patch.sh   # go mod vendor + re-apply patches + verify
```

CI fails if the patches go missing.

## Releasing

Tagging publishes an ISO built by CI, not one built on your machine:

```sh
git tag -a v0.3.2 -F -   # write the note on stdin
git push origin v0.3.2
```

`.github/workflows/release.yml` builds the image, checks it is really ISO 9660,
writes `SHA256SUMS` and attaches both to the release.

Verify the published artifact rather than trusting the green tick — download it,
check the checksum, and unpack the initramfs to confirm the change is actually
inside:

```sh
gh release download v0.3.2 --repo diggyen/SimpleClient --dir /tmp/rel
cd /tmp/rel && shasum -a 256 -c SHA256SUMS
```

## Conventions

- **Comments explain why, not what.** The code says what it does. A comment
  earns its place by recording a decision, a constraint or a trap — especially
  one discovered the hard way.
- **Tests carry the bug in their name and comment.** Several tests here exist
  because something failed on hardware that every green run had missed; the
  comment says what that was, so nobody quietly "simplifies" the case away.
- **No new dependencies without a reason that survives `vendor/`.** Every
  addition is a tree that has to compile statically for Linux and be carried in
  the repository.
