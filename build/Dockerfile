# SimpleClient build container — produces a bootable hybrid BIOS/UEFI ISO.
#
#   docker build --target export --output type=local,dest=dist -f build/Dockerfile .
#
# The kernel is Debian's signed one rather than compiled from source: a source
# build costs 30-40 minutes of CI time, only covers the hardware whose drivers
# were configured in, and could not boot under Secure Boot; the distro kernel
# ships a broad module set that actually boots on real machines.
#
# The Go binary is fully static (CGO_ENABLED=0). That works because vendor/
# carries a patch removing a vestigial `import "C"` from grdp/plugin — see
# build/patches/.

# 3.24: supported until mid-2028, and its linux-lts (6.18) knows the GPUs, NICs
# and audio codecs of current machines while still running the old ones.
ARG ALPINE_VERSION=3.24
ARG DEBIAN_VERSION=trixie

# ── Go binary ─────────────────────────────────────────────────────────────────
FROM --platform=linux/amd64 golang:1.23-alpine AS go-builder

# Passed in rather than derived here: .git is not in the build context, so the
# tag can only be known on the host.
ARG VERSION=dev

WORKDIR /src

# vendor/ is committed, so there is nothing to download and no network needed.
COPY go.mod go.sum ./
COPY vendor/ ./vendor/
COPY cmd/ ./cmd/
COPY internal/ ./internal/

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -mod=vendor \
      -ldflags "-s -w -X github.com/diggyen/SimpleClient/internal/version.Version=${VERSION}" \
      -o /out/simpleclient ./cmd/simpleclient

# ── Kernel and the Secure Boot chain ──────────────────────────────────────────
# The kernel, its modules and the UEFI boot loaders come from Debian, signed:
# shim (signed by Microsoft) loads GRUB (signed by Debian), which loads the
# kernel (signed by Debian) — the chain Debian's own install media boot with.
# So the kiosk boots with Secure Boot on, out of the box, on any machine that
# trusts Microsoft's UEFI CA, which is every PC sold with Windows; with Secure
# Boot off it boots the same way. Under Secure Boot the kernel loads only
# signed modules, and Debian's are signed.
#
# Debian stable's kernel is an LTS series kept up to date for the release's
# whole life. The userland stays Alpine's; a kernel does not care whose libc
# runs above it.
#
# Packages are unpacked with dpkg-deb rather than installed: their maintainer
# scripts would run grub-install and build an initramfs inside the container.
FROM --platform=linux/amd64 debian:${DEBIAN_VERSION} AS boot-builder
ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
        grub-pc-bin grub-common xorriso mtools dosfstools kmod xz-utils zstd \
 && mkdir -p /deb /signed && cd /deb \
 && KPKG=$(apt-cache depends linux-image-amd64 | awk '$1 == "Depends:" && $2 ~ /^linux-image-[0-9]/ { print $2; exit }') \
 && echo "kernel package: $KPKG" \
 && apt-get download "$KPKG" shim-signed shim-helpers-amd64-signed grub-efi-amd64-signed \
 && for p in *.deb; do dpkg-deb -x "$p" /signed; done \
 && rm -rf /var/lib/apt/lists/*

# Modules go from Debian's compression to gzip, the one busybox's modprobe
# reads. A module's signature is part of the uncompressed file, so it survives.
RUN set -e; \
    M=$(ls -d /signed/usr/lib/modules/* /signed/lib/modules/* 2>/dev/null | head -1); \
    KVER=$(basename "$M"); echo "kernel: $KVER"; \
    mkdir -p /kernel/lib/modules; mv "$M" /kernel/lib/modules/; \
    cp "/signed/boot/vmlinuz-$KVER" /kernel/vmlinuz; \
    cd "/kernel/lib/modules/$KVER"; \
    find . -name '*.ko.xz' -exec xz -d {} +; \
    find . -name '*.ko.zst' -exec zstd -q -d --rm {} +; \
    find . -name '*.ko' -exec gzip -9 {} +; \
    depmod -b /kernel "$KVER"; \
    ls -l /signed/usr/lib/shim /signed/usr/lib/grub/x86_64-efi-signed

# ── Initramfs ─────────────────────────────────────────────────────────────────
FROM --platform=linux/amd64 alpine:${ALPINE_VERSION} AS rootfs-builder

RUN apk add --no-cache busybox-static kmod cpio gzip
COPY --from=boot-builder /kernel/lib/modules /lib/modules

# NIC firmware. Most onboard NICs need none, but these do: bnx2/bnx2x (HP
# ProLiant and many Dell servers' Broadcom NetXtreme II) do not come up at all
# without it, and r8169 (the Realtek on most desktops and thin clients) loads
# per-chip PHY fixes from rtl_nic/ without which some revisions link at 10/100
# or drop the link under load. The kernel loads firmware straight from
# /lib/firmware; there is no udev here to be involved.
RUN apk add --no-cache linux-firmware-bnx2 linux-firmware-bnx2x \
        linux-firmware-rtl_nic linux-firmware-tigon linux-firmware-e100

# Sound. The kiosk plays and records through ALSA's own tools: their plughw
# layer converts rate, channels and format to whatever a codec accepts, which
# hand-rolled PCM ioctls would have to reimplement for every card. They are
# dynamically linked against musl and alsa-lib, so those come along, as does
# alsa-lib's configuration — without /usr/share/alsa it cannot open "plughw"
# at all — and alsactl's init database, which sets sane mixer defaults.
#
# Speakers, the headphone jack, a monitor's speakers over HDMI/DisplayPort, USB
# headsets and microphones: the kernel side is the HD Audio, USB audio and
# HDMI codec drivers under kernel/sound, copied below.
RUN apk add --no-cache alsa-utils alsa-lib
RUN set -e; mkdir -p /rootfs/usr/bin /rootfs/lib /rootfs/usr/lib /rootfs/usr/share; \
    for b in aplay arecord amixer alsactl; do \
        cp -a "$(command -v $b)" /rootfs/usr/bin/; \
        for l in $(ldd "$(command -v $b)" | awk '/=>/ {print $3} /ld-musl/ {print $1}'); do \
            [ -f "$l" ] || continue; \
            mkdir -p "/rootfs$(dirname "$l")"; \
            cp -L "$l" "/rootfs$l"; \
        done; \
    done; \
    cp -a /usr/share/alsa /rootfs/usr/share/; \
    ls -l /rootfs/usr/bin /rootfs/lib /rootfs/usr/lib

COPY --from=go-builder /out/simpleclient /rootfs/sbin/simpleclient
COPY build/init /rootfs/init
COPY build/udhcpc-default.script /rootfs/usr/share/udhcpc/default.script

RUN mkdir -p /out \
 && mkdir -p /rootfs/proc /rootfs/sys /rootfs/dev /rootfs/tmp /rootfs/etc /rootfs/bin \
 && chmod +x /rootfs/init /rootfs/sbin/simpleclient /rootfs/usr/share/udhcpc/default.script

# Busybox provides every userspace tool /init needs (ip, udhcpc, modprobe, ...).
#
# The applet symlinks are created by hand rather than with `busybox --install -s`:
# that resolves the target from /proc/self/exe, so it would point every applet at
# /rootfs/bin/busybox — a path that does not exist once the initramfs is the root
# filesystem. The kernel then fails to exec /init with ENOENT because it cannot
# find the #!/bin/sh interpreter, and panics with "No working init found".
RUN cp /bin/busybox.static /rootfs/bin/busybox \
 && for applet in $(/rootfs/bin/busybox --list); do \
        ln -sf /bin/busybox "/rootfs/bin/$applet"; \
    done \
 && test -L /rootfs/bin/sh \
 && test "$(readlink /rootfs/bin/sh)" = "/bin/busybox"

# Debian's kernel builds most drivers as modules, so the initramfs has to carry
# the ones /init loads. Copying the whole tree would add ~200 MB; these subtrees
# cover NIC, framebuffer/DRM, input and HID/USB, which is everything a
# framebuffer kiosk touches.
#
# kernel/net is not optional: af_packet lives there, and busybox udhcpc opens an
# AF_PACKET socket to send its DHCP discover. Without it every boot silently
# falls back to a link-local address and the network scan finds nothing.
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
RUN KVER=$(basename /lib/modules/*) \
 && echo "kernel: $KVER" \
 && mkdir -p "/rootfs/lib/modules/$KVER/kernel/drivers" \
 && for d in net gpu input hid usb virtio char i2c cpufreq; do \
        if [ -d "/lib/modules/$KVER/kernel/drivers/$d" ]; then \
            cp -a "/lib/modules/$KVER/kernel/drivers/$d" "/rootfs/lib/modules/$KVER/kernel/drivers/"; \
        fi; \
    done \
 && cp -a "/lib/modules/$KVER/kernel/net" "/rootfs/lib/modules/$KVER/kernel/" \
 && cp -a "/lib/modules/$KVER/kernel/sound" "/rootfs/lib/modules/$KVER/kernel/" \
 && for f in modules.builtin modules.builtin.modinfo modules.order; do \
        if [ -f "/lib/modules/$KVER/$f" ]; then \
            cp -a "/lib/modules/$KVER/$f" "/rootfs/lib/modules/$KVER/"; \
        fi; \
    done \
 && rm -rf "/rootfs/lib/modules/$KVER/kernel/drivers/net/wireless" \
           "/rootfs/lib/modules/$KVER/kernel/net/wireless" \
           "/rootfs/lib/modules/$KVER/kernel/net/mac80211" \
           "/rootfs/lib/modules/$KVER/kernel/net/bluetooth" \
 && cd "/lib/modules/$KVER" \
 && (cd "/rootfs/lib/modules/$KVER" && find kernel -name '*.ko*') > /tmp/have \
 && awk -F': ?' 'NR == FNR { have[$1] = 1; next } \
        ($1 in have) { n = split($2, d, " "); for (i = 1; i <= n; i++) if (!(d[i] in have)) print d[i] }' \
        /tmp/have modules.dep | sort -u > /tmp/need \
 && while read -r m; do \
        mkdir -p "/rootfs/lib/modules/$KVER/$(dirname "$m")"; \
        cp -a "$m" "/rootfs/lib/modules/$KVER/$m"; \
    done < /tmp/need \
 && echo "dependencies outside the copied trees: $(wc -l < /tmp/need)" && cat /tmp/need \
 && cd / \
 && mkdir -p /rootfs/lib/firmware \
 && for d in bnx2 bnx2x rtl_nic tigon e100; do \
        if [ -d "/lib/firmware/$d" ]; then cp -a "/lib/firmware/$d" /rootfs/lib/firmware/; fi; \
    done \
 && du -sh /rootfs/lib/firmware \
 && depmod -b /rootfs "$KVER" \
 && du -sh /rootfs/lib/modules

RUN cd /rootfs && find . | cpio -oH newc | gzip -9 > /out/initramfs.cpio.gz \
 && ls -lh /out/initramfs.cpio.gz

# ── ISO ───────────────────────────────────────────────────────────────────────
# One image that boots four ways: BIOS/CSM and UEFI, each from a CD or from a
# USB stick written with dd. The layout and the xorriso call are grub-mkrescue's,
# done by hand because grub-mkrescue only knows how to make an unsigned EFI
# image:
#   BIOS     GRUB i386-pc as an El Torito image, and boot_hybrid.img as the
#            MBR boot code when the ISO is a disk.
#   UEFI     a FAT image holding shim as \EFI\boot\bootx64.efi, with Debian's
#            signed CD GRUB next to it as grubx64.efi — the name shim loads. It
#            is both the El Torito EFI image and a GPT partition, for the CD and
#            the USB case. That GRUB finds the medium by /.disk/info and reads
#            /boot/grub/grub.cfg, the same file the BIOS GRUB reads.
FROM boot-builder AS iso-builder

COPY --from=rootfs-builder /out/initramfs.cpio.gz /iso/boot/initramfs.cpio.gz
COPY build/grub.cfg /iso/boot/grub/grub.cfg

RUN set -e; \
    cp /kernel/vmlinuz /iso/boot/vmlinuz; \
    mkdir -p /iso/.disk /iso/boot/grub/i386-pc /iso/boot/grub/fonts /efi/EFI/boot /out; \
    echo "SimpleClient" > /iso/.disk/info; \
    cp /usr/lib/grub/i386-pc/*.mod /usr/lib/grub/i386-pc/*.lst /iso/boot/grub/i386-pc/; \
    cp /usr/share/grub/unicode.pf2 /iso/boot/grub/fonts/; \
    grub-mkimage -O i386-pc-eltorito -p /boot/grub -o /iso/boot/grub/i386-pc/eltorito.img biosdisk iso9660; \
    cp /signed/usr/lib/shim/shimx64.efi.signed /efi/EFI/boot/bootx64.efi; \
    cp /signed/usr/lib/shim/mmx64.efi.signed /efi/EFI/boot/mmx64.efi; \
    cp /signed/usr/lib/grub/x86_64-efi-signed/gcdx64.efi.signed /efi/EFI/boot/grubx64.efi; \
    cp -r /efi/EFI /iso/; \
    mkfs.vfat -C /iso/boot/efi.img 8192 >/dev/null; \
    mcopy -s -i /iso/boot/efi.img /efi/EFI ::/; \
    xorriso -as mkisofs -r -J -joliet-long -V SIMPLECLIENT -o /out/simpleclient.iso \
        -b boot/grub/i386-pc/eltorito.img -no-emul-boot -boot-load-size 4 -boot-info-table \
        --grub2-boot-info --grub2-mbr /usr/lib/grub/i386-pc/boot_hybrid.img \
        -apm-block-size 2048 \
        --efi-boot boot/efi.img -efi-boot-part --efi-boot-image \
        --protective-msdos-label \
        /iso; \
    ls -lh /out/simpleclient.iso

# ── Export ────────────────────────────────────────────────────────────────────
FROM scratch AS export
COPY --from=go-builder /out/simpleclient /
COPY --from=rootfs-builder /out/initramfs.cpio.gz /
COPY --from=boot-builder /kernel/vmlinuz /
COPY --from=iso-builder /out/simpleclient.iso /
