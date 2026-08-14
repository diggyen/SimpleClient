#!/usr/bin/env bash
# build/vendor-patch.sh — regenerate vendor/ and re-apply the local grdp patches.
#
# vendor/ is committed to the repository, so this script is NOT part of the
# normal build. Run it only when a dependency version changes (go.mod edit),
# then commit the result.
#
# Why the patches exist: github.com/tomatome/grdp does not compile for Linux as
# published. See build/patches/*.patch for the two upstream defects and the
# exact fixes.
set -euo pipefail

cd "$(dirname "$0")/.."

echo "==> go mod vendor"
GOFLAGS=-mod=mod go mod vendor

echo "==> applying build/patches/*.patch"
for p in build/patches/*.patch; do
    echo "    $(basename "$p")"
    git apply --verbose "$p"
done

echo "==> verifying the Linux build"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -mod=vendor ./...

echo "OK — vendor/ regenerated and patched. Remember to commit vendor/."
