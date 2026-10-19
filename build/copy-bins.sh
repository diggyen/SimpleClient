#!/bin/sh
# copy-bins.sh ROOT BINARY... — copy each binary, and every shared library it
# links against, into ROOT at the same paths. The initramfs has no package
# manager; this is how a tool from the build container gets there whole.
set -e
root=$1
shift
for b in "$@"; do
    p=$(command -v "$b") || { echo "copy-bins: $b not found" >&2; exit 1; }
    mkdir -p "$root$(dirname "$p")"
    cp -L "$p" "$root$p"
    for l in $(ldd "$p" 2>/dev/null | awk '/=>/ {print $3} /ld-musl/ {print $1}'); do
        [ -f "$l" ] || continue
        mkdir -p "$root$(dirname "$l")"
        cp -L "$l" "$root$l"
    done
done
