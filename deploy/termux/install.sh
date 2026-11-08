#!/data/data/com.termux/files/usr/bin/sh
set -eu
umask 077
package_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
binary=${1:-"$package_dir/aksara-ai-router-android-arm64"}
app_dir=${AKSARA_DATA_DIR:-"$HOME/.local/share/aksara-ai-router"}
if [ "$(uname -m)" != aarch64 ]; then
  echo 'This package requires an ARM64 Android device.' >&2
  exit 1
fi
if [ ! -x /system/bin/linker64 ] || [ "$(getprop ro.build.version.sdk)" -lt 26 ]; then
  echo 'This package requires ARM64 Android 8/API 26 or newer.' >&2
  exit 1
fi
if [ -z "${PREFIX:-}" ] || [ ! -d "$PREFIX/bin" ]; then
  echo 'Run this installer inside Termux.' >&2
  exit 1
fi
test -f "$binary"
command -v openssl >/dev/null || { echo 'Install dependencies: pkg install openssl-tool termux-services' >&2; exit 1; }
mkdir -p "$app_dir/bin" "$app_dir/logs"
if command -v sv >/dev/null && [ -d "$PREFIX/var/service/aksara-ai-router" ]; then
  sv down "$PREFIX/var/service/aksara-ai-router"
fi
cp "$binary" "$app_dir/bin/router.next"
chmod 700 "$app_dir/bin/router.next"
mv "$app_dir/bin/router.next" "$app_dir/bin/router"
if [ ! -f "$app_dir/.env" ]; then
  session_key=$(openssl rand -hex 32)
  encryption_key=$(openssl rand -base64 32)
  cat > "$app_dir/.env" <<EOF
PORT=3000
BIND_ADDRESS=127.0.0.1
SQLITE_PATH=$app_dir/new-api.db
SESSION_SECRET=$session_key
PLUGIN_OAUTH_ENCRYPTION_KEY=$encryption_key
AKSARA_PERSONAL_PROFILE=true
PLUGIN_OAUTH_LOOPBACK_CALLBACKS=true
PLUGIN_OAUTH_POOL_ENABLED=true
PLUGIN_OAUTH_MAX_CONCURRENCY=1
PLUGIN_OAUTH_AUTO_CACHE=true
MEMORY_CACHE_ENABLED=true
SYNC_FREQUENCY=60
TASK_PLUGIN_ENABLED=true
TASK_PLUGIN_PROTOCOL_TIMEOUT_SECONDS=600
GIN_MODE=release
GOMEMLIMIT=256MiB
GOGC=100
EOF
  chmod 600 "$app_dir/.env"
fi
cat > "$app_dir/start.sh" <<EOF
#!$PREFIX/bin/sh
set -eu
cd '$app_dir'
set -a
. './.env'
set +a
exec 2>&1
exec '$app_dir/bin/router'
EOF
chmod 700 "$app_dir/start.sh"
if command -v sv >/dev/null; then
  service_dir="$PREFIX/var/service/aksara-ai-router"
  mkdir -p "$service_dir/log"
  cp "$app_dir/start.sh" "$service_dir/run"
  chmod 700 "$service_dir/run"
  cat > "$service_dir/log/run" <<EOF
#!$PREFIX/bin/sh
exec svlogd -tt '$app_dir/logs'
EOF
  chmod 700 "$service_dir/log/run"
  printf 's1048576\nn5\n' > "$app_dir/logs/config"
  echo "Installed runit service: $service_dir"
  echo 'Open a new Termux session, then run: sv up aksara-ai-router'
else
  echo 'Install termux-services and run the installer again for background service management.'
fi
echo "Foreground start: $app_dir/start.sh"
echo 'Open http://localhost:3000 on this Android device and create your administrator account.'
echo 'Existing database and encryption keys are preserved on reinstall.'
