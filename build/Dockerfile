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
# Two kernels ship, and GRUB picks between them on its own (see grub.cfg):
#   new  trixie-backports, the default. The kiosk has to run on mini PCs from
#        2010 to the current year, and the newest GPUs, NICs and audio DSPs are
#        only in the newest kernel, while the old drivers all remain.
#   lts  stable's LTS series, which a machine falls back to, and stays on, when
#        the new kernel does not come up on it.
# Both are signed with the same key. The userland stays Alpine's; a kernel does
# not care whose libc runs above it.
#
# Packages are unpacked with dpkg-deb rather than installed: their maintainer
# scripts would run grub-install and build an initramfs inside the container.
#
# A kernel is no longer one package. Newer series split linux-image-<ver> into
# an empty package depending on linux-binary-<ver> (the signed vmlinuz, in the
# modules directory), linux-modules-<ver> and linux-base-<ver>, so each kernel
# is fetched as every package its image pulls in that carries its version, and
# unpacked on its own: new and lts never share a directory to be told apart in.
FROM --platform=linux/amd64 debian:${DEBIAN_VERSION} AS boot-builder
ARG DEBIAN_VERSION
ENV DEBIAN_FRONTEND=noninteractive

RUN set -e; \
    echo "deb http://deb.debian.org/debian ${DEBIAN_VERSION}-backports main" \
        > /etc/apt/sources.list.d/backports.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
        grub-pc-bin grub-common xorriso mtools dosfstools kmod xz-utils zstd; \
    kpkg() { apt-cache "$@" depends linux-image-amd64 \
        | awk '$1 == "Depends:" && $2 ~ /^linux-image-[0-9]/ { print $2; exit }'; }; \
    NEW=$(kpkg -o "APT::Default-Release=${DEBIAN_VERSION}-backports"); \
    LTS=$(kpkg); \
    echo "kernels: new=$NEW lts=$LTS"; \
    test -n "$NEW"; test -n "$LTS"; test "$NEW" != "$LTS"; \
    for F in new lts; do \
        if [ $F = new ]; then P=$NEW; else P=$LTS; fi; \
        mkdir -p /deb/$F /unpacked/$F; cd /deb/$F; \
        apt-get download $(apt-cache depends --recurse --no-recommends --no-suggests \
                --no-conflicts --no-breaks --no-replaces --no-enhances "$P" \
            | awk -v k="-${P#linux-image-}" '!/^ / && substr($0, length($0) - length(k) + 1) == k'); \
        for p in *.deb; do echo "kernel $F: $p"; dpkg-deb -x "$p" /unpacked/$F; done; \
    done; \
    mkdir -p /deb/boot /signed; cd /deb/boot; \
    apt-get download shim-signed shim-helpers-amd64-signed grub-efi-amd64-signed; \
    for p in *.deb; do dpkg-deb -x "$p" /signed; done; \
    rm -rf /var/lib/apt/lists/*

# Modules stay xz-compressed, as Debian ships them. Both kernels decompress
# modules themselves (CONFIG_MODULE_DECOMPRESS), and busybox modprobe loads a
# compressed module by handing the kernel the file. Recompressing them to gzip
# once looked necessary and broke the module set without a sound: this depmod
# is built without zlib, read none of the .ko.gz files, and wrote a modules.dep
# with no dependencies in it, so select-modules never copied scsi_mod and no
# SATA or USB disk ever appeared.
# The vmlinuz is in the modules directory in the split packages and in /boot
# in the older ones.
RUN set -e; \
    for F in new lts; do \
        M=$(ls -d /unpacked/$F/usr/lib/modules/* /unpacked/$F/lib/modules/* 2>/dev/null | head -1); \
        KVER=$(basename "$M"); \
        echo "kernel $F: $KVER"; \
        mkdir -p /kernel/$F/lib/modules; mv "$M" /kernel/$F/lib/modules/; \
        M=/kernel/$F/lib/modules/$KVER; \
        if [ -f "$M/vmlinuz" ]; then mv "$M/vmlinuz" /kernel/$F/vmlinuz; \
        else cp "/unpacked/$F/boot/vmlinuz-$KVER" /kernel/$F/vmlinuz; fi; \
        depmod -b /kernel/$F "$KVER"; \
    done; \
    test -f /kernel/new/vmlinuz; test -f /kernel/lts/vmlinuz; \
    ls -l /signed/usr/lib/shim /signed/usr/lib/grub/x86_64-efi-signed

# ── Initramfs ─────────────────────────────────────────────────────────────────
# One initramfs with everything but the kernel modules, and one modules archive
# per kernel. GRUB loads the initramfs and the archive that matches the kernel
# it boots; the kernel unpacks both into the same root.
FROM --platform=linux/amd64 alpine:${ALPINE_VERSION} AS rootfs-builder

RUN apk add --no-cache busybox-static kmod cpio gzip
COPY --from=boot-builder /kernel/new/lib /kmods/new/lib
COPY --from=boot-builder /kernel/lts/lib /kmods/lts/lib
COPY build/select-modules.sh /usr/local/bin/select-modules
RUN set -e; chmod +x /usr/local/bin/select-modules; mkdir -p /out; \
    for F in new lts; do \
        select-modules /kmods/$F /mods/$F; \
        (cd /mods/$F && find . | cpio -oH newc --quiet | gzip -9) > /out/modules-$F.cpio.gz; \
    done; \
    ls -lh /out

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
#
# Installing to disk (sc-install): sfdisk partitions, mkfs.fat and mkfs.ext4
# format, grub-install puts GRUB's BIOS boot code on the disk, efibootmgr adds
# the UEFI boot entry. The daily update fetches over HTTPS, which needs the CA
# bundle.
RUN apk add --no-cache alsa-utils alsa-lib \
        sfdisk dosfstools e2fsprogs grub grub-bios efibootmgr ca-certificates-bundle
COPY build/copy-bins.sh /usr/local/bin/copy-bins
RUN set -e; chmod +x /usr/local/bin/copy-bins; \
    copy-bins /rootfs aplay arecord amixer alsactl \
        sfdisk mkfs.fat mkfs.ext4 grub-install grub-bios-setup grub-mkimage grub-probe grub-editenv \
        efibootmgr; \
    mkdir -p /rootfs/usr/share /rootfs/usr/lib /rootfs/etc/ssl/certs; \
    cp -a /usr/share/alsa /rootfs/usr/share/; \
    cp -a /usr/lib/grub /rootfs/usr/lib/; \
    cp /etc/ssl/certs/ca-certificates.crt /rootfs/etc/ssl/certs/; \
    du -sh /rootfs/usr /rootfs/lib

COPY --from=go-builder /out/simpleclient /rootfs/sbin/simpleclient
COPY build/init /rootfs/init
COPY build/udhcpc-default.script /rootfs/usr/share/udhcpc/default.script
COPY build/sc-install /rootfs/sbin/sc-install
COPY build/sc-net /rootfs/sbin/sc-net
COPY build/grub-disk.cfg /rootfs/usr/share/simpleclient/grub-disk.cfg

RUN mkdir -p /out \
 && mkdir -p /rootfs/proc /rootfs/sys /rootfs/dev /rootfs/tmp /rootfs/run /rootfs/etc /rootfs/bin \
 && chmod +x /rootfs/init /rootfs/sbin/simpleclient /rootfs/sbin/sc-install /rootfs/sbin/sc-net \
        /rootfs/usr/share/udhcpc/default.script

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

RUN mkdir -p /rootfs/lib/firmware \
 && for d in bnx2 bnx2x rtl_nic tigon e100; do \
        if [ -d "/lib/firmware/$d" ]; then cp -a "/lib/firmware/$d" /rootfs/lib/firmware/; fi; \
    done \
 && du -sh /rootfs/lib/firmware

RUN cd /rootfs && find . | cpio -oH newc --quiet | gzip -9 > /out/initramfs.cpio.gz \
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
#
# The FAT image is also where a USB stick keeps its one piece of state: a
# grubenv recording which of the two kernels comes up on this machine. It is
# labelled SC_EFI so GRUB and the kiosk can find it. A CD cannot be written, so
# booted from one the kiosk always starts with the new kernel; the boot menu
# offers the other.
#
# boot/efi/grubx64.efi is Debian's GRUB for disks, not CDs; sc-install copies
# it onto the ESP of a disk install.
FROM boot-builder AS iso-builder

COPY --from=rootfs-builder /out/initramfs.cpio.gz /out/modules-new.cpio.gz /out/modules-lts.cpio.gz /iso/boot/
COPY build/grub.cfg /iso/boot/grub/grub.cfg

RUN set -e; \
    cp /kernel/new/vmlinuz /iso/boot/vmlinuz-new; \
    cp /kernel/lts/vmlinuz /iso/boot/vmlinuz-lts; \
    mkdir -p /iso/.disk /iso/boot/grub/i386-pc /iso/boot/grub/fonts /iso/boot/efi /efi/EFI/boot /out; \
    echo "SimpleClient" > /iso/.disk/info; \
    cp /usr/lib/grub/i386-pc/*.mod /usr/lib/grub/i386-pc/*.lst /iso/boot/grub/i386-pc/; \
    cp /usr/share/grub/unicode.pf2 /iso/boot/grub/fonts/; \
    grub-mkimage -O i386-pc-eltorito -p /boot/grub -o /iso/boot/grub/i386-pc/eltorito.img biosdisk iso9660; \
    cp /signed/usr/lib/shim/shimx64.efi.signed /efi/EFI/boot/bootx64.efi; \
    cp /signed/usr/lib/shim/mmx64.efi.signed /efi/EFI/boot/mmx64.efi; \
    cp /signed/usr/lib/grub/x86_64-efi-signed/gcdx64.efi.signed /efi/EFI/boot/grubx64.efi; \
    cp /signed/usr/lib/grub/x86_64-efi-signed/grubx64.efi.signed /iso/boot/efi/grubx64.efi; \
    cp -r /efi/EFI /iso/; \
    grub-editenv /efi/grubenv create; \
    grub-editenv /efi/grubenv set sc_kernel=new sc_kpending=0; \
    mkfs.vfat -n SC_EFI -C /iso/boot/efi.img 8192 >/dev/null; \
    mcopy -s -i /iso/boot/efi.img /efi/EFI /efi/grubenv ::/; \
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
COPY --from=rootfs-builder /out/initramfs.cpio.gz /out/modules-new.cpio.gz /out/modules-lts.cpio.gz /
COPY --from=iso-builder /iso/boot/vmlinuz-new /iso/boot/vmlinuz-lts /
COPY --from=iso-builder /out/simpleclient.iso /
