#!/bin/sh
set -eu
repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"
if [ ! -f web/dist/index.html ]; then
  echo 'Build the frontend first: cd web && bun install --frozen-lockfile && bun run build' >&2
  exit 1
fi
if [ -z "${ANDROID_NDK_HOME:-}" ]; then
  echo 'Set ANDROID_NDK_HOME to an installed Android NDK directory.' >&2
  exit 1
fi
case $(uname -s) in
  Darwin) toolchain=darwin-x86_64 ;;
  Linux) toolchain=linux-x86_64 ;;
  *) echo 'Cross build requires a Linux or macOS host.' >&2; exit 1 ;;
esac
compiler="$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/$toolchain/bin/aarch64-linux-android26-clang"
test -x "$compiler"
mkdir -p dist
build_version="v0.1.0-aksara.$(git rev-parse --short HEAD)"
GOOS=android GOARCH=arm64 CGO_ENABLED=1 CC="$compiler" \
  go build -buildmode=pie -trimpath -ldflags="-s -w -X github.com/QuantumNous/new-api/common.Version=$build_version" -o dist/aksara-ai-router-android-arm64 .
printf 'Built %s/dist/aksara-ai-router-android-arm64\n' "$repo_dir"
