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

WORKDIR /src

# vendor/ is committed, so there is nothing to download and no network needed.
COPY go.mod go.sum ./
COPY vendor/ ./vendor/
COPY cmd/ ./cmd/
COPY internal/ ./internal/

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -mod=vendor -ldflags '-s -w' -o /out/simpleclient ./cmd/simpleclient

# ── Kernel + initramfs ────────────────────────────────────────────────────────
FROM --platform=linux/amd64 alpine:${ALPINE_VERSION} AS rootfs-builder

RUN apk add --no-cache linux-lts busybox-static kmod cpio gzip

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
RUN KVER=$(basename /lib/modules/*) \
 && echo "kernel: $KVER" \
 && mkdir -p "/rootfs/lib/modules/$KVER/kernel/drivers" \
 && for d in net gpu input hid usb virtio; do \
        if [ -d "/lib/modules/$KVER/kernel/drivers/$d" ]; then \
            cp -a "/lib/modules/$KVER/kernel/drivers/$d" "/rootfs/lib/modules/$KVER/kernel/drivers/"; \
        fi; \
    done \
 && cp -a "/lib/modules/$KVER/kernel/net" "/rootfs/lib/modules/$KVER/kernel/" \
 && for f in modules.builtin modules.builtin.modinfo modules.order; do \
        if [ -f "/lib/modules/$KVER/$f" ]; then \
            cp -a "/lib/modules/$KVER/$f" "/rootfs/lib/modules/$KVER/"; \
        fi; \
    done \
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
