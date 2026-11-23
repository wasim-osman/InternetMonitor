#!/bin/sh
# Install (or upgrade) the InternetMonitor agent (imonitord) on Linux or macOS.
#
#   curl -fsSL https://raw.githubusercontent.com/wasim-osman/InternetMonitor/main/scripts/install-agent.sh | sh
#
# Picks the release asset for this OS/CPU, verifies it against SHA256SUMS and
# runs `sudo ./imonitord install` (systemd service on Linux, LaunchDaemon on
# macOS). Re-running upgrades in place and keeps history and pairings.
#
# Environment:
#   IMONITOR_LISTEN       listen address for the service, e.g. ":8080" (default :7878 / keep installed)
#   IMONITOR_RELEASE_URL  base URL of the release assets
#                         (default https://github.com/wasim-osman/InternetMonitor/releases/latest/download)
set -eu

BASE_URL="${IMONITOR_RELEASE_URL:-https://github.com/wasim-osman/InternetMonitor/releases/latest/download}"
LISTEN="${IMONITOR_LISTEN:-}"

say() { printf '%s\n' "$*"; }
die() { printf 'install-agent: %s\n' "$*" >&2; exit 1; }

os=$(uname -s)
arch=$(uname -m)
case "$os" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) die "unsupported OS '$os' (Linux and macOS only; on Windows use scripts/install-agent.ps1)" ;;
esac
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  i386 | i486 | i586 | i686) arch=386 ;;
  aarch64 | arm64) arch=arm64 ;;
  armv7* | armv8l | armhf) arch=arm ;;
  armv6* | armv5*) die "ARMv6 and older (Pi 1 / Pi Zero W) are not supported; an ARMv7+ CPU is required" ;;
  *) die "unsupported CPU architecture '$arch'" ;;
esac
# A shell running under Rosetta reports x86_64 on Apple silicon: use the native build.
if [ "$os" = darwin ] && [ "$arch" = amd64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
  arch=arm64
fi
asset="imonitord-$os-$arch"

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --retry 3 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
else
  die "curl or wget is required"
fi

if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
elif command -v openssl >/dev/null 2>&1; then
  sha256() { openssl dgst -sha256 "$1" | awk '{print $NF}'; }
else
  die "sha256sum, shasum or openssl is required to verify the download"
fi

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t imonitord)
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading $asset from $BASE_URL ..."
fetch "$BASE_URL/$asset" "$tmp/imonitord" || die "download of $BASE_URL/$asset failed"
fetch "$BASE_URL/SHA256SUMS" "$tmp/SHA256SUMS" || die "download of $BASE_URL/SHA256SUMS failed"

want=$(awk -v n="$asset" '{f=$2; sub(/^\*/, "", f); if (f == n) {print $1; exit}}' "$tmp/SHA256SUMS")
[ -n "$want" ] || die "$asset is not listed in SHA256SUMS"
got=$(sha256 "$tmp/imonitord")
[ "$got" = "$want" ] || die "checksum mismatch for $asset (expected $want, got $got)"
say "Checksum OK ($got)"
chmod 755 "$tmp/imonitord"

set -- install
[ -n "$LISTEN" ] && set -- install --listen "$LISTEN"
if [ "$(id -u)" -eq 0 ]; then
  "$tmp/imonitord" "$@"
else
  command -v sudo >/dev/null 2>&1 || die "sudo is required (or run this script as root)"
  say "Running: sudo imonitord $* (you may be asked for your password)"
  sudo "$tmp/imonitord" "$@"
fi

bin=/usr/local/bin/imonitord
if [ -x "$bin" ]; then
  say ""
  i=0
  while [ $i -lt 10 ]; do
    if "$bin" pair-code 2>/dev/null; then
      say "Enter this code in the InternetMonitor app to pair (or run: imonitord pair-code)."
      exit 0
    fi
    i=$((i + 1))
    sleep 1
  done
  say "The agent was installed. Get a pairing code with: $bin pair-code"
fi
