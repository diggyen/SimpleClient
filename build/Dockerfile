# simpleclient build container
# Produces a statically linked binary + bootable ISO
#
# NOTE: grdp/plugin uses CGo (import "C"), so we need CGO_ENABLED=1
# with a cross-compiler for amd64 on ARM hosts.

FROM --platform=linux/amd64 golang:1.23-alpine AS go-builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
    go build -ldflags '-s -w -extldflags "-static"' \
    -o /out/simpleclient ./cmd/simpleclient

# ── Kernel builder ────────────────────────────────────────────────────────────
FROM --platform=linux/amd64 debian:bookworm-slim AS kernel-builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential bc bison flex libelf-dev libssl-dev \
    perl python3 xz-utils wget ca-certificates \
    && rm -rf /var/lib/apt/lists/*

ARG KERNEL_VERSION=6.6.37
RUN wget -q "https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-${KERNEL_VERSION}.tar.xz" \
    && tar xf linux-${KERNEL_VERSION}.tar.xz

COPY build/kernel.config /linux-${KERNEL_VERSION}/.config

RUN cd linux-${KERNEL_VERSION} \
    && make olddefconfig \
    && make -j$(nproc) bzImage \
    && ls -lh arch/x86/boot/bzImage

RUN mkdir -p /out && cp /linux-${KERNEL_VERSION}/arch/x86/boot/bzImage /out/vmlinuz

# ── Initramfs builder ─────────────────────────────────────────────────────────
FROM --platform=linux/amd64 alpine:3.20 AS initramfs-builder

RUN apk add --no-cache busybox-static cpio gzip

COPY --from=go-builder /out/simpleclient /rootfs/sbin/simpleclient
COPY build/init /rootfs/init

RUN chmod +x /rootfs/init /rootfs/sbin/simpleclient \
    && mkdir -p /rootfs/proc /rootfs/sys /rootfs/dev /rootfs/tmp /rootfs/etc /rootfs/bin

# Copy busybox. Alpine 3.20 installs to /bin/busybox for the static variant.
RUN BUSYBOX=$(command -v busybox || echo /bin/busybox) \
    && cp "$BUSYBOX" /rootfs/bin/busybox \
    && /rootfs/bin/busybox --install -s /rootfs/bin

# Build cpio.gz
RUN mkdir -p /out && cd /rootfs \
    && find . | cpio -oH newc | gzip > /out/initramfs.cpio.gz

# ── ISO builder ───────────────────────────────────────────────────────────────
FROM --platform=linux/amd64 alpine:3.20 AS iso-builder

RUN apk add --no-cache grub grub-efi xorriso mtools dosfstools

COPY --from=kernel-builder /out/vmlinuz /iso/boot/vmlinuz
COPY --from=initramfs-builder /out/initramfs.cpio.gz /iso/boot/initramfs.cpio.gz
COPY build/grub.cfg /iso/boot/grub/grub.cfg

RUN mkdir -p /out && grub-mkrescue -o /out/simpleclient.iso /iso \
    -- -volid SIMPLECLIENT

# ── Final export stage ────────────────────────────────────────────────────────
FROM scratch AS export
COPY --from=go-builder /out/simpleclient /
COPY --from=initramfs-builder /out/initramfs.cpio.gz /
COPY --from=iso-builder /out/simpleclient.iso /
