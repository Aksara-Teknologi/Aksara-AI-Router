#!/data/data/com.termux/files/usr/bin/sh
set -eu
umask 077

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "Usage: sh update-existing.sh /absolute/path/to/new-api [runit-service-name]" >&2
  exit 2
fi

if [ -z "${PREFIX:-}" ] || [ ! -d "$PREFIX/var/service" ]; then
  echo 'Run this updater inside Termux with termux-services installed.' >&2
  exit 1
fi

package_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
source_binary="$package_dir/aksara-ai-router-android-arm64"
target=$1
service=${2:-new-api}
service_dir="$PREFIX/var/service/$service"

test -f "$source_binary" || { echo "Missing package binary: $source_binary" >&2; exit 1; }
case "$target" in /*) ;; *) echo 'Use an absolute path for the existing binary.' >&2; exit 2 ;; esac
test -x "$target" || { echo "Target is not an executable file: $target" >&2; exit 1; }
test -d "$service_dir" || { echo "Runit service not found: $service_dir" >&2; exit 1; }
command -v sv >/dev/null || { echo 'Install termux-services first.' >&2; exit 1; }

backup="$target.backup.$(date +%Y%m%d-%H%M%S)"
sv down "$service"
cp -p "$target" "$backup"
cp "$source_binary" "$target.next"
chmod 700 "$target.next"
mv "$target.next" "$target"

if ! sv up "$service"; then
  echo 'Service failed to start; restoring the previous binary.' >&2
  cp -p "$backup" "$target"
  sv up "$service" || true
  exit 1
fi

echo "Updated $target"
echo "Previous binary backed up at $backup"
echo "Check service with: sv status $service"
