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

# ── AMD's video decoders ──────────────────────────────────────────────────────
# A GPU decodes the session's H.264 where it can (engine/hwdec.c), through
# VA-API. Intel's VA-API drivers are Alpine's (see the engine below); AMD's is
# Mesa's, built here with only what a video decoder takes: radeonsi (the HD
# 7000 series and later) and r600 (the Radeons before them), H.264, and no
# LLVM, which radeonsi does without as it compiles with ACO. Alpine's Mesa
# would bring LLVM, OpenGL and X11 along, over 220 MB the initramfs would carry
# for this one driver; this one is 20 MB. The tarball's SHA-512 matched
# Alpine's own record of it when the sum below was taken.
FROM --platform=linux/amd64 alpine:${ALPINE_VERSION} AS mesa-builder
ARG MESA_VERSION=26.1.6
ARG MESA_SHA256=5296b88a0f1e012e2cb9ada150a2bbadf728ca81e5a4fb2ab43c83a4d2158606
RUN set -e; \
    apk add --no-cache build-base meson samurai python3 py3-mako py3-yaml py3-packaging bison \
        flex glslang pkgconf curl linux-headers libdrm-dev libva-dev zlib-dev elfutils-dev; \
    mkdir -p /src; cd /src; \
    curl -fsSLo mesa.tar.xz "https://mesa.freedesktop.org/archive/mesa-${MESA_VERSION}.tar.xz"; \
    echo "${MESA_SHA256}  mesa.tar.xz" | sha256sum -c -; \
    tar -xJf mesa.tar.xz; \
    meson setup mesa-build "mesa-${MESA_VERSION}" --prefix=/usr --libdir=lib --buildtype=release \
        -Db_ndebug=true -Dplatforms= -Dgallium-drivers=radeonsi,r600 -Dvulkan-drivers= \
        -Dgallium-va=enabled -Dvideo-codecs=h264dec -Dllvm=disabled \
        -Dopengl=false -Dglx=disabled -Degl=disabled -Dgbm=disabled -Dgles1=disabled \
        -Dgles2=disabled -Dglvnd=disabled -Dxmlconfig=disabled -Dexpat=disabled -Dzstd=disabled \
        -Dshader-cache=disabled -Dvalgrind=disabled -Dlibunwind=disabled -Dlmsensors=disabled \
        -Dmicrosoft-clc=disabled -Dgallium-d3d12-video=disabled -Dgallium-d3d12-graphics=disabled \
        -Dintel-rt=disabled -Ddisplay-info=disabled -Dspirv-tools=disabled -Dbuild-tests=false \
        -Dtools=; \
    meson compile -C mesa-build; \
    DESTDIR=/mesa meson install -C mesa-build; \
    strip --strip-unneeded /mesa/usr/lib/dri/libgallium_drv_video.so; \
    ls -l /mesa/usr/lib/dri

# ── Session engine ────────────────────────────────────────────────────────────
# sc-rdp (engine/): FreeRDP drawing straight to the display through KMS, with
# the graphics pipeline's H.264 for frame rates the bitmap protocol cannot
# reach, and FreeRDP's own channels for sound, microphone, USB devices and
# smart cards. The kiosk falls back to its built-in client where it cannot run.
#
# FFmpeg is built with the H.264 decoder, its VA-API path to the GPU, and
# swscale, and nothing else: Alpine's ffmpeg-libs would bring every codec there
# is, tens of megabytes the initramfs would carry for nothing. FreeRDP is built
# without its X11, Wayland and SDL clients, its server side and everything that
# would want a desktop.
#
# The sources are checked against their published SHA-256 (FreeRDP's release
# .sha256; FFmpeg's tarball, whose signature by the FFmpeg release key was
# checked when the sum was taken).
FROM --platform=linux/amd64 alpine:${ALPINE_VERSION} AS rdp-builder
ARG FFMPEG_VERSION=8.1.3
ARG FFMPEG_SHA256=7138d28c96d9d3e3af4ee3d8cad72741f8ffb40da90c1112235dea3ecd3178a3
ARG FREERDP_VERSION=3.32.2
ARG FREERDP_SHA256=e95a505c0057e4d06a3b120477c421028769deba4db9c639ef3465d050f0955c

RUN apk add --no-cache build-base cmake samurai nasm pkgconf curl linux-headers \
        openssl-dev zlib-dev alsa-lib-dev libusb-dev pcsc-lite-dev libdrm-dev libva-dev

RUN set -e; mkdir -p /src; cd /src; \
    curl -fsSLo ffmpeg.tar.xz "https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.xz"; \
    echo "${FFMPEG_SHA256}  ffmpeg.tar.xz" | sha256sum -c -; \
    tar -xJf ffmpeg.tar.xz; cd "ffmpeg-${FFMPEG_VERSION}"; \
    ./configure --prefix=/usr --libdir=/usr/lib --enable-shared --disable-static \
        --disable-programs --disable-doc --disable-network --disable-autodetect \
        --disable-everything --enable-decoder=h264 --enable-parser=h264 \
        --enable-vaapi --enable-libdrm --enable-hwaccel=h264_vaapi \
        --disable-avformat --disable-avdevice --disable-avfilter --disable-swresample \
        --enable-swscale --enable-pthreads --disable-debug; \
    grep -q "define CONFIG_H264_VAAPI_HWACCEL 1" config_components.h; \
    grep -q "define HAVE_VAAPI_DRM 1" config.h; \
    make -j"$(nproc)"; make install

# build/patches/freerdp/: what a session costs.
#   0001  FreeRDP sized its planar buffers for the whole desktop, twice: 67
#         bytes a pixel in all, 345 MB at 3440x1440, which a 768 MB kiosk did
#         not have.
#   0002-0005  Decoding video. A 1080p film at 30 fps in a browser took a
#         kiosk 2.2 cores over RemoteFX and 0.8 over H.264; it takes 1.0 and
#         0.6. The progressive codec's inverse DWT in SSE2 (ARM had NEON): 5.9
#         times as fast, and the same to the bit. Its tiles go to the thread
#         pool eight at a time, with each thread's own scratch buffers: the
#         pool's lock and wakeups for every tile had cost more than the DWT.
#         And H.264 decodes a frame's slices side by side, as many as the
#         server cuts it into, where FreeRDP left libavcodec on one thread.
#   0006  H.264 on the GPU: a driver that lists YUV420P among the formats it
#         hands pictures back in, and then fails to, hands them back in its
#         own and swscale converts them, where FreeRDP lost every frame.
COPY build/patches/freerdp/ /src/freerdp-patches/
RUN set -e; cd /src; \
    curl -fsSLo freerdp.tar.gz \
        "https://github.com/FreeRDP/FreeRDP/releases/download/${FREERDP_VERSION}/freerdp-${FREERDP_VERSION}.tar.gz"; \
    echo "${FREERDP_SHA256}  freerdp.tar.gz" | sha256sum -c -; \
    tar -xzf freerdp.tar.gz; \
    for p in /src/freerdp-patches/*.patch; do patch -d "freerdp-${FREERDP_VERSION}" -p1 < "$p"; done; \
    cmake -S "freerdp-${FREERDP_VERSION}" -B freerdp-build -G Ninja \
        -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX=/usr -DCMAKE_INSTALL_LIBDIR=lib \
        -DBUILD_TESTING=OFF -DWITH_MANPAGES=OFF -DWITH_SAMPLE=OFF \
        -DWITH_CLIENT=ON -DWITH_CLIENT_COMMON=ON -DWITH_CLIENT_CHANNELS=ON \
        -DWITH_CLIENT_SDL=OFF -DWITH_X11=OFF -DWITH_WAYLAND=OFF \
        -DWITH_SERVER=OFF -DWITH_SERVER_CHANNELS=OFF -DWITH_SHADOW=OFF -DWITH_PROXY=OFF \
        -DWITH_PLATFORM_SERVER=OFF -DWITH_RDTK=OFF -DWITH_WINPR_TOOLS=OFF \
        -DWITH_FFMPEG=ON -DWITH_VIDEO_FFMPEG=ON -DWITH_DSP_FFMPEG=OFF -DWITH_SWSCALE=ON \
        -DWITH_FFMPEG_HWACCEL=ON \
        -DWITH_OPENH264=OFF -DWITH_GFX_AV1=OFF \
        -DWITH_ALSA=ON -DWITH_PULSE=OFF -DWITH_OSS=OFF -DWITH_SNDIO=OFF \
        -DWITH_OPUS=OFF -DWITH_FDK_AAC=OFF -DWITH_SOXR=OFF \
        -DWITH_PCSC=ON -DWITH_PKCS11=OFF -DWITH_KRB5=OFF \
        -DWITH_CUPS=OFF -DWITH_FUSE=OFF -DWITH_CAIRO=OFF \
        -DWITH_JPEG=OFF -DWITH_PNG=OFF -DWITH_WEBP=OFF -DWITH_LODEPNG=OFF \
        -DWINPR_UTILS_IMAGE_PNG=OFF -DWINPR_UTILS_IMAGE_JPEG=OFF -DWINPR_UTILS_IMAGE_WEBP=OFF \
        -DWITH_JSON_DISABLED=ON -DWITH_AAD=OFF -DWITH_URIPARSER=OFF -DWITH_SSO_MIB=OFF \
        -DWITH_SYSTEMD=OFF -DWITH_UNICODE_BUILTIN=ON -DWITH_TIMEZONE_ICU=OFF \
        -DWITH_INTERNAL_MD4=ON -DWITH_INTERNAL_MD5=ON -DWITH_INTERNAL_RC4=ON \
        -DWITH_SIMD=ON -DWITH_VERBOSE_WINPR_ASSERT=OFF -DWITH_FREERDP_DEPRECATED_COMMANDLINE=OFF; \
    cmake --build freerdp-build; \
    cmake --install freerdp-build

# Smart cards: pcscd, which FreeRDP's smart-card redirection reaches through
# libpcsclite, and the CCID driver, which covers nearly every USB reader and
# token. pcscd is built here rather than taken from Alpine, whose build finds
# readers through udev: the kiosk runs no udev daemon, so a reader plugged in
# after boot would never be seen. Built for libusb, pcscd looks for readers
# itself, every second. The CCID driver is Alpine's. The tarball's SHA-512
# matched Alpine's own record of it when the sum below was taken.
ARG PCSC_VERSION=2.5.2
ARG PCSC_SHA256=60a08942d8c00a1d86a3bf4c64eddbf7a5569c7c54f6904e3f07801bb5316acd
RUN set -e; apk add --no-cache meson flex perl ccid; cd /src; \
    curl -fsSLo pcsc.tar.xz "https://pcsclite.apdu.fr/files/pcsc-lite-${PCSC_VERSION}.tar.xz"; \
    echo "${PCSC_SHA256}  pcsc.tar.xz" | sha256sum -c -; \
    tar -xJf pcsc.tar.xz; \
    meson setup pcsc-build "pcsc-lite-${PCSC_VERSION}" --prefix=/usr --libdir=lib --buildtype=release \
        -Dlibudev=false -Dlibusb=true -Dpolkit=false -Dlibsystemd=false -Dserial=false \
        -Dusbdropdir=/usr/lib/pcsc/drivers -Dipcdir=/run/pcscd; \
    meson compile -C pcsc-build; \
    meson install -C pcsc-build; \
    grep -A1 ifdCapabilities /usr/lib/pcsc/drivers/ifd-ccid.bundle/Contents/Info.plist

COPY engine/ /src/engine/
COPY build/copy-bins.sh /usr/local/bin/copy-bins
COPY --from=mesa-builder /mesa/usr/lib/dri/ /usr/lib/dri/
# yuv_test checks the engine's own YUV conversion against FreeRDP's, byte for
# byte; hwdec_test that the test stream a GPU is tried with decodes, on the CPU,
# to the pictures a GPU's are checked against. Either failing stops the build.
#
# The GPUs' VA-API drivers come along: Alpine's for Intel, iHD (Broadwell and
# later) and i965 (the GPUs before), and Mesa's for AMD, from above, one file
# that each of its drivers' names links to. They are 70 MB, and stay
# compressed with xz in 12, as a machine needs one maker's at most: /init
# unpacks those of the GPUs it runs.
RUN set -e; chmod +x /usr/local/bin/copy-bins; \
    apk add --no-cache x264 python3 xz intel-media-driver libva-intel-driver libelf libstdc++; \
    python3 /src/engine/probe264.py > /src/engine/probe264.h; \
    FLAGS="$(pkg-config --cflags --libs freerdp-client3 freerdp3 winpr3 libdrm libavcodec libavutil libva) -lpthread"; \
    cc -O2 -Wall -o /tmp/yuv_test /src/engine/yuv_test.c /src/engine/yuv.c $FLAGS; \
    /tmp/yuv_test; \
    cc -O2 -Wall -o /tmp/hwdec_test /src/engine/hwdec_test.c /src/engine/hwdec.c $FLAGS; \
    /tmp/hwdec_test; \
    cc -O2 -Wall -o /usr/bin/sc-rdp /src/engine/sc-rdp.c /src/engine/kms.c /src/engine/yuv.c \
        /src/engine/hwdec.c $FLAGS; \
    copy-bins /out sc-rdp; \
    copy-bins /out /usr/lib/dri/iHD_drv_video.so /usr/lib/dri/i965_drv_video.so \
        /usr/lib/dri/libgallium_drv_video.so; \
    for d in radeonsi r600; do ln -s libgallium_drv_video.so "/out/usr/lib/dri/${d}_drv_video.so"; done; \
    xz -9e -T1 /out/usr/lib/dri/iHD_drv_video.so /out/usr/lib/dri/i965_drv_video.so \
        /out/usr/lib/dri/libgallium_drv_video.so; \
    if [ -d /usr/lib/freerdp3 ]; then mkdir -p /out/usr/lib; cp -a /usr/lib/freerdp3 /out/usr/lib/; fi; \
    CCID=/usr/lib/pcsc/drivers/ifd-ccid.bundle; \
    copy-bins /out pcscd /usr/lib/libpcsclite.so.1 /usr/lib/libpcsclite_real.so.1 "$CCID/Contents/Linux/libccid.so"; \
    cp -a "$CCID/Contents/Info.plist" "/out$CCID/Contents/"; \
    find /out -type f | sort; du -sh /out

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

# GPU firmware, for the GPU's own driver that /init loads (see its display
# step). radeon, amdgpu and xe do not bring a display up without theirs; i915
# runs without but loses power management. It is its own archive, firmware.cpio,
# which GRUB loads after the modules: one copy for both kernels, and its files
# are compressed already (zstd, which the kernel reads), so it is not
# compressed again.
#
# Left out, as no kiosk has use for them:
#   NVIDIA's GSP firmware, 100 MB. nouveau needs it only on Ada and later,
#                 which /init therefore leaves on the firmware framebuffer;
#                 older GeForces run without it.
#   i915's HuC and GSC firmware, for the encoders' rate control and content
#                 protection: the GPU decodes video without them.
#   AMD's data-centre accelerators (Instinct MI100-MI355), which have no
#                 display.
RUN set -e; \
    apk add --no-cache linux-firmware-i915 linux-firmware-xe linux-firmware-amdgpu \
        linux-firmware-radeon linux-firmware-nvidia; \
    mkdir -p /fw/lib/firmware; \
    for d in i915 xe amdgpu radeon nvidia; do cp -a "/lib/firmware/$d" /fw/lib/firmware/; done; \
    cd /fw/lib/firmware; \
    rm -rf nvidia/*/gsp i915/*huc* i915/*gsc*; \
    for p in aldebaran arcturus gc_9_4_3 gc_9_4_4 gc_9_5_0 psp_13_0_6 psp_13_0_12 psp_13_0_14 \
             sdma_4_4_2 sdma_4_4_4 sdma_4_4_5 smu_13_0_6 smu_13_0_12 smu_13_0_14 \
             vcn_4_0_3 vcn_5_0_1; do \
        rm -f amdgpu/${p}[._]*; \
    done; \
    find . -type l | while read -r l; do [ -e "$l" ] || rm "$l"; done; \
    du -sh /fw/lib/firmware/*

# Wireless firmware, into the same archive: what the wireless drivers in the
# modules archives say they load (see select-firmware.sh), for both kernels.
# And the regulatory database: cfg80211 reads from it which channels, at what
# power, the country the access points announce allows. Without it a kiosk
# keeps to the few channels allowed everywhere, and only listens on most of
# those.
COPY build/select-firmware.sh /usr/local/bin/select-firmware
RUN set -e; chmod +x /usr/local/bin/select-firmware; \
    apk add --no-cache linux-firmware-intel linux-firmware-other linux-firmware-ath10k \
        linux-firmware-ath11k linux-firmware-ath12k linux-firmware-ath6k linux-firmware-ath9k_htc \
        linux-firmware-mediatek linux-firmware-rtw88 linux-firmware-rtw89 linux-firmware-rtlwifi \
        linux-firmware-brcm linux-firmware-cypress linux-firmware-mrvl wireless-regdb; \
    select-firmware /fw $(find /mods/new /mods/lts \( -path '*/net/wireless/*' -o -path '*/staging/*' \) \
        -name '*.ko*'); \
    cp /lib/firmware/regulatory.db /lib/firmware/regulatory.db.p7s /fw/lib/firmware/; \
    cd /fw && find . | cpio -oH newc --quiet > /out/firmware.cpio; \
    ls -lh /out/firmware.cpio

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
#
# Wireless networks: wpa_supplicant joins them (WPA2/WPA3, personal and
# enterprise), wpa_cli is how sc-net and the network dialog talk to it.
RUN apk add --no-cache alsa-utils alsa-lib \
        sfdisk dosfstools e2fsprogs grub grub-bios efibootmgr ca-certificates-bundle \
        wpa_supplicant
COPY build/copy-bins.sh /usr/local/bin/copy-bins
RUN set -e; chmod +x /usr/local/bin/copy-bins; \
    copy-bins /rootfs aplay arecord amixer alsactl \
        sfdisk mkfs.fat mkfs.ext4 grub-install grub-bios-setup grub-mkimage grub-probe grub-editenv \
        efibootmgr wpa_supplicant wpa_cli; \
    mkdir -p /rootfs/usr/share /rootfs/usr/lib /rootfs/etc/ssl/certs; \
    cp -a /usr/share/alsa /rootfs/usr/share/; \
    cp -a /usr/lib/grub /rootfs/usr/lib/; \
    cp /etc/ssl/certs/ca-certificates.crt /rootfs/etc/ssl/certs/; \
    du -sh /rootfs/usr /rootfs/lib

COPY --from=go-builder /out/simpleclient /rootfs/sbin/simpleclient
COPY --from=rdp-builder /out/ /rootfs/
COPY build/init /rootfs/init
COPY build/udhcpc-default.script /rootfs/usr/share/udhcpc/default.script
COPY build/sc-install /rootfs/sbin/sc-install
COPY build/sc-net /rootfs/sbin/sc-net
COPY build/sc-wifi-event /rootfs/sbin/sc-wifi-event
COPY build/grub-disk.cfg /rootfs/usr/share/simpleclient/grub-disk.cfg

# /media is where USB drives are mounted for sessions to offer the server.
# FreeRDP looks there, and under /media/<user>, by the name of the user it runs
# as; without a passwd entry to name root it finds no drives at all.
RUN mkdir -p /out \
 && mkdir -p /rootfs/proc /rootfs/sys /rootfs/dev /rootfs/tmp /rootfs/run /rootfs/etc /rootfs/bin \
        /rootfs/media \
 && echo 'root:x:0:0:root:/tmp:/bin/sh' > /rootfs/etc/passwd \
 && echo 'root:x:0:' > /rootfs/etc/group \
 && chmod +x /rootfs/init /rootfs/sbin/simpleclient /rootfs/sbin/sc-install /rootfs/sbin/sc-net \
        /rootfs/sbin/sc-wifi-event /rootfs/usr/share/udhcpc/default.script

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
 && test "$(readlink /rootfs/bin/sh)" = "/bin/busybox" \
 && test -L /rootfs/bin/unxz

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
#            MBR boot code when the ISO is a disk. GRUB loads a partition map
#            only when told to, so the ones a USB stick has are built in, with
#            FAT: without them the grubenv on SC_EFI is never found, and a
#            kernel that fails is never swapped for the other.
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

COPY --from=rootfs-builder /out/initramfs.cpio.gz /out/modules-new.cpio.gz /out/modules-lts.cpio.gz \
     /out/firmware.cpio /iso/boot/
COPY build/grub.cfg /iso/boot/grub/grub.cfg

RUN set -e; \
    cp /kernel/new/vmlinuz /iso/boot/vmlinuz-new; \
    cp /kernel/lts/vmlinuz /iso/boot/vmlinuz-lts; \
    mkdir -p /iso/.disk /iso/boot/grub/i386-pc /iso/boot/grub/fonts /iso/boot/efi /efi/EFI/boot /out; \
    echo "SimpleClient" > /iso/.disk/info; \
    cp /usr/lib/grub/i386-pc/*.mod /usr/lib/grub/i386-pc/*.lst /iso/boot/grub/i386-pc/; \
    cp /usr/share/grub/unicode.pf2 /iso/boot/grub/fonts/; \
    grub-mkimage -O i386-pc-eltorito -p /boot/grub -o /iso/boot/grub/i386-pc/eltorito.img \
        biosdisk iso9660 part_gpt part_msdos fat search search_label loadenv; \
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
COPY --from=rootfs-builder /out/initramfs.cpio.gz /out/modules-new.cpio.gz /out/modules-lts.cpio.gz \
     /out/firmware.cpio /
COPY --from=iso-builder /iso/boot/vmlinuz-new /iso/boot/vmlinuz-lts /
COPY --from=iso-builder /out/simpleclient.iso /
