#!/bin/sh
set -eu

PKG_NAME=connection-recorder
BIN_DAEMON=networkmond
BIN_CLIENT=networkmonc
NFPM_VERSION=${NFPM_VERSION:-v2.46.3}
ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DIST_DIR="$ROOT_DIR/dist"
BUILD_DIR="$ROOT_DIR/build/package"
VERSION=${VERSION:-}

if [ -z "$VERSION" ]; then
	if git -C "$ROOT_DIR" describe --tags --exact-match >/dev/null 2>&1; then
		VERSION=$(git -C "$ROOT_DIR" describe --tags --exact-match)
	elif git -C "$ROOT_DIR" describe --tags --always >/dev/null 2>&1; then
		VERSION=$(git -C "$ROOT_DIR" describe --tags --always)
	else
		VERSION=0.0.0-dev
	fi
fi
VERSION=${VERSION#v}

usage() {
	cat <<EOF
Usage:
  scripts/package.sh deb amd64|arm64
  scripts/package.sh openwrt x86_64|aarch64_generic|aarch64_cortex-a53
  scripts/package.sh all
EOF
}

need() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "missing required command: $1" >&2
		exit 1
	fi
}

go_arch_for() {
	case "$1" in
		amd64|x86_64) echo amd64 ;;
		arm64|aarch64_generic|aarch64_cortex-a53) echo arm64 ;;
		*) echo "unsupported architecture: $1" >&2; exit 1 ;;
	esac
}

nfpm_arch_for() {
	format=$1
	arch=$2
	case "$format:$arch" in
		deb:amd64) echo amd64 ;;
		deb:arm64) echo arm64 ;;
		openwrt:x86_64) echo x86_64 ;;
		openwrt:aarch64_generic) echo aarch64_generic ;;
		openwrt:aarch64_cortex-a53) echo aarch64_cortex-a53 ;;
		*) echo "unsupported package target: $format $arch" >&2; exit 1 ;;
	esac
}

run_nfpm() {
	packager=$1
	target=$2
	config=$3
	if command -v nfpm >/dev/null 2>&1; then
		nfpm package --packager "$packager" --target "$target" --config "$config"
	else
		go run "github.com/goreleaser/nfpm/v2/cmd/nfpm@$NFPM_VERSION" package --packager "$packager" --target "$target" --config "$config"
	fi
}

build_binaries() {
	format=$1
	arch=$2
	goarch=$(go_arch_for "$arch")
	out="$BUILD_DIR/$format-$arch/root/usr/bin"

	mkdir -p "$out"
	CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build -trimpath -ldflags="-s -w" -o "$out/$BIN_DAEMON" ./cmd/networkmond
	CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" go build -trimpath -ldflags="-s -w" -o "$out/$BIN_CLIENT" ./cmd/networkmonc
}

write_deb_config() {
	arch=$1
	root=$2
	config=$3
	nfpm_arch=$(nfpm_arch_for deb "$arch")
	cat >"$config" <<EOF
name: $PKG_NAME
arch: $nfpm_arch
platform: linux
version: "$VERSION"
section: utils
priority: optional
maintainer: fujr
description: Local TCP/UDP network connection recorder with process ownership.
homepage: https://github.com/fujr/connection_recorder
license: MIT
contents:
  - src: $root/usr/bin/networkmond
    dst: /usr/bin/networkmond
    file_info:
      mode: 0755
  - src: $root/usr/bin/networkmonc
    dst: /usr/bin/networkmonc
    file_info:
      mode: 0755
  - src: $ROOT_DIR/packaging/systemd/networkmond.service
    dst: /lib/systemd/system/networkmond.service
    file_info:
      mode: 0644
scripts:
  postinstall: $ROOT_DIR/packaging/debian/postinst
  preremove: $ROOT_DIR/packaging/debian/prerm
  postremove: $ROOT_DIR/packaging/debian/postrm
EOF
}

write_openwrt_config() {
	arch=$1
	root=$2
	config=$3
	nfpm_arch=$(nfpm_arch_for openwrt "$arch")
	cat >"$config" <<EOF
name: $PKG_NAME
arch: $nfpm_arch
platform: linux
version: "$VERSION"
section: net
priority: optional
maintainer: fujr
description: Local TCP/UDP network connection recorder with process ownership.
homepage: https://github.com/fujr/connection_recorder
license: MIT
contents:
  - src: $root/usr/bin/networkmond
    dst: /usr/bin/networkmond
    file_info:
      mode: 0755
  - src: $root/usr/bin/networkmonc
    dst: /usr/bin/networkmonc
    file_info:
      mode: 0755
  - src: $ROOT_DIR/packaging/openwrt/networkmond.init
    dst: /etc/init.d/networkmond
    file_info:
      mode: 0755
  - src: $ROOT_DIR/packaging/openwrt/networkmond.config
    dst: /etc/config/networkmond
    type: config
    file_info:
      mode: 0644
scripts:
  postinstall: $ROOT_DIR/packaging/openwrt/postinstall
  preremove: $ROOT_DIR/packaging/openwrt/preremove
EOF
}

package_one() {
	format=$1
	arch=$2
	case "$format" in
		deb) ext=deb; packager=deb ;;
		openwrt) ext=apk; packager=apk ;;
		*) echo "unsupported format: $format" >&2; usage; exit 1 ;;
	esac

	need go
	mkdir -p "$DIST_DIR" "$BUILD_DIR"
	build_binaries "$format" "$arch"

	root="$BUILD_DIR/$format-$arch/root"
	config="$BUILD_DIR/$format-$arch/nfpm.yaml"
	mkdir -p "$(dirname "$config")"
	if [ "$format" = deb ]; then
		write_deb_config "$arch" "$root" "$config"
	else
		write_openwrt_config "$arch" "$root" "$config"
	fi

	target="$DIST_DIR/${PKG_NAME}_${VERSION}_${format}_${arch}.${ext}"
	run_nfpm "$packager" "$target" "$config"
}

package_all() {
	package_one deb amd64
	package_one deb arm64
	package_one openwrt x86_64
	package_one openwrt aarch64_generic
	package_one openwrt aarch64_cortex-a53
}

case "${1:-}" in
	deb|openwrt)
		if [ "$#" -ne 2 ]; then
			usage
			exit 1
		fi
		package_one "$1" "$2"
		;;
	all)
		package_all
		;;
	-h|--help|"")
		usage
		;;
	*)
		usage
		exit 1
		;;
esac
