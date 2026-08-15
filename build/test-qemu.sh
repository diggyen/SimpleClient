#!/usr/bin/env bash
# test-qemu.sh — boot dist/simpleclient.iso in QEMU.
#
#   bash build/test-qemu.sh                     interactive window
#   bash build/test-qemu.sh --headless          boot, screenshot, exit
#   bash build/test-qemu.sh --fake-rdp
#           listen on host port 3389 so the guest's network scan finds a host at
#           10.0.2.2 (QEMU user-mode networking maps the host there). The
#           listener accepts and hangs up, so it appears in the list but will
#           not complete a session.
#   bash build/test-qemu.sh --proxy-rdp=10.0.0.5:3389
#           forward host port 3389 to a real RDP server. The guest sits on
#           QEMU's private 10.0.2.0/24 and only scans its own subnet, so a
#           server elsewhere on your network can never be discovered directly;
#           this bridges it in as 10.0.2.2 for a full click-through demo.
#   bash build/test-qemu.sh --entry=1           boot the Turkish menu entry
#   bash build/test-qemu.sh --usb               attach the ISO as a USB disk
#   bash build/test-qemu.sh --uefi              boot via UEFI firmware, not BIOS
#
# The headless mode is the end-to-end check: it proves the ISO boots, the
# framebuffer comes up and the UI actually draws, none of which unit tests cover.
#
# --usb and --uefi exist because the default here is neither of the ways this
# actually ships. A plain run attaches the ISO to an emulated CD-ROM and boots
# it with SeaBIOS, which exercises El Torito and legacy BIOS — but the image
# goes onto a USB stick, and most machines that stick goes into are UEFI. Those
# use different structures entirely (the hybrid MBR/GPT, the EFI system
# partition, BOOTX64.EFI), so a green CD-ROM run says nothing about them. Run
# all four combinations before trusting an ISO on real hardware:
#
#   bash build/test-qemu.sh --headless
#   bash build/test-qemu.sh --headless --usb
#   bash build/test-qemu.sh --headless --uefi
#   bash build/test-qemu.sh --headless --usb --uefi
#
# Secure Boot is still not covered: the firmware below enrols no keys, so it
# never enforces. GRUB's BOOTX64.EFI here is unsigned and a machine that does
# enforce will refuse it — silently, looking exactly like an empty stick.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ISO="${ROOT_DIR}/dist/simpleclient.iso"

HEADLESS=0
FAKE_RDP=0
USB=0
UEFI=0
PROXY_TARGET=""
# Which GRUB menu entry to boot: 0 = English, 1 = Turkish, 2 = verbose debug.
ENTRY=0
# Overridable, but the default has to follow the firmware: OVMF spends roughly
# 20s enumerating devices before it even reaches the boot entry, so the BIOS
# default times out mid-boot under --uefi and reports a working image as broken.
BOOT_WAIT_DEFAULT=45
SHOT="${SHOT:-${ROOT_DIR}/dist/qemu-screenshot.png}"
SERIAL_LOG="${SERIAL_LOG:-${ROOT_DIR}/dist/qemu-serial.log}"

for arg in "$@"; do
    case "$arg" in
        --headless)    HEADLESS=1 ;;
        --fake-rdp)    FAKE_RDP=1 ;;
        --proxy-rdp=*) PROXY_TARGET="${arg#--proxy-rdp=}" ;;
        --entry=*)     ENTRY="${arg#--entry=}" ;;
        --usb)         USB=1 ;;
        --uefi)        UEFI=1 ;;
        *) echo "unknown option: $arg" >&2; exit 2 ;;
    esac
done

if [ ! -f "$ISO" ]; then
    echo "ERROR: $ISO not found. Build it first: make -f build/Makefile iso" >&2
    exit 1
fi

HELPER_PID=""
MON=""

cleanup() {
    [ -n "${QEMU_PID:-}" ] && kill "$QEMU_PID" 2>/dev/null
    [ -n "$HELPER_PID" ] && kill "$HELPER_PID" 2>/dev/null
    [ -n "$MON" ] && rm -f "$MON"
    [ -n "${OVMF_VARS_COPY:-}" ] && rm -f "$OVMF_VARS_COPY"
    return 0
}
trap cleanup EXIT INT TERM

# ── Host-side RDP helper ──────────────────────────────────────────────────────
start_fake_rdp() {
    # Accept and immediately hang up: the scanner only checks that the TCP
    # handshake completes on port 3389.
    python3 - <<'PY' &
import socket
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", 3389))
s.listen(128)
while True:
    try:
        c, _ = s.accept()
        c.close()
    except OSError:
        break
PY
    HELPER_PID=$!
    sleep 1
    echo "fake RDP listener on host :3389 — the guest sees it as 10.0.2.2"
}

start_proxy_rdp() {
    local target="$1"
    SC_PROXY_TARGET="$target" python3 - <<'PY' &
import os
import socket
import threading

host, _, port = os.environ["SC_PROXY_TARGET"].rpartition(":")
target = (host, int(port))


def pipe(src, dst):
    try:
        while True:
            data = src.recv(65536)
            if not data:
                break
            dst.sendall(data)
    except OSError:
        pass
    finally:
        for s in (src, dst):
            try:
                s.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            s.close()


def serve(client):
    try:
        upstream = socket.create_connection(target, timeout=10)
    except OSError:
        client.close()
        return
    threading.Thread(target=pipe, args=(client, upstream), daemon=True).start()
    threading.Thread(target=pipe, args=(upstream, client), daemon=True).start()


listener = socket.socket()
listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
listener.bind(("0.0.0.0", 3389))
listener.listen(128)
while True:
    try:
        conn, _ = listener.accept()
    except OSError:
        break
    threading.Thread(target=serve, args=(conn,), daemon=True).start()
PY
    HELPER_PID=$!
    sleep 1
    echo "proxying host :3389 -> ${target} — the guest sees it as 10.0.2.2"
}

if [ -n "$PROXY_TARGET" ] && [ "$FAKE_RDP" = "1" ]; then
    echo "ERROR: --fake-rdp and --proxy-rdp are mutually exclusive" >&2
    exit 2
fi

if [ -n "$PROXY_TARGET" ]; then
    start_proxy_rdp "$PROXY_TARGET"
elif [ "$FAKE_RDP" = "1" ]; then
    start_fake_rdp
fi

# ── How the image is attached, and which firmware boots it ────────────────────

# MEDIA_ARGS presents the ISO either as a CD-ROM or as a USB mass-storage
# device. The USB form is what a written stick looks like to the firmware: it
# has to find the hybrid MBR or the GPT rather than an El Torito catalogue.
if [ "$USB" = "1" ]; then
    MEDIA_ARGS=(
        -drive "if=none,id=usbstick,format=raw,readonly=on,file=${ISO}"
        -device qemu-xhci
        -device usb-storage,drive=usbstick,bootindex=0
    )
else
    MEDIA_ARGS=(-boot d -cdrom "$ISO")
fi

FIRMWARE_ARGS=()
OVMF_VARS_COPY=""
if [ "$UEFI" = "1" ]; then
    # Package layout differs per distribution; take the first that exists.
    OVMF_CODE=""
    OVMF_VARS=""
    for pair in \
        "/opt/homebrew/share/qemu/edk2-x86_64-code.fd:/opt/homebrew/share/qemu/edk2-i386-vars.fd" \
        "/usr/local/share/qemu/edk2-x86_64-code.fd:/usr/local/share/qemu/edk2-i386-vars.fd" \
        "/usr/share/OVMF/OVMF_CODE.fd:/usr/share/OVMF/OVMF_VARS.fd" \
        "/usr/share/edk2/ovmf/OVMF_CODE.fd:/usr/share/edk2/ovmf/OVMF_VARS.fd" \
        "/usr/share/qemu/edk2-x86_64-code.fd:/usr/share/qemu/edk2-i386-vars.fd"
    do
        if [ -f "${pair%%:*}" ] && [ -f "${pair##*:}" ]; then
            OVMF_CODE="${pair%%:*}"; OVMF_VARS="${pair##*:}"; break
        fi
    done
    if [ -z "$OVMF_CODE" ]; then
        echo "ERROR: --uefi needs OVMF/edk2 firmware, which was not found." >&2
        echo "  macOS:  brew install qemu (ships it)" >&2
        echo "  Debian: apt install ovmf" >&2
        exit 1
    fi

    # The variable store is written during boot, so it cannot be the read-only
    # packaged copy. A fresh one each run also means no stale boot entry can
    # make a broken image look bootable.
    OVMF_VARS_COPY="$(mktemp /tmp/simpleclient-ovmf-vars.XXXXXX)"
    cp "$OVMF_VARS" "$OVMF_VARS_COPY"
    FIRMWARE_ARGS=(
        -machine q35
        -drive "if=pflash,format=raw,unit=0,readonly=on,file=${OVMF_CODE}"
        -drive "if=pflash,format=raw,unit=1,file=${OVMF_VARS_COPY}"
    )
    echo "firmware: UEFI ($(basename "$OVMF_CODE"))"
    BOOT_WAIT_DEFAULT=75
else
    echo "firmware: legacy BIOS (SeaBIOS)"
fi
BOOT_WAIT="${BOOT_WAIT:-$BOOT_WAIT_DEFAULT}"
[ "$USB" = "1" ] && echo "media: USB mass storage" || echo "media: CD-ROM"

# ── Interactive ───────────────────────────────────────────────────────────────
if [ "$HEADLESS" = "0" ]; then
    cat <<'EOF'
Starting SimpleClient in QEMU.

  In the guest      arrow keys select a host, Enter opens the login dialog,
                    Tab moves between fields, F5 rescans, F2 switches language,
                    Ctrl+Alt+End ends a session.
  QEMU window       Ctrl+Alt+G releases the captured mouse/keyboard,
                    Ctrl+Alt+F toggles fullscreen.
  This terminal     Ctrl+C stops the VM.

EOF
    qemu-system-x86_64 \
        -name simpleclient \
        -m 512M \
        -smp 2 \
        ${FIRMWARE_ARGS[@]+"${FIRMWARE_ARGS[@]}"} \
        ${MEDIA_ARGS[@]+"${MEDIA_ARGS[@]}"} \
        -vga std \
        -device virtio-rng-pci \
        -device e1000,netdev=net0 \
        -netdev user,id=net0 \
        -no-reboot
    exit $?
fi

# ── Headless: boot, screenshot, shut down ─────────────────────────────────────
MON="$(mktemp -u /tmp/simpleclient-mon.XXXXXX)"
QEMU_PID=""

mkdir -p "$(dirname "$SHOT")"
rm -f "$SHOT" "$SERIAL_LOG"

echo "booting $ISO headless (waiting ${BOOT_WAIT}s)..."
qemu-system-x86_64 \
    -name simpleclient-headless \
    -m 512M \
    -smp 2 \
    ${FIRMWARE_ARGS[@]+"${FIRMWARE_ARGS[@]}"} \
    ${MEDIA_ARGS[@]+"${MEDIA_ARGS[@]}"} \
    -vga std \
    -display none \
    -monitor "unix:${MON},server,nowait" \
    -serial "file:${SERIAL_LOG}" \
    -device virtio-rng-pci \
    -device e1000,netdev=net0 \
    -netdev user,id=net0 \
    -no-reboot &
QEMU_PID=$!

# Drive the GRUB menu when a non-default entry was asked for. The menu times out
# after 3s, so this has to land inside that window.
if [ "$ENTRY" != "0" ]; then
    sleep 2
    {
        sleep 0.3
        i=0
        while [ "$i" -lt "$ENTRY" ]; do echo "sendkey down"; sleep 0.2; i=$((i + 1)); done
        echo "sendkey ret"
        sleep 0.5
    } | nc -U "$MON" > /dev/null
    echo "selected GRUB entry $ENTRY"
fi

sleep "$BOOT_WAIT"

if ! kill -0 "$QEMU_PID" 2>/dev/null; then
    echo "ERROR: QEMU exited before the screenshot could be taken" >&2
    echo "--- serial log (${SERIAL_LOG}) ---" >&2
    tail -40 "$SERIAL_LOG" >&2 2>/dev/null
    exit 1
fi

# QEMU's HMP prints a banner first, so give it a moment before writing.
{ sleep 0.5; echo "screendump ${SHOT} -f png"; sleep 2; } | nc -U "$MON" > /dev/null

if [ ! -s "$SHOT" ]; then
    echo "ERROR: screenshot was not written to $SHOT" >&2
    exit 1
fi

echo "screenshot: $SHOT"
file "$SHOT"
echo "serial log: $SERIAL_LOG"
grep -a 'simpleclient' "$SERIAL_LOG" 2>/dev/null | tail -10
