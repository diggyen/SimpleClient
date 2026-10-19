# SimpleClient build container — produces a bootable hybrid BIOS/UEFI ISO.
#
#   docker build --target export --output type=local,dest=dist -f build/Dockerfile .
#
# The kernel comes from Alpine's linux-lts package rather than being compiled
# from source: a source build costs 30-40 minutes of CI time and only covers the
# hardware whose drivers were configured in, whereas the distro kernel ships a
# broad module set that actually boots on real machines.
#
# The Go binary is fully static (CGO_ENABLED=0). That works because vendor/
# carries a patch removing a vestigial `import "C"` from grdp/plugin — see
# build/patches/.

ARG ALPINE_VERSION=3.20

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

# ── Kernel + initramfs ────────────────────────────────────────────────────────
FROM --platform=linux/amd64 alpine:${ALPINE_VERSION} AS rootfs-builder

RUN apk add --no-cache linux-lts busybox-static kmod cpio gzip

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
 && cp /boot/vmlinuz-lts /out/vmlinuz \
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

# Alpine's kernel builds most drivers as modules, so the initramfs has to carry
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
FROM --platform=linux/amd64 alpine:${ALPINE_VERSION} AS iso-builder

# grub-bios is what makes the image bootable on legacy/CSM machines; grub-efi
# covers UEFI. grub-mkrescue emits a hybrid image when both are installed.
RUN apk add --no-cache grub grub-bios grub-efi xorriso mtools dosfstools

COPY --from=rootfs-builder /out/vmlinuz /iso/boot/vmlinuz
COPY --from=rootfs-builder /out/initramfs.cpio.gz /iso/boot/initramfs.cpio.gz
COPY build/grub.cfg /iso/boot/grub/grub.cfg

RUN mkdir -p /out \
 && grub-mkrescue -o /out/simpleclient.iso /iso -- -volid SIMPLECLIENT \
 && ls -lh /out/simpleclient.iso

# ── Export ────────────────────────────────────────────────────────────────────
FROM scratch AS export
COPY --from=go-builder /out/simpleclient /
COPY --from=rootfs-builder /out/initramfs.cpio.gz /
COPY --from=rootfs-builder /out/vmlinuz /
COPY --from=iso-builder /out/simpleclient.iso /
