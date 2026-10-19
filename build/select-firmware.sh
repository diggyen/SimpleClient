#!/bin/sh
# select-firmware.sh DEST MODULES... — copy into DEST/lib/firmware the firmware
# the given kernel modules load, from /lib/firmware.
#
# A driver names the files it loads in its modinfo (firmware: lines), so the
# wireless drivers the kiosk carries say exactly which of linux-firmware's
# files they can use: a few dozen of the thousands in the packages. Taking
# whole vendor folders instead would carry every chip of every generation —
# the Intel folder alone is 88 MB — for every boot to read off the medium.
#
# Three things the names leave out are added by rule:
#   iwlwifi names the newest firmware it speaks (iwlwifi-<chip>-<API>.ucode)
#                and falls back to older ones, so when that exact one is not
#                in linux-firmware the newest it has below it is taken; the
#                same goes for any name ending in -<number>.ucode or .bin.
#   iwlwifi's .pnvm files, platform data the newer chips need beside the
#                firmware, which it does not name.
#   a name with a wildcard (ath11k/WCN6855/hw2.0/*) is a folder of files.
#
# And some are left out: the drivers of PC cards also drive the chips of
# access points and routers (Qualcomm's QCN, IPQ and QCA99xx, MediaTek's
# Wi-Fi 6/7 AP chips), and Broadcom's SDIO chips, which on a PC need board
# files linux-firmware does not have.
#
# Files in linux-firmware are zstd- or xz-compressed (name.zst), which the
# kernel reads; the names in modinfo are without the suffix.
set -e
dest=$1/lib/firmware
shift
FW=/lib/firmware
mkdir -p "$dest"

take() {
    for f in "$FW/$1" "$FW/$1.zst" "$FW/$1.xz"; do
        if [ -e "$f" ]; then
            mkdir -p "$dest/$(dirname "$1")"
            cp -L "$f" "$dest/${f#"$FW"/}"
            return 0
        fi
    done
    return 1
}

# older NAME: the newest firmware below the version NAME asks for, if NAME
# ends in -<number>.<ext> and some older one is there.
older() {
    base=${1%-*} tail=${1##*-}
    ver=${tail%%.*} ext=${tail#*.}
    case "$ver" in '' | *[!0-9]*) return 1 ;; esac
    v=$((ver - 1))
    while [ "$v" -ge 0 ] && [ $((ver - v)) -le 40 ]; do
        if take "$base-$v.$ext"; then return 0; fi
        v=$((v - 1))
    done
    return 1
}

for m in "$@"; do
    modinfo -F firmware "$m" 2>/dev/null
done | grep -v -E 'QCN|IPQ|QCA99|QCA4019|QCA9888|WCN3990|mt79(15|16|81|86|88|92|96)|brcm/.*-sdio|cypress/.*-sdio' |
    sort -u > /tmp/fw-names

missing=0
while read -r name; do
    case "$name" in
        *'*'* | *'?'*)
            for f in $FW/$name; do
                [ -f "$f" ] || continue
                r=${f#"$FW"/}
                mkdir -p "$dest/$(dirname "$r")"
                cp -L "$f" "$dest/$r"
            done
            ;;
        *)
            take "$name" || older "$name" || missing=$((missing + 1))
            ;;
    esac
done < /tmp/fw-names
for f in "$FW"/iwlwifi-*.pnvm* "$FW"/intel/iwlwifi/iwlwifi-*.pnvm*; do
    if [ -e "$f" ]; then
        r=${f#"$FW"/}
        mkdir -p "$dest/$(dirname "$r")"
        cp -L "$f" "$dest/$r"
    fi
done

echo "firmware: $(wc -l < /tmp/fw-names) names, $missing not in linux-firmware, $(du -sh "$dest" | cut -f1)"
