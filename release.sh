#!/bin/bash
# Cross-compile the release binaries into dist/. They are ARTEFACTS: git carries none of them
# and neither does a worktree. The site that serves them ships with the binary now
# (modules/platformSite), so they are NOT part of it: they go into the platform tenant's
# override tree for that module, which is the tenant's own and is not committed.
#
# To publish them, copy dist/ over the server's
# tenants/platform/software/modules/platformSite/web/dl/
set -e

cd "$(dirname "$0")"
rm -rf dist
mkdir dist

build() {
	echo "building $1-$2"
	GOOS=$1 GOARCH=$2 go build -ldflags "-s -w" -o "dist/peek-$1-$2$3" .
}

build darwin arm64
build darwin amd64
build linux amd64
build linux arm64
build windows amd64 .exe

ls -lh dist
