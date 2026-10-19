#!/usr/bin/env bash
# simpleclient-server.sh — prepares a Linux desktop to serve SimpleClient
# kiosks over RDP.
#
# The xrdp that distributions package is 0.9 (Ubuntu 24.04, Linux Mint 22 and
# older): no graphics pipeline, so no H.264. The desktop then goes out as
# bitmaps, at a fraction of the frame rate and many times the bandwidth. This
# builds xrdp 0.10 with x264 (OpenH264 where x264 is not packaged) and its
# Xorg backend from source, and sets them up for the kiosks:
#   - H.264 frames up to every 8 ms (120 fps) instead of every 16
#   - the Turkish Q and F keyboard layouts, which xrdp does not map: without
#     them a Turkish keyboard types as a US one
#   - sound both ways, through PipeWire or PulseAudio, whichever the desktop
#     runs, and USB drives through xrdp's FUSE file system
#   - the kiosks' printers, scanners, smart-card readers and serial adapters
#     attached here over USB/IP, as if plugged in, by an agent the kiosks
#     reach on port 3241; the PC/SC daemon and CCID driver for the cards
#   - the desktop the machine already has (Cinnamon, MATE, Xfce, KDE Plasma,
#     GNOME, ...) for every user without a ~/.xsession of their own
#   - no polkit password prompts for colour profiles and package lists,
#     which a remote session otherwise gets on every login
#   - ports 3389 and 3241 open in ufw or firewalld
# It can be run again: to upgrade, or after a distribution upgrade has
# replaced the libraries xrdp was built against.
#
# Usage:  sudo bash simpleclient-server.sh [--session NAME] [--no-audio] [--no-usb]
#   --session NAME  the desktop for remote sessions, a file name from
#                   /usr/share/xsessions without .desktop (cinnamon, mate,
#                   xfce, plasmax11, ubuntu-xorg, ...); default: the first of
#                   those this machine has
#   --no-audio      leave sound alone
#   --no-usb        no USB/IP agent: drives still come, by name
set -Eeuo pipefail

XRDP_VERSION=0.10.6.1
XRDP_SHA256=2f7beb5a3b2529c8d72dc0df9b8cdca31ab0e0c14d1e3421210f5e6ec0ab3b75
XORGXRDP_VERSION=0.10.5
XORGXRDP_SHA256=a5d03435f0ef48bf3d5010e63d9264f2334e7063cba3ecd8d4c0a15616a4f712
PWMOD_VERSION=0.1
PWMOD_SHA256=a85b6b17643452790af3e53966a885650feeeef9c59c24e5c82924a62eeacf8f
PAMOD_VERSION=0.8
PAMOD_SHA256=f0cc39b67421511c28395329a4091f8c5be79d861414b7d67313aa760e3b9db4

SRC=/usr/local/src/simpleclient-server
LOG=/var/log/simpleclient-server.log
CONF=/etc/simpleclient

say() { printf '\033[1m[SimpleClient]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[SimpleClient] UYARI:\033[0m %s\n' "$*"; }
die() {
    printf '\033[1;31m[SimpleClient] HATA:\033[0m %s\n' "$*" >&2
    printf 'Ayrıntılar: %s\n' "$LOG" >&2
    exit 1
}

WANT_SESSION=""
AUDIO=1
USB=1
while [ $# -gt 0 ]; do
    case "$1" in
        --session) WANT_SESSION=${2:-}; shift ;;
        --session=*) WANT_SESSION=${1#--session=} ;;
        --no-audio) AUDIO=0 ;;
        --no-usb) USB=0 ;;
        -h|--help) sed -n '2,/^set -Eeuo/p' "$0" | sed '$d; s/^# \{0,1\}//'; exit 0 ;;
        *) die "bilinmeyen seçenek: $1" ;;
    esac
    shift
done

[ "$(id -u)" = 0 ] || die "root olarak çalıştırın: sudo bash $0"
mkdir -p "$SRC" "$CONF"
exec > >(tee -a "$LOG") 2>&1
trap 'die "satır $LINENO başarısız oldu"' ERR
say "$(date '+%F %T') başlıyor"

# shellcheck disable=SC1091
. /etc/os-release
say "Sistem: ${PRETTY_NAME:-$ID}, $(nproc) işlemci, sanallaştırma: $(systemd-detect-virt 2>/dev/null || echo bilinmiyor)"

# ── Packages ─────────────────────────────────────────────────────────────────
if command -v apt-get >/dev/null; then FAMILY=apt
elif command -v dnf >/dev/null; then FAMILY=dnf
elif command -v zypper >/dev/null; then FAMILY=zypper
elif command -v pacman >/dev/null; then FAMILY=pacman
else die "desteklenmeyen dağıtım: apt, dnf, zypper ya da pacman gerekli"
fi

# installed CMD reports whether a program is on the system.
installed() { command -v "$1" >/dev/null 2>&1; }

# The distribution's xrdp would fight this one for the same files and port.
remove_packaged_xrdp() {
    case $FAMILY in
        apt) if dpkg -s xrdp >/dev/null 2>&1 || dpkg -s xorgxrdp >/dev/null 2>&1; then
                 say "Dağıtımın xrdp paketi kaldırılıyor (ayarlar yerinde kalır)"
                 apt-get remove -y xrdp xorgxrdp
             fi ;;
        dnf) if rpm -q xrdp >/dev/null 2>&1; then dnf remove -y xrdp xorgxrdp; fi ;;
        zypper) if rpm -q xrdp >/dev/null 2>&1; then zypper -n rm xrdp xorgxrdp; fi ;;
        pacman) if pacman -Q xrdp >/dev/null 2>&1; then pacman -R --noconfirm xrdp xorgxrdp || true; fi ;;
    esac
}

H264=x264
install_build_deps() {
    say "Derleme araçları ve kitaplıklar kuruluyor"
    case $FAMILY in
    apt)
        export DEBIAN_FRONTEND=noninteractive
        apt-get update
        apt-get install -y --no-install-recommends \
            build-essential pkg-config autoconf automake libtool nasm curl ca-certificates \
            openssl xz-utils libssl-dev libpam0g-dev libx11-dev libxfixes-dev libxrandr-dev \
            libfuse3-dev fuse3 libx264-dev libpixman-1-dev libopus-dev \
            xserver-xorg-core xserver-xorg-dev libepoxy-dev libgbm-dev libdrm-dev \
            xauth dbus-x11 x11-xserver-utils
        ;;
    dnf)
        dnf install -y gcc gcc-c++ make autoconf automake libtool nasm pkgconf-pkg-config curl \
            openssl xz openssl-devel pam-devel libX11-devel libXfixes-devel libXrandr-devel \
            fuse3-devel fuse3 pixman-devel opus-devel \
            xorg-x11-server-devel xorg-x11-server-Xorg libepoxy-devel mesa-libgbm-devel \
            libdrm-devel xorg-x11-xauth dbus-x11
        # x264 lives in RPM Fusion; Fedora's own repositories carry Cisco's
        # OpenH264 instead.
        if dnf install -y x264-devel; then H264=x264
        elif dnf install -y openh264-devel; then H264=openh264
        else die "H.264 kodlayıcı yok: RPM Fusion'ı (x264-devel) ya da fedora-cisco-openh264 deposunu (openh264-devel) açın"
        fi
        ;;
    zypper)
        zypper -n in -t pattern devel_basis || true
        zypper -n in gcc gcc-c++ make autoconf automake libtool nasm pkg-config curl openssl xz \
            libopenssl-devel pam-devel libX11-devel libXfixes-devel libXrandr-devel \
            fuse3-devel fuse3 libpixman-1-0-devel libopus-devel \
            xorg-x11-server xorg-x11-server-sdk libepoxy-devel libgbm-devel libdrm-devel xauth
        # x264 comes from Packman; Cisco's OpenH264 from its own repository.
        if zypper -n in libx264-devel || zypper -n in x264-devel; then H264=x264
        elif zypper -n in openh264-devel; then H264=openh264
        else die "H.264 kodlayıcı yok: Packman deposunu (libx264-devel) ya da openh264 deposunu açın"
        fi
        ;;
    pacman)
        pacman -Sy --noconfirm --needed base-devel nasm curl openssl xz pam libx11 libxfixes libxrandr \
            fuse3 x264 pixman opus xorg-server xorg-server-devel libepoxy mesa libdrm xorg-xauth
        ;;
    esac
}

# fetch URL SHA256 FILE downloads into $SRC once, and checks it.
fetch() {
    local url=$1 sum=$2 file=$SRC/$3
    if [ -f "$file" ] && echo "$sum  $file" | sha256sum -c --quiet - >/dev/null 2>&1; then
        return
    fi
    curl -fsSL --retry 3 -o "$file.part" "$url" || die "indirilemedi: $url"
    echo "$sum  $file.part" | sha256sum -c --quiet - || die "sağlama tutmadı: $url"
    mv "$file.part" "$file"
}

# unpack ARCHIVE DIR gives a clean tree to build in.
unpack() {
    rm -rf "${SRC:?}/$2"
    tar -xf "$SRC/$1" -C "$SRC"
    cd "$SRC/$2"
}

# ── xrdp and xorgxrdp ────────────────────────────────────────────────────────
build_xrdp() {
    fetch "https://github.com/neutrinolabs/xrdp/releases/download/v$XRDP_VERSION/xrdp-$XRDP_VERSION.tar.gz" \
        "$XRDP_SHA256" "xrdp-$XRDP_VERSION.tar.gz"
    fetch "https://github.com/neutrinolabs/xorgxrdp/releases/download/v$XORGXRDP_VERSION/xorgxrdp-$XORGXRDP_VERSION.tar.gz" \
        "$XORGXRDP_SHA256" "xorgxrdp-$XORGXRDP_VERSION.tar.gz"

    say "xrdp $XRDP_VERSION derleniyor ($H264)"
    local units
    units=$(pkg-config --variable=systemdsystemunitdir systemd 2>/dev/null || true)
    [ -n "$units" ] || units=/usr/lib/systemd/system
    unpack "xrdp-$XRDP_VERSION.tar.gz" "xrdp-$XRDP_VERSION"
    ./configure --prefix=/usr --sysconfdir=/etc --localstatedir=/var \
        --with-systemdsystemunitdir="$units" \
        --enable-"$H264" --enable-pixman --enable-fuse --enable-opus --enable-ipv6
    make -j"$(nproc)"
    make install

    say "xorgxrdp $XORGXRDP_VERSION derleniyor"
    unpack "xorgxrdp-$XORGXRDP_VERSION.tar.gz" "xorgxrdp-$XORGXRDP_VERSION"
    ./configure --prefix=/usr
    make -j"$(nproc)"
    make install
}

# ── Settings ─────────────────────────────────────────────────────────────────
# set_key FILE KEY VALUE sets an existing "key=value" or "key = value" line.
set_key() {
    sed -i -E "s|^([#; ]*)?($2)[[:space:]]*=.*|\2=$3|" "$1"
}

configure_xrdp() {
    say "xrdp ayarlanıyor"
    local ini=/etc/xrdp/xrdp.ini gfx=/etc/xrdp/gfx.toml kb=/etc/xrdp/xrdp_keyboard.ini

    # A frame every 8 ms, where the default is 16: what the kiosks show at up
    # to 120 Hz instead of 60. Full colour.
    set_key "$ini" h264_frame_interval 8
    set_key "$ini" max_bpp 32

    # x264 told the frame rate it now gets, and, with cores to spare, two
    # threads: each frame is cut in two slices, encoded side by side.
    local threads=1
    [ "$(nproc)" -ge 4 ] && threads=2
    sed -i -E "s/^(fps_num[[:space:]]*=[[:space:]]*).*/\1120/; s/^(threads[[:space:]]*=[[:space:]]*)[0-9]+/\1$threads/" "$gfx"

    # The Turkish keyboards. The kiosk sends the layout as Windows does, and
    # xrdp maps only the layouts in this file; any other gets "us".
    if ! grep -q '^rdp_layout_tr=' "$kb"; then
        sed -i '/^\[default_rdp_layouts\]/a rdp_layout_tr=0x0000041F\nrdp_layout_tr_f=0x0001041F' "$kb"
        sed -i '/^\[default_layouts_map\]/a rdp_layout_tr=tr\nrdp_layout_tr_f=tr(f)' "$kb"
    fi

    # TLS. make install creates a self-signed certificate; keep whatever is
    # there, a real one included.
    if [ ! -s /etc/xrdp/cert.pem ] || [ ! -s /etc/xrdp/key.pem ]; then
        openssl req -x509 -newkey rsa:2048 -sha256 -nodes -days 3650 \
            -keyout /etc/xrdp/key.pem -out /etc/xrdp/cert.pem -subj "/CN=$(hostname)" 2>/dev/null
        chmod 600 /etc/xrdp/key.pem
    fi

    # The X server itself, not the /usr/bin/Xorg script: that runs the setuid
    # Xorg.wrap where there is one (Fedora; Debian, Ubuntu and Mint with
    # xserver-xorg-legacy), which starts X only for a user at the console —
    # and a remote session never is.
    local x
    for x in /usr/lib/xorg/Xorg /usr/libexec/Xorg /usr/lib/Xorg; do
        if [ -x "$x" ]; then
            sed -i -E "s#^param=(Xorg|/[^ ]*/Xorg)\$#param=$x#" /etc/xrdp/sesman.ini
            break
        fi
    done
}

# pick_session prints the desktop remote sessions start, from
# /usr/share/xsessions: X11 only — xrdp runs its own X server, and a
# Wayland-only desktop cannot start there.
pick_session() {
    local d=/usr/share/xsessions s
    if [ -n "$WANT_SESSION" ]; then
        [ -r "$d/$WANT_SESSION.desktop" ] || die "$d/$WANT_SESSION.desktop yok"
        echo "$WANT_SESSION"
        return
    fi
    # Cinnamon's software-rendering session first: an RDP session has no GPU
    # whichever is chosen, and that one does not warn about it at every login.
    for s in cinnamon2d cinnamon mate xfce plasmax11 plasma ubuntu-xorg gnome-xorg budgie-desktop \
             lxqt LXDE lxde; do
        if [ -r "$d/$s.desktop" ]; then
            echo "$s"
            return
        fi
    done
    for s in "$d"/*.desktop; do
        if [ -r "$s" ]; then
            basename "$s" .desktop
            return
        fi
    done
}

# check_session stops before the long build when there is no desktop to
# start remotely.
check_session() {
    SESSION=$(pick_session)
    if [ -z "$SESSION" ]; then
        die "X11 masaüstü bulunamadı (/usr/share/xsessions boş). Wayland'e özel bir masaüstüyle (yeni GNOME) xrdp oturum açamaz; Xfce, MATE ya da Cinnamon kurup yeniden çalıştırın."
    fi
}

configure_session() {
    local session=$SESSION
    echo "$session" > "$CONF/session"
    say "Uzak oturum masaüstü: $session (~/.xsession dosyası olan kullanıcılar kendininkini açar)"

    cat > /etc/xrdp/startwm.sh <<'EOF'
#!/bin/sh
# Written by simpleclient-server.sh: start a remote session the way the
# display manager would — the user's ~/.xsession if there is one, else the
# desktop chosen for this machine in /etc/simpleclient/session.
[ -r /etc/profile ] && . /etc/profile
[ -r "$HOME/.profile" ] && . "$HOME/.profile"
if [ -r /etc/default/locale ]; then
    . /etc/default/locale
    export LANG LANGUAGE LC_ALL LC_CTYPE LC_MESSAGES LC_TIME LC_NUMERIC LC_MONETARY LC_PAPER
fi
if [ -r /etc/locale.conf ]; then
    . /etc/locale.conf
    export LANG
fi

if [ -r "$HOME/.xsession" ]; then
    START="sh $HOME/.xsession"
else
    SESSION=$(cat /etc/simpleclient/session 2>/dev/null)
    FILE=/usr/share/xsessions/$SESSION.desktop
    if [ -r "$FILE" ]; then
        START=$(sed -n 's/^Exec=//p' "$FILE" | head -n 1)
        NAMES=$(sed -n 's/^DesktopNames=//p' "$FILE" | head -n 1 | tr ';' ':' | sed 's/:$//')
        export DESKTOP_SESSION="$SESSION" XDG_SESSION_DESKTOP="$SESSION" XDG_SESSION_TYPE=x11
        [ -n "$NAMES" ] && export XDG_CURRENT_DESKTOP="$NAMES"
    fi
fi

# The distribution's Xsession sets up what its display manager would (D-Bus,
# agents, input methods) before running the session.
if [ -x /etc/X11/Xsession ]; then
    exec /etc/X11/Xsession ${START:+"$START"}
elif [ -x /etc/X11/xinit/Xsession ]; then
    exec /etc/X11/xinit/Xsession ${START:+"$START"}
fi
exec sh -c "${START:-xterm}"
EOF
    chmod 755 /etc/xrdp/startwm.sh
}

# Remote sessions are not at the console, so polkit asks for a password — on
# every login, for things no one asked for: colour profiles for the virtual
# display, refreshing the package lists. And it refuses them the smart-card
# daemon outright, which allows only the user at the console: a card in a
# kiosk's reader would be unusable in the session it was put in for.
configure_polkit() {
    local actions=(org.freedesktop.color-manager.create-device org.freedesktop.color-manager.create-profile
                   org.freedesktop.color-manager.delete-device org.freedesktop.color-manager.delete-profile
                   org.freedesktop.color-manager.modify-device org.freedesktop.color-manager.modify-profile
                   org.freedesktop.packagekit.system-sources-refresh
                   org.debian.pcsc-lite.access_pcsc org.debian.pcsc-lite.access_card)
    if [ -d /etc/polkit-1/rules.d ] || [ -d /usr/share/polkit-1/rules.d ]; then
        mkdir -p /etc/polkit-1/rules.d
        {
            echo "// Written by simpleclient-server.sh: no password prompts in remote"
            echo "// sessions for colour profiles and package lists; smart cards."
            echo "polkit.addRule(function(action, subject) {"
            echo "    var quiet = [$(printf '"%s", ' "${actions[@]}" | sed 's/, $//')];"
            echo "    if (quiet.indexOf(action.id) >= 0 && subject.local === false) {"
            echo "        return polkit.Result.YES;"
            echo "    }"
            echo "});"
        } > /etc/polkit-1/rules.d/48-simpleclient-xrdp.rules
    fi
    # polkit 0.105 (Ubuntu 22.04, Linux Mint 21) reads .pkla files instead.
    if [ -d /etc/polkit-1/localauthority ]; then
        mkdir -p /etc/polkit-1/localauthority/50-local.d
        cat > /etc/polkit-1/localauthority/50-local.d/48-simpleclient-xrdp.pkla <<EOF
[SimpleClient remote sessions]
Identity=unix-user:*
Action=$(IFS=';'; echo "${actions[*]}")
ResultAny=yes
ResultInactive=yes
ResultActive=yes
EOF
    fi
}

# ── Sound ────────────────────────────────────────────────────────────────────
audio_build_deps() {
    case $FAMILY in
        apt) apt-get install -y --no-install-recommends "$@" ;;
        dnf) dnf install -y "$@" ;;
        zypper) zypper -n in "$@" ;;
        pacman) pacman -S --noconfirm --needed "$@" ;;
    esac
}

setup_pipewire() {
    say "Ses: PipeWire modülü derleniyor"
    case $FAMILY in
        apt) audio_build_deps libpipewire-0.3-dev libspa-0.2-dev ;;
        dnf) audio_build_deps pipewire-devel ;;
        zypper) audio_build_deps pipewire-devel ;;
        pacman) audio_build_deps pipewire ;;
    esac
    fetch "https://github.com/neutrinolabs/pipewire-module-xrdp/archive/refs/tags/v$PWMOD_VERSION.tar.gz" \
        "$PWMOD_SHA256" "pipewire-module-xrdp-$PWMOD_VERSION.tar.gz"
    unpack "pipewire-module-xrdp-$PWMOD_VERSION.tar.gz" "pipewire-module-xrdp-$PWMOD_VERSION"
    ./bootstrap
    ./configure --prefix=/usr --sysconfdir=/etc
    make -j"$(nproc)"
    make install
}

# The PulseAudio modules are built against PulseAudio's own source, of the
# version installed: its module API is internal and not in any development
# package. Only its configuration header is needed, which meson writes
# without most of PulseAudio's build dependencies.
setup_pulseaudio() {
    local v
    v=$(pulseaudio --version 2>/dev/null | awk '{print $2}')
    v=${v%%-*}
    [ -n "$v" ] || { warn "PulseAudio sürümü okunamadı; ses atlandı"; return; }
    say "Ses: PulseAudio $v modülü derleniyor"
    case $FAMILY in
        apt) audio_build_deps meson ninja-build m4 libsndfile1-dev libltdl-dev libpulse-dev ;;
        dnf) audio_build_deps meson ninja-build m4 libsndfile-devel libtool-ltdl-devel pulseaudio-libs-devel ;;
        zypper) audio_build_deps meson ninja m4 libsndfile-devel libltdl7 libpulse-devel ;;
        pacman) audio_build_deps meson ninja m4 libsndfile libtool libpulse ;;
    esac
    curl -fsSL --retry 3 -o "$SRC/pulseaudio-$v.tar.xz" \
        "https://freedesktop.org/software/pulseaudio/releases/pulseaudio-$v.tar.xz" \
        || { warn "PulseAudio $v kaynağı indirilemedi; ses atlandı"; return; }
    unpack "pulseaudio-$v.tar.xz" "pulseaudio-$v"
    local opts="-Ddaemon=false -Ddoxygen=false -Dman=false -Dtests=false -Ddatabase=simple" f
    for f in alsa asyncns avahi bluez5 dbus fftw glib gsettings gtk jack lirc openssl orc samplerate \
             soxr speex systemd tcpwrap udev valgrind x11 webrtc-aec gstreamer elogind consolekit \
             oss-output; do
        grep -q "option('$f'" meson_options.txt && opts="$opts -D$f=disabled"
    done
    for f in hal-compat adrian-aec ipv6; do
        grep -q "option('$f'" meson_options.txt && opts="$opts -D$f=false"
    done
    # shellcheck disable=SC2086
    meson setup build $opts
    local pa=$SRC/pulseaudio-$v
    fetch "https://github.com/neutrinolabs/pulseaudio-module-xrdp/archive/refs/tags/v$PAMOD_VERSION.tar.gz" \
        "$PAMOD_SHA256" "pulseaudio-module-xrdp-$PAMOD_VERSION.tar.gz"
    unpack "pulseaudio-module-xrdp-$PAMOD_VERSION.tar.gz" "pulseaudio-module-xrdp-$PAMOD_VERSION"
    ./bootstrap
    ./configure PULSE_DIR="$pa" PULSE_CONFIG_DIR="$pa/build"
    make -j"$(nproc)"
    make install
}

setup_audio() {
    if installed pipewire-pulse; then
        local v
        v=$(pipewire --version 2>/dev/null | sed -n 's/.*libpipewire \([0-9.]*\).*/\1/p' | tail -n 1)
        if [ -n "$v" ] && [ "$(printf '%s\n' 0.3.58 "$v" | sort -V | head -n 1)" = 0.3.58 ]; then
            setup_pipewire
            return
        fi
    fi
    if installed pulseaudio; then
        setup_pulseaudio
        return
    fi
    warn "PipeWire (0.3.58+) ya da PulseAudio bulunamadı; ses atlandı"
}

# ── USB ──────────────────────────────────────────────────────────────────────
# A kiosk's drives come through RDP, by name. Everything else it offers —
# printers, scanners, smart-card readers, serial adapters — is attached here
# over USB/IP by the agent below, which the kiosks reach on port 3241: the
# devices appear as if plugged into this machine, and go when the session
# ends. That takes the kernel's vhci-hcd; smart cards also take the PC/SC
# daemon and the CCID driver.
USB_READY=0
setup_usb() {
    if ! modprobe vhci-hcd 2>/dev/null; then
        case $FAMILY in
            apt) apt-get install -y "linux-modules-extra-$(uname -r)" || true ;;
            dnf) dnf install -y "kernel-modules-extra-$(uname -r)" || true ;;
        esac
        if ! modprobe vhci-hcd 2>/dev/null; then
            warn "Bu çekirdekte USB/IP (vhci-hcd) yok: kiosklardaki yazıcı, tarayıcı ve kart okuyucular bu makineye takılamaz; USB bellekler yine gelir"
            return
        fi
    fi
    say "USB: kiosklardaki yazıcı, tarayıcı, kart okuyucu ve seri aygıtlar bu makineye USB/IP ile takılır"
    case $FAMILY in
        apt) apt-get install -y --no-install-recommends python3 pcscd libccid acl ;;
        dnf) dnf install -y python3 pcsc-lite pcsc-lite-ccid acl ;;
        zypper) zypper -n in python3 pcsc-lite pcsc-ccid acl ;;
        pacman) pacman -S --noconfirm --needed python pcsclite ccid acl ;;
    esac
    echo vhci-hcd > /etc/modules-load.d/simpleclient-usb.conf
    write_usb_agent
    cat > /etc/systemd/system/simpleclient-usb.service <<'UNIT'
[Unit]
Description=SimpleClient USB agent: the kiosks' USB devices over USB/IP
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/bin/env python3 /usr/local/sbin/simpleclient-usb-agent
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
UNIT
    USB_READY=1
}

write_usb_agent() {
    cat > /usr/local/sbin/simpleclient-usb-agent <<'AGENT'
#!/usr/bin/env python3
# simpleclient-usb-agent, written by simpleclient-server.sh.
#
# Attaches the USB devices of SimpleClient kiosks that have a session open on
# this machine: printers, scanners, smart-card readers, serial adapters appear
# here as if plugged in, for as long as the kiosk stays connected.
#
# A kiosk connects to port 3241 when its session starts and keeps the
# connection open; it offers its devices over USB/IP on its own port 3240, to
# this machine only. Only a kiosk with an RDP connection open here is served.
# Nothing that would act as a keyboard or mouse here is let through: the
# interfaces of attached devices start unauthorized, and only the others are
# authorized. Each device's nodes are given to the user whose session the
# kiosk is in, as a desktop does for the user at its console.
import errno
import glob
import ipaddress
import logging
import os
import re
import socket
import struct
import subprocess
import sys
import threading
import time

AGENT_PORT = 3241
USBIP_PORT = 3240
RDP_PORT = int(os.environ.get("SIMPLECLIENT_RDP_PORT", "3389"))
VHCI = "/sys/devices/platform/vhci_hcd.0"
USB = "/sys/bus/usb/devices"
SESMAN_LOG = "/var/log/xrdp-sesman.log"

USBIP_VERSION = 0x0111
OP_REQ_DEVLIST, OP_REP_DEVLIST = 0x8005, 0x0005
OP_REQ_IMPORT, OP_REP_IMPORT = 0x8003, 0x0003
DEVICE_LEN = 312
VDEV_ST_NULL, VDEV_ST_USED = 4, 6
CLASS_HID, CLASS_HUB = 0x03, 0x09

log = logging.getLogger("simpleclient-usb")

# vhci's ports are shared by every kiosk: which one each attached device is
# on, and whose it is.
lock = threading.Lock()
owners = {}  # port -> Kiosk
kiosks = {}  # ip -> Kiosk


def read(path):
    try:
        with open(path) as f:
            return f.read().strip()
    except OSError:
        return ""


def write(path, value):
    with open(path, "w") as f:
        f.write(value)


def normal_ip(addr):
    ip = ipaddress.ip_address(addr.split("%", 1)[0])
    if ip.version == 6 and ip.ipv4_mapped:
        ip = ip.ipv4_mapped
    return str(ip)


def recv_exact(sock, n):
    buf = b""
    while len(buf) < n:
        chunk = sock.recv(n - len(buf))
        if not chunk:
            raise ConnectionError("connection closed")
        buf += chunk
    return buf


def parse_device(rec):
    (busnum, devnum, speed, vid, pid, _bcd, cls, _sub, _proto, _conf, _nconf,
     nintf) = struct.unpack(">IIIHHHBBBBBB", rec[288:312])
    return {"busid": rec[256:288].split(b"\0", 1)[0].decode(), "busnum": busnum,
            "devnum": devnum, "speed": speed, "vid": vid, "pid": pid, "class": cls,
            "nintf": nintf}


def devlist(ip):
    """The devices a kiosk offers, from its USB/IP port."""
    with socket.create_connection((ip, USBIP_PORT), timeout=5) as s:
        s.sendall(struct.pack(">HHI", USBIP_VERSION, OP_REQ_DEVLIST, 0))
        _, code, status = struct.unpack(">HHI", recv_exact(s, 8))
        if code != OP_REP_DEVLIST or status != 0:
            raise ConnectionError("device list refused")
        (n,) = struct.unpack(">I", recv_exact(s, 4))
        devs = []
        for _ in range(n):
            d = parse_device(recv_exact(s, DEVICE_LEN))
            d["classes"] = [recv_exact(s, 4)[0] for _ in range(d["nintf"])]
            devs.append(d)
        return devs


def ports():
    """vhci's ports: (port, hub, status, devid, local bus ID)."""
    out = []
    for name in sorted(os.listdir(VHCI)):
        if name != "status" and not name.startswith("status."):
            continue
        for line in read(os.path.join(VHCI, name)).splitlines()[1:]:
            f = line.split()
            if len(f) >= 7 and f[0] in ("hs", "ss"):
                out.append((int(f[1]), f[0], int(f[2]), int(f[4], 16), f[6]))
    return out


def import_device(ip, dev):
    """Asks a kiosk for a device: the connection it is now carried on, and the
    device as the kiosk describes it."""
    s = socket.create_connection((ip, USBIP_PORT), timeout=10)
    try:
        s.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
        # A kiosk switched off mid-session is noticed in half a minute, and
        # its devices unplugged here.
        s.setsockopt(socket.SOL_SOCKET, socket.SO_KEEPALIVE, 1)
        s.setsockopt(socket.IPPROTO_TCP, socket.TCP_KEEPIDLE, 10)
        s.setsockopt(socket.IPPROTO_TCP, socket.TCP_KEEPINTVL, 5)
        s.setsockopt(socket.IPPROTO_TCP, socket.TCP_KEEPCNT, 4)
        busid = dev["busid"].encode().ljust(32, b"\0")
        s.sendall(struct.pack(">HHI", USBIP_VERSION, OP_REQ_IMPORT, 0) + busid)
        _, code, status = struct.unpack(">HHI", recv_exact(s, 8))
        if code != OP_REP_IMPORT or status != 0:
            raise ConnectionError("refused by the kiosk (status %d)" % status)
        d = parse_device(recv_exact(s, DEVICE_LEN))
        if d["busid"] != dev["busid"]:
            raise ConnectionError("the kiosk answered for another device")
        return s, d
    except BaseException:
        s.close()
        raise


def plug(s, d):
    """Plugs an imported device into a free vhci port: the port and the
    device's ID there. Callers hold lock."""
    devid = (d["busnum"] << 16) | d["devnum"]
    hub = "ss" if d["speed"] >= 5 else "hs"
    for port, h, st, _, _ in ports():
        if h != hub or st != VDEV_ST_NULL:
            continue
        try:
            write(VHCI + "/attach", "%d %d %d %d" % (port, s.fileno(), devid, d["speed"]))
            return port, devid
        except OSError as e:
            if e.errno != errno.EBUSY:
                raise
    raise OSError("no free USB/IP port")


def detach(port):
    try:
        write(VHCI + "/detach", str(port))
    except OSError:
        pass


def session_user(ip):
    """The user whose session the kiosk at ip is in: the last login sesman
    took from there."""
    try:
        with open(SESMAN_LOG, "rb") as f:
            f.seek(0, os.SEEK_END)
            f.seek(max(0, f.tell() - (1 << 20)))
            text = f.read().decode("utf-8", "replace")
    except OSError:
        return None
    user = None
    for m in re.finditer(r"login request from xrdp for user: (\S+) IP: (\S+)", text):
        try:
            if normal_ip(m.group(2)) == ip:
                user = m.group(1)
        except ValueError:
            pass
    return user


def rdp_peers():
    """The addresses with an RDP connection open to this machine."""
    peers = set()
    for name in ("/proc/net/tcp", "/proc/net/tcp6"):
        try:
            with open(name) as f:
                lines = f.read().splitlines()[1:]
        except OSError:
            continue
        for line in lines:
            f = line.split()
            if len(f) < 4 or f[3] != "01" or int(f[1].rsplit(":", 1)[1], 16) != RDP_PORT:
                continue
            raw = bytes.fromhex(f[2].rsplit(":", 1)[0])
            if sys.byteorder == "little":
                # Each 32-bit word is printed in host order.
                raw = b"".join(raw[i:i + 4][::-1] for i in range(0, len(raw), 4))
            peers.add(normal_ip(str(ipaddress.ip_address(raw))))
    return peers


def device_nodes(busid):
    """The device nodes of a USB device and of its interfaces' drivers."""
    nodes = []
    for root, _, files in os.walk(os.path.join(USB, busid)):
        if "uevent" in files:
            m = re.search(r"^DEVNAME=(.+)$", read(os.path.join(root, "uevent")), re.M)
            if m:
                nodes.append("/dev/" + m.group(1))
    return nodes


class Kiosk(threading.Thread):
    def __init__(self, conn, ip):
        super().__init__(daemon=True)
        self.conn, self.ip, self.buf = conn, ip, b""
        self.ports = {}  # bus ID on the kiosk -> (port, devid)
        self.user = None

    def run(self):
        try:
            self.serve()
        except Exception as e:  # noqa: BLE001 - one kiosk must not stop the others
            log.info("%s: %s", self.ip, e)
        finally:
            with lock:
                for busid, (port, _) in self.ports.items():
                    if owners.get(port) is self:
                        detach(port)
                        del owners[port]
                        log.info("%s: %s detached", self.ip, busid)
                if kiosks.get(self.ip) is self:
                    del kiosks[self.ip]
            self.conn.close()

    def readline(self):
        while b"\n" not in self.buf:
            chunk = self.conn.recv(256)
            if not chunk or len(self.buf) > 4096:
                return None
            self.buf += chunk
        line, self.buf = self.buf.split(b"\n", 1)
        return line.decode("ascii", "replace").strip()

    def serve(self):
        self.conn.settimeout(10)
        if not (self.readline() or "").startswith("SIMPLECLIENT-USB "):
            return
        self.conn.sendall(b"OK 1\n")
        self.conn.settimeout(2)
        start = heard = time.monotonic()
        rdp_seen, refresh, next_check = None, True, 0.0
        while True:
            try:
                line = self.readline()
                if line is None:
                    return
                heard = time.monotonic()
                if line.startswith("DEVICES"):
                    refresh = True
            except socket.timeout:
                pass
            now = time.monotonic()
            if now - heard > 35:
                log.info("%s: no word from the kiosk", self.ip)
                return
            if not refresh and now < next_check:
                continue
            next_check = now + 5
            if self.ip not in rdp_peers():
                # Its session is being set up, or reconnecting.
                if (rdp_seen is None and now - start > 60) or (rdp_seen and now - rdp_seen > 30):
                    log.info("%s: no RDP session from the kiosk", self.ip)
                    return
                continue
            rdp_seen = now
            self.sync(refresh)
            refresh = False

    def sync(self, refresh):
        if refresh or self.user is None:
            self.user = session_user(self.ip)
        with lock:
            state = {p: (st, devid, local) for p, _, st, devid, local in ports()}
            for busid, (port, devid) in list(self.ports.items()):
                st, dv, local = state.get(port, (0, 0, ""))
                if owners.get(port) is not self or st != VDEV_ST_USED or dv != devid:
                    # Unplugged at the kiosk: vhci let go of it by itself.
                    del self.ports[busid]
                    if owners.get(port) is self:
                        del owners[port]
                    log.info("%s: %s unplugged", self.ip, busid)
                else:
                    self.admit(local)
        if not refresh:
            return
        try:
            offered = devlist(self.ip)
        except OSError as e:
            log.info("%s: no device list: %s", self.ip, e)
            return
        for d in offered:
            if d["busid"] in self.ports:
                continue
            name = "%s (%04x:%04x)" % (d["busid"], d["vid"], d["pid"])
            if d["class"] == CLASS_HUB or any(c in (CLASS_HID, CLASS_HUB) for c in d["classes"]):
                log.info("%s: %s not attached: it would act as a keyboard or mouse here", self.ip, name)
                continue
            try:
                s, dev = import_device(self.ip, d)
                try:
                    with lock:
                        port, devid = plug(s, dev)
                        owners[port] = self
                finally:
                    # The kernel holds the connection now.
                    s.close()
            except Exception as e:  # noqa: BLE001
                log.info("%s: %s not attached: %s", self.ip, name, e)
                continue
            self.ports[d["busid"]] = (port, devid)
            log.info("%s: %s attached on port %d%s", self.ip, name, port,
                     " for " + self.user if self.user else "")
            threading.Thread(target=self.settle, args=(port, devid), daemon=True).start()

    def settle(self, port, devid):
        """Admits a just-attached device's interfaces as they appear."""
        for _ in range(20):
            time.sleep(0.5)
            with lock:
                if owners.get(port) is not self:
                    return
                for p, _, st, dv, local in ports():
                    if p == port and st == VDEV_ST_USED and dv == devid:
                        self.admit(local)

    def admit(self, local):
        """Authorizes a device's interfaces but input ones, and gives its
        nodes to the session's user."""
        for intf in glob.glob(os.path.join(USB, local, local + ":*")):
            cls = read(os.path.join(intf, "bInterfaceClass"))
            if cls and int(cls, 16) != CLASS_HID and read(os.path.join(intf, "authorized")) == "0":
                try:
                    write(os.path.join(intf, "authorized"), "1")
                except OSError as e:
                    log.info("%s: %s: %s", self.ip, intf, e)
        if self.user:
            for node in device_nodes(local):
                try:
                    subprocess.run(["setfacl", "-m", "u:%s:rw" % self.user, node],
                                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
                except OSError:
                    return


def main():
    logging.basicConfig(level=logging.INFO, format="%(message)s", stream=sys.stdout)
    subprocess.run(["modprobe", "vhci-hcd"], check=False)
    if not os.path.isdir(VHCI):
        log.error("vhci-hcd is not available in this kernel: USB/IP cannot attach anything")
        sys.exit(1)
    for hub in glob.glob(VHCI + "/usb[0-9]*"):
        write(hub + "/interface_authorized_default", "0")
    # What a previous run attached belongs to no kiosk now; each one in a
    # session connects again within seconds and gets its devices back.
    for port, _, st, _, _ in ports():
        if st == VDEV_ST_USED:
            detach(port)
    try:
        srv = socket.create_server(("", AGENT_PORT), family=socket.AF_INET6, dualstack_ipv6=True)
    except OSError:
        srv = socket.create_server(("", AGENT_PORT))
    log.info("listening on port %d for kiosks with an RDP session on port %d", AGENT_PORT, RDP_PORT)
    while True:
        conn, addr = srv.accept()
        ip = normal_ip(addr[0])
        k = Kiosk(conn, ip)
        with lock:
            old = kiosks.get(ip)
            kiosks[ip] = k
        if old:
            # The kiosk connected again: the old connection is dead, and
            # its devices go back before the new one takes them.
            try:
                old.conn.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            old.join(10)
        k.start()


if __name__ == "__main__":
    main()
AGENT
    chmod 755 /usr/local/sbin/simpleclient-usb-agent
}

# ── Firewall and services ────────────────────────────────────────────────────
open_firewall() {
    local ports=(3389) p
    if [ "$USB_READY" = 1 ]; then
        ports+=(3241)
    fi
    if installed ufw && ufw status 2>/dev/null | grep -q "Status: active"; then
        for p in "${ports[@]}"; do
            ufw allow "$p/tcp"
        done
    fi
    if installed firewall-cmd && firewall-cmd --state >/dev/null 2>&1; then
        for p in "${ports[@]}"; do
            firewall-cmd --permanent --add-port="$p/tcp"
        done
        firewall-cmd --reload
    fi
}

start_services() {
    # Freshly installed files take the SELinux labels the policy gives
    # xrdp's own paths.
    if installed selinuxenabled && selinuxenabled; then
        restorecon -R /usr/sbin/xrdp* /etc/xrdp /usr/lib*/xrdp /usr/local/sbin 2>/dev/null || true
    fi
    if [ -d /run/systemd/system ]; then
        systemctl daemon-reload
        systemctl enable xrdp-sesman xrdp
        systemctl restart xrdp-sesman xrdp
        if [ "$USB_READY" = 1 ]; then
            systemctl enable --now pcscd.socket 2>/dev/null || true
            systemctl enable simpleclient-usb
            systemctl restart simpleclient-usb
        fi
        sleep 1
        systemctl is-active --quiet xrdp || die "xrdp başlamadı: journalctl -u xrdp"
        if [ "$USB_READY" = 1 ] && ! systemctl is-active --quiet simpleclient-usb; then
            warn "USB ajanı başlamadı: journalctl -u simpleclient-usb"
        fi
    else
        local extra=""
        if [ "$USB_READY" = 1 ]; then
            extra=", pcscd ve simpleclient-usb-agent"
        fi
        warn "systemd çalışmıyor: xrdp-sesman, xrdp$extra elle başlatılmalı"
    fi
}

# ── Main ─────────────────────────────────────────────────────────────────────
SESSION=""
check_session
remove_packaged_xrdp
install_build_deps
build_xrdp
configure_xrdp
configure_session
configure_polkit
if [ "$AUDIO" = 1 ]; then
    setup_audio
fi
if [ "$USB" = 1 ]; then
    setup_usb
fi
open_firewall
start_services

case $(systemd-detect-virt 2>/dev/null || true) in
    vmware|kvm|microsoft|oracle|xen)
        say "Sanal makine: H.264 işlemcide kodlanır. Aynı anda bağlanan her kiosk için 2 vCPU ayırın"
        say "(şu an $(nproc)); masaüstü de yazılımla çizilir, Cinnamon yerine MATE ya da Xfce daha hafiftir."
        ;;
esac
ADDR=$(ip -4 -o route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p')
say "Hazır: xrdp $XRDP_VERSION ($H264), port 3389${ADDR:+, adres $ADDR}."
say "Kiosk bu sunucuyu ağ taramasında bulur; başka bir alt ağdaysa F7 ile adresini girin."
