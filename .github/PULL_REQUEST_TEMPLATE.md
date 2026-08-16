# What this changes

<!-- What it does and why. If it fixes a bug, say what the bug actually was —
     that sentence usually belongs in a test comment too. -->

## How it was verified

<!-- Not "tests pass" — CI says that. What did you actually run?

     Tick what applies, and say what you saw. -->

- [ ] `go test -race ./...`
- [ ] Screens regenerated and reviewed — `docs/screenshots/` committed
- [ ] Booted in QEMU: `--headless` / `--usb` / `--uefi` / `--usb --uefi`
- [ ] Booted on real hardware
- [ ] Tested against a real RDP server (`TestLiveConnect`)

> A CD-ROM boot under BIOS proves nothing about a USB stick under UEFI: they use
> different structures entirely. If you touched the boot path, `build/init`,
> `grub.cfg` or the Dockerfile, run all four combinations.

## What is still untested

<!-- Be specific. Secure Boot is never covered by anything here. Neither is any
     hardware you do not have. Saying so is more useful than leaving it blank. -->
