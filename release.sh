#!/bin/bash
# Cross-compile the release binaries into dist/. Upload them to prisnap.com/peek/.
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
