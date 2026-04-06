#!/bin/bash
# test-qemu.sh — Run rdpboot.iso in QEMU for visual verification.
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ISO="${SCRIPT_DIR}/../dist/rdpboot.iso"

if [ ! -f "$ISO" ]; then
    echo "ERROR: $ISO not found. Run 'make iso' (or 'make docker-build') first."
    exit 1
fi

echo "Starting rdpboot in QEMU..."
echo "  Ctrl+Alt+G : grab/release mouse"
echo "  Ctrl+Alt+F : toggle fullscreen"
echo "  Press Ctrl+C in this terminal to stop QEMU"

qemu-system-x86_64 \
    -name rdpboot \
    -m 256M \
    -smp 2 \
    -boot d \
    -cdrom "$ISO" \
    -display sdl \
    -device e1000,netdev=net0 \
    -netdev user,id=net0,hostfwd=tcp::13389-:3389 \
    -k tr \
    -no-reboot
