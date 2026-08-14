#!/usr/bin/env bash
# test-qemu.sh — boot dist/simpleclient.iso in QEMU.
#
#   bash build/test-qemu.sh                     interactive window
#   bash build/test-qemu.sh --headless          boot, screenshot, exit
#   bash build/test-qemu.sh --headless --fake-rdp
#           also listen on host port 3389 so the guest's network scan finds a
#           host at 10.0.2.2 (QEMU user-mode networking maps the host there)
#
# The headless mode is the end-to-end check: it proves the ISO boots, the
# framebuffer comes up and the UI actually draws, none of which unit tests cover.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ISO="${ROOT_DIR}/dist/simpleclient.iso"

HEADLESS=0
FAKE_RDP=0
# Which GRUB menu entry to boot: 0 = English, 1 = Turkish, 2 = verbose debug.
ENTRY=0
BOOT_WAIT="${BOOT_WAIT:-45}"
SHOT="${SHOT:-${ROOT_DIR}/dist/qemu-screenshot.png}"
SERIAL_LOG="${SERIAL_LOG:-${ROOT_DIR}/dist/qemu-serial.log}"

for arg in "$@"; do
    case "$arg" in
        --headless)  HEADLESS=1 ;;
        --fake-rdp)  FAKE_RDP=1 ;;
        --entry=*)   ENTRY="${arg#--entry=}" ;;
        *) echo "unknown option: $arg" >&2; exit 2 ;;
    esac
done

if [ ! -f "$ISO" ]; then
    echo "ERROR: $ISO not found. Build it first: make -f build/Makefile iso" >&2
    exit 1
fi

if [ "$HEADLESS" = "0" ]; then
    echo "Starting SimpleClient in QEMU..."
    echo "  Ctrl+Alt+G : grab/release mouse"
    echo "  Ctrl+Alt+F : toggle fullscreen"
    echo "  Ctrl+C here to stop QEMU"

    exec qemu-system-x86_64 \
        -name simpleclient \
        -m 512M \
        -smp 2 \
        -boot d \
        -cdrom "$ISO" \
        -vga std \
        -device e1000,netdev=net0 \
        -netdev user,id=net0 \
        -no-reboot
fi

# ── Headless: boot, screenshot, shut down ─────────────────────────────────────
MON="$(mktemp -u /tmp/simpleclient-mon.XXXXXX)"
FAKE_PID=""
QEMU_PID=""

cleanup() {
    [ -n "$QEMU_PID" ] && kill "$QEMU_PID" 2>/dev/null
    [ -n "$FAKE_PID" ] && kill "$FAKE_PID" 2>/dev/null
    rm -f "$MON"
}
trap cleanup EXIT

if [ "$FAKE_RDP" = "1" ]; then
    # Accept and immediately drop connections: the scanner only checks that the
    # TCP handshake completes on port 3389.
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
    FAKE_PID=$!
    sleep 1
    echo "fake RDP listener on host :3389 (guest sees it at 10.0.2.2) pid=$FAKE_PID"
fi

mkdir -p "$(dirname "$SHOT")"
rm -f "$SHOT" "$SERIAL_LOG"

echo "booting $ISO headless (waiting ${BOOT_WAIT}s)..."
qemu-system-x86_64 \
    -name simpleclient-headless \
    -m 512M \
    -smp 2 \
    -boot d \
    -cdrom "$ISO" \
    -vga std \
    -display none \
    -monitor "unix:${MON},server,nowait" \
    -serial "file:${SERIAL_LOG}" \
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
grep -a 'simpleclient-init' "$SERIAL_LOG" 2>/dev/null | tail -10
