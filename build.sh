#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$ROOT_DIR"

HOST_OS=$(go env GOOS)
HOST_ARCH=$(go env GOARCH)
ALL_MODE=0
RELEASE_MODE=0
FAILED=0
RESOURCE_FILE=""

cleanup() {
	if [[ -n "$RESOURCE_FILE" ]]; then
		rm -f "$RESOURCE_FILE"
	fi
	rm -f build.tmp
}
trap cleanup EXIT

usage() {
	printf 'Usage: %s [--all | --release | --help | -h]\n' "$(basename "$0")"
	printf '\nBuild the current operating system and architecture into bin/openscript.\n'
	printf 'Windows builds use the .exe extension and embed docs/openscript.ico.\n'
	printf '\nOptions:\n'
	printf '  --all      Build Windows, Linux, and macOS binaries for the current Go architecture.\n'
	printf '             Cross-builds disable cgo and write artifacts to bin/.\n'
	printf '  --release  Build an optimized current-platform binary into dist/ and copy lib/ beside it.\n'
	printf '  --help, -h, /? Show this help and exit.\n'
}

build_target() {
	local target_os=$1
	local target_arch=$2
	local output=$3
	local target_found=0
	local resource="main/rsrc_windows_${target_arch}.syso"

	if go tool dist list | grep -Fxq "${target_os}/${target_arch}"; then
		target_found=1
	fi
	if [[ "$target_found" -eq 0 ]]; then
		printf 'Skipping unsupported target %s/%s.\n' "$target_os" "$target_arch"
		return 0
	fi

	if [[ "$target_os" == windows ]]; then
		RESOURCE_FILE=$resource
		rm -f "$RESOURCE_FILE"
		printf 'Embedding Windows icon for %s/%s...\n' "$target_os" "$target_arch"
		if ! GOOS="$HOST_OS" GOARCH="$HOST_ARCH" go run github.com/akavel/rsrc@v0.10.2 \
			-ico docs/openscript.ico -o "$RESOURCE_FILE" -arch "$target_arch"; then
			FAILED=1
			return 1
		fi
		output+=.exe
	fi

	mkdir -p "$(dirname "$output")"
	printf 'Building %s/%s to %s...\n' "$target_os" "$target_arch" "$output"
	if [[ "$RELEASE_MODE" -eq 1 ]]; then
		if ! GOOS="$target_os" GOARCH="$target_arch" go build \
			-trimpath -ldflags='-s -w' -o "$output" ./main; then
			FAILED=1
			return 1
		fi
		return 0
	fi

	if [[ "$ALL_MODE" -eq 1 ]]; then
		if ! GOOS="$target_os" GOARCH="$target_arch" CGO_ENABLED=0 \
			go build -gcflags='-m' -o "$output" ./main > build.tmp 2>&1; then
			cat build.tmp
			FAILED=1
			rm -f build.tmp
			return 1
		fi
	else
		if ! GOOS="$target_os" GOARCH="$target_arch" \
			go build -gcflags='-m' -o "$output" ./main > build.tmp 2>&1; then
			cat build.tmp
			FAILED=1
			rm -f build.tmp
			return 1
		fi
	fi

	{
		printf '[ %s/%s ]\n' "$target_os" "$target_arch"
		grep -E 'escapes to heap|moved to heap|leaking param' build.tmp || true
	} >> build.txt
	rm -f build.tmp
}

case "${1:-}" in
	--help|-h|/?|-\?)
		usage
		exit 0
		;;
	--all)
		if [[ $# -ne 1 ]]; then
			usage >&2
			exit 2
		fi
		ALL_MODE=1
		mkdir -p bin
		: > build.txt
		for target_os in windows linux darwin; do
			build_target "$target_os" "$HOST_ARCH" "bin/openscript-${target_os}-${HOST_ARCH}" || true
		done
		(( FAILED == 0 ))
		;;
	--release)
		if [[ $# -ne 1 ]]; then
			usage >&2
			exit 2
		fi
		RELEASE_MODE=1
		mkdir -p dist
		build_target "$HOST_OS" "$HOST_ARCH" dist/openscript
		cp -R lib dist/
		;;
	'')
		mkdir -p bin
		build_target "$HOST_OS" "$HOST_ARCH" bin/openscript
		;;
	*)
		usage >&2
		exit 2
		;;
esac