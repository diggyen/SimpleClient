#!/bin/sh
# select-modules.sh SRC DEST — copy the kiosk's share of the kernel modules in
# SRC/lib/modules/<version> to DEST/lib/modules/<version>, together with every
# module they depend on, and index them with depmod.
#
# Debian's kernel builds most drivers as modules, so the initramfs has to carry
# the ones /init loads. Copying the whole tree would add ~200 MB; these subtrees
# cover NIC, framebuffer/DRM, input and HID/USB, which is everything a
# framebuffer kiosk touches, plus the disk controllers (NVMe, SATA, eMMC, USB
# storage) and file systems that installing to disk and booting from it need,
# and the ones a USB drive brought to a session carries: FAT, exFAT, NTFS.
#
# Of kernel/net only af_packet, which is not optional: busybox udhcpc opens an
# AF_PACKET socket to send its DHCP discover. Without it every boot silently
# falls back to a link-local address and the network scan finds nothing. The
# rest of kernel/net — netfilter, traffic shaping, bridging, NFS, a third of
# the modules — serves no purpose on a kiosk, and every megabyte here is one
# more for an old BIOS to read off a USB stick before the kernel starts. A
# module a driver needs from there comes along with it, as below.
#
# The same goes for hardware no kiosk has: data-centre NICs (25-400G), CAN,
# WWAN, amateur radio, the USB device side, joysticks.
#
# drivers/char carries hw_random, including virtio-rng. A diskless kiosk has
# almost no entropy at boot, so getrandom() blocks and the RDP client's TLS
# handshake stops dead. random.trust_cpu=on covers real hardware via RDRAND;
# virtio-rng covers virtual machines, where RDRAND may not be available.
#
# A driver in these trees often needs a module that lives elsewhere: the HD
# Audio codec driver needs drivers/leds (ledtrig-audio), igb drivers/dca, tg3
# and every PTP-capable NIC drivers/ptp and drivers/pps, b44 drivers/ssb. With
# one missing, modprobe fails on "Unknown symbol" and the hardware is dead —
# no sound on any HD Audio board, no network on a Broadcom one. So after the
# trees, every dependency modules.dep names for a copied module is copied too,
# wherever it lives. modules.dep lists each module's dependencies transitively,
# so a single pass is complete.
set -e
src=$1
dest=$2
KVER=$(basename "$src"/lib/modules/*)
S=$src/lib/modules/$KVER
D=$dest/lib/modules/$KVER

mkdir -p "$D/kernel/drivers"
for d in net gpu input hid usb virtio char i2c cpufreq nvme ata mmc; do
    if [ -d "$S/kernel/drivers/$d" ]; then
        cp -a "$S/kernel/drivers/$d" "$D/kernel/drivers/"
    fi
done
cp -a "$S/kernel/sound" "$D/kernel/"
mkdir -p "$D/kernel/net"
cp -a "$S/kernel/net/packet" "$D/kernel/net/"
for m in sd_mod sr_mod virtio_blk vmw_pvscsi loop fat vfat exfat ntfs3 ext4 jbd2 mbcache isofs efivarfs \
         nls_ascii nls_cp437 nls_iso8859-1 nls_utf8 crc32c_generic crc32c-intel libcrc32c; do
    for f in $(find "$S/kernel" -name "$m.ko*"); do
        r=${f#"$S"/}
        mkdir -p "$D/$(dirname "$r")"
        cp -a "$f" "$D/$r"
    done
done
for f in modules.builtin modules.builtin.modinfo modules.order; do
    if [ -f "$S/$f" ]; then
        cp -a "$S/$f" "$D/"
    fi
done
N=$D/kernel/drivers/net
rm -rf "$N/wireless" "$N/can" "$N/wwan" "$N/wan" "$N/ieee802154" "$N/arcnet" "$N/fddi" \
       "$N/hamradio" "$N/plip" \
       "$N/ethernet/mellanox" "$N/ethernet/qlogic" "$N/ethernet/chelsio" "$N/ethernet/sfc" \
       "$N/ethernet/netronome" "$N/ethernet/cavium" "$N/ethernet/pensando" "$N/ethernet/huawei" \
       "$N/ethernet/brocade" "$N/ethernet/emulex" "$N/ethernet/fungible" \
       "$D/kernel/drivers/usb/gadget" "$D/kernel/drivers/usb/atm" "$D/kernel/drivers/input/joystick"

# A modules.dep without a single dependency means depmod could not read the
# modules (a compression it was built without); following it would silently
# leave out scsi_mod and the like.
if ! grep -q ': kernel/' "$S/modules.dep"; then
    echo "$S/modules.dep lists no dependencies; depmod could not read the modules" >&2
    exit 1
fi
(cd "$D" && find kernel -name '*.ko*') > /tmp/have
awk -F': ?' 'NR == FNR { have[$1] = 1; next }
    ($1 in have) { n = split($2, d, " "); for (i = 1; i <= n; i++) if (!(d[i] in have)) print d[i] }' \
    /tmp/have "$S/modules.dep" | sort -u > /tmp/need
while read -r m; do
    mkdir -p "$D/$(dirname "$m")"
    cp -a "$S/$m" "$D/$m"
done < /tmp/need
echo "$KVER: $(wc -l < /tmp/need) dependencies outside the copied trees"

depmod -b "$dest" "$KVER"
if ! grep -q '/sd_mod\.ko[^:]*: .*scsi_mod' "$D/modules.dep" &&
   ! grep -q '/scsi_mod\.ko' "$D/modules.builtin"; then
    echo "$D/modules.dep: sd_mod does not depend on scsi_mod; depmod could not read the modules" >&2
    exit 1
fi
du -sh "$D"
