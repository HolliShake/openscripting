#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$ROOT_DIR"

HOST_OS=$(go env GOOS)
HOST_ARCH=$(go env GOARCH)
ALL_MODE=0
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
	printf 'Usage: %s [--all | --help | -h]\n' "$(basename "$0")"
	printf '\nBuild the current operating system and architecture into openscript.\n'
	printf 'Windows builds use the .exe extension and embed docs/openscript.ico.\n'
	printf '\nOptions:\n'
	printf '  --all      Build optimized Windows, Linux, macOS, BSD, and WebAssembly releases for supported architectures.\n'
	printf '             Packages go to dist/compressed; js/wasm includes Go runtime files for Node.js.\n'
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
			rm -f "$RESOURCE_FILE"
			RESOURCE_FILE=""
			return 1
		fi
		output+=.exe
	fi
	if [[ "$target_os" == js || "$target_os" == wasip1 ]]; then
		output+=.wasm
	fi

	mkdir -p "$(dirname "$output")"
	printf 'Building %s/%s to %s...\n' "$target_os" "$target_arch" "$output"
	if [[ "$ALL_MODE" -eq 1 ]]; then
		if ! CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build \
			-trimpath -ldflags='-s -w' -o "$output" ./main; then
			FAILED=1
			rm -f "$RESOURCE_FILE"
			RESOURCE_FILE=""
			return 1
		fi
		rm -f "$RESOURCE_FILE"
		RESOURCE_FILE=""
		return 0
	fi

	if ! GOOS="$target_os" GOARCH="$target_arch" go build -gcflags="-m=2" -o "$output" ./main; then
		FAILED=1
		return 1
	fi
}

package_target() {
	local target_os=$1
	local target_arch=$2
	local stage archive output
	stage=$(mktemp -d "${TMPDIR:-/tmp}/openscript-${target_os}-${target_arch}.XXXXXX")
	output="$stage/openscript"
	if [[ "$target_os" == windows ]]; then
		archive="$ROOT_DIR/dist/compressed/openscript-${target_os}-${target_arch}.zip"
	else
		archive="$ROOT_DIR/dist/compressed/openscript-${target_os}-${target_arch}.tar.gz"
	fi

	if ! build_target "$target_os" "$target_arch" "$output"; then
		rm -rf "$stage"
		return 1
	fi
	cp -R lib "$stage/lib"
	if [[ "$target_os" == js ]]; then
		local wasm_runtime="$(go env GOROOT)/lib/wasm"
		cp "$wasm_runtime/wasm_exec.js" "$stage/wasm_exec.js"
		cp "$wasm_runtime/wasm_exec_node.js" "$stage/wasm_exec_node.js"
	fi
	if [[ "$target_os" == windows ]]; then
		if ! command -v zip >/dev/null 2>&1; then
			printf 'zip is required to package Windows targets.\n' >&2
			FAILED=1
			rm -rf "$stage"
			return 1
		fi
		if ! (cd "$stage" && zip -qr "$archive" .); then
			FAILED=1
			rm -rf "$stage"
			return 1
		fi
	else
		if ! tar -czf "$archive" -C "$stage" .; then
			FAILED=1
			rm -rf "$stage"
			return 1
		fi
	fi
	rm -rf "$stage"
	(
		cd "$ROOT_DIR/dist/compressed"
		if command -v sha256sum >/dev/null 2>&1; then
			sha256sum "$(basename "$archive")" > "$(basename "$archive").sha256"
		else
			shasum -a 256 "$(basename "$archive")" > "$(basename "$archive").sha256"
		fi
	)
	printf 'Packaged %s/%s to %s.\n' "$target_os" "$target_arch" "${archive#"$ROOT_DIR/"}"
}

target_supported() {
	local target_os=$1
	local target_arch=$2
	case "$target_os" in
		js|wasip1)
			[[ "$target_arch" == wasm ]]
			;;
		windows|linux|darwin|dragonfly|freebsd|netbsd|openbsd)
			case "$target_arch" in
				amd64|arm64|386|arm|riscv64) return 0 ;;
				*) return 1 ;;
			esac
			;;
		*) return 1 ;;
	esac
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
		mkdir -p dist/compressed
		while IFS=/ read -r target_os target_arch; do
			if target_supported "$target_os" "$target_arch"; then
				package_target "$target_os" "$target_arch" || true
			fi
		done < <(go tool dist list)
		if (( FAILED != 0 )); then
			exit 1
		fi
		;;
	'')
		rm -f build.txt
		build_target "$HOST_OS" "$HOST_ARCH" openscript 2>&1 | tee -a build.txt
		;;
	*)
		usage >&2
		exit 2
		;;
esac