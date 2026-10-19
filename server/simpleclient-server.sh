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
#   - the desktop the machine already has (Cinnamon, MATE, Xfce, KDE Plasma,
#     GNOME, ...) for every user without a ~/.xsession of their own
#   - no polkit password prompts for colour profiles and package lists,
#     which a remote session otherwise gets on every login
#   - port 3389 open in ufw or firewalld
# It can be run again: to upgrade, or after a distribution upgrade has
# replaced the libraries xrdp was built against.
#
# Usage:  sudo bash simpleclient-server.sh [--session NAME] [--no-audio]
#   --session NAME  the desktop for remote sessions, a file name from
#                   /usr/share/xsessions without .desktop (cinnamon, mate,
#                   xfce, plasmax11, ubuntu-xorg, ...); default: the first of
#                   those this machine has
#   --no-audio      leave sound alone
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
while [ $# -gt 0 ]; do
    case "$1" in
        --session) WANT_SESSION=${2:-}; shift ;;
        --session=*) WANT_SESSION=${1#--session=} ;;
        --no-audio) AUDIO=0 ;;
        -h|--help) sed -n '2,/^set -euo/p' "$0" | sed '$d; s/^# \{0,1\}//'; exit 0 ;;
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
# display, refreshing the package lists.
configure_polkit() {
    local actions=(org.freedesktop.color-manager.create-device org.freedesktop.color-manager.create-profile
                   org.freedesktop.color-manager.delete-device org.freedesktop.color-manager.delete-profile
                   org.freedesktop.color-manager.modify-device org.freedesktop.color-manager.modify-profile
                   org.freedesktop.packagekit.system-sources-refresh)
    if [ -d /etc/polkit-1/rules.d ] || [ -d /usr/share/polkit-1/rules.d ]; then
        mkdir -p /etc/polkit-1/rules.d
        {
            echo "// Written by simpleclient-server.sh: no password prompts in remote"
            echo "// sessions for colour profiles and package lists."
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

# ── Firewall and services ────────────────────────────────────────────────────
open_firewall() {
    if installed ufw && ufw status 2>/dev/null | grep -q "Status: active"; then
        ufw allow 3389/tcp
    fi
    if installed firewall-cmd && firewall-cmd --state >/dev/null 2>&1; then
        firewall-cmd --permanent --add-port=3389/tcp
        firewall-cmd --reload
    fi
}

start_services() {
    # Freshly installed files take the SELinux labels the policy gives
    # xrdp's own paths.
    if installed selinuxenabled && selinuxenabled; then
        restorecon -R /usr/sbin/xrdp* /etc/xrdp /usr/lib*/xrdp 2>/dev/null || true
    fi
    if [ -d /run/systemd/system ]; then
        systemctl daemon-reload
        systemctl enable xrdp-sesman xrdp
        systemctl restart xrdp-sesman xrdp
        sleep 1
        systemctl is-active --quiet xrdp || die "xrdp başlamadı: journalctl -u xrdp"
    else
        warn "systemd çalışmıyor: xrdp-sesman ve xrdp elle başlatılmalı"
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
