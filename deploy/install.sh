#!/usr/bin/env bash
# lmcwhm one-click installer (Ubuntu 20.04 / 22.04 / 24.04).
#
#   curl -fsSL https://raw.githubusercontent.com/wilsonstar90/lmcwhm/main/deploy/install.sh | sudo bash
#
# By default the latest GitHub release is installed. Alternatives:
#   bash install.sh --binary ./lmcwhm-linux-amd64          # a local build
#   bash install.sh --version v0.1.0                       # a specific release
#
# Options: --port N --entry /path --username U --password P --skip-docker --force
set -euo pipefail

HOME_DIR=/opt/lmcwhm
REPO=wilsonstar90/lmcwhm
VERSION=latest
BINARY=""
SKIP_DOCKER=0
FORCE=0
INIT_ARGS=()

red()   { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
die()   { red "ERROR: $*"; exit 1; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --binary)      BINARY="$2"; shift 2 ;;
    --version)     VERSION="$2"; shift 2 ;;
    --port|--entry|--username|--password) INIT_ARGS+=("$1" "$2"); shift 2 ;;
    --skip-docker) SKIP_DOCKER=1; shift ;;
    --force)       FORCE=1; shift ;;
    *) die "unknown option $1" ;;
  esac
done

[[ $EUID -eq 0 ]] || die "please run as root"

# ---- OS check ----
. /etc/os-release
case "${ID}-${VERSION_ID}" in
  ubuntu-20.04)
    red "Note: Ubuntu 20.04 standard support ended in 2025-05 (ESM only). 22.04 / 24.04 is recommended." ;;
  ubuntu-22.04|ubuntu-24.04) ;;
  *) [[ $FORCE -eq 1 ]] || die "unsupported OS ${PRETTY_NAME}; use --force to continue anyway" ;;
esac

case "$(uname -m)" in
  x86_64)  ARCH=amd64 ;;
  aarch64) ARCH=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac

if systemctl is-active --quiet lmcwhm 2>/dev/null && [[ $FORCE -ne 1 ]]; then
  die "lmcwhm is already installed and running (use 'lmcwhm update' or --force)"
fi

green ">> Installing base packages"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl ca-certificates tar gnupg lsb-release >/dev/null

# ---- Docker (official repository) ----
if [[ $SKIP_DOCKER -eq 0 ]] && ! command -v docker >/dev/null 2>&1; then
  green ">> Installing Docker Engine"
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  echo "deb [arch=${ARCH} signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu ${VERSION_CODENAME} stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -qq
  apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-compose-plugin >/dev/null
  systemctl enable --now docker
fi

# ---- lmcwhm binary ----
green ">> Installing lmcwhm"
install -d -m 0700 "$HOME_DIR" "$HOME_DIR/bin"
TMP_BIN="$(mktemp)"
if [[ -n "$BINARY" ]]; then
  cp "$BINARY" "$TMP_BIN"
else
  if [[ "$VERSION" == latest ]]; then
    BASE="https://github.com/${REPO}/releases/latest/download"
  else
    BASE="https://github.com/${REPO}/releases/download/${VERSION}"
  fi
  URL="${LMCWHM_DOWNLOAD_URL:-$BASE/lmcwhm-linux-${ARCH}}"
  echo "   downloading $URL"
  curl -fL --retry 3 --progress-bar "$URL" -o "$TMP_BIN" || die "download failed: $URL"
  # Verify against the release checksum file when it is published.
  if [[ -z "${LMCWHM_DOWNLOAD_URL:-}" ]] && curl -fsSL "$BASE/SHA256SUMS" -o "$TMP_BIN.sums" 2>/dev/null; then
    want="$(awk -v f="lmcwhm-linux-${ARCH}" '$2==f {print $1}' "$TMP_BIN.sums")"
    got="$(sha256sum "$TMP_BIN" | awk '{print $1}')"
    rm -f "$TMP_BIN.sums"
    [[ -z "$want" || "$want" == "$got" ]] || die "checksum mismatch for lmcwhm-linux-${ARCH}"
  fi
fi
install -m 0755 "$TMP_BIN" "$HOME_DIR/bin/lmcwhm"
rm -f "$TMP_BIN"
ln -sf "$HOME_DIR/bin/lmcwhm" /usr/bin/lmcwhm
"$HOME_DIR/bin/lmcwhm" version

# ---- systemd ----
# Under `curl | bash` there is no script file, so fall back to the inline unit.
SCRIPT_DIR=""
[[ -n "${BASH_SOURCE[0]:-}" && -f "${BASH_SOURCE[0]}" ]] && SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ -n "$SCRIPT_DIR" && -f "$SCRIPT_DIR/lmcwhm.service" ]]; then
  install -m 0644 "$SCRIPT_DIR/lmcwhm.service" /etc/systemd/system/lmcwhm.service
else
  cat > /etc/systemd/system/lmcwhm.service <<'UNIT'
[Unit]
Description=lmcwhm server control panel
After=network-online.target docker.service
Wants=network-online.target

[Service]
Type=simple
Environment=LMCWHM_HOME=/opt/lmcwhm
ExecStart=/opt/lmcwhm/bin/lmcwhm serve
ExecReload=/bin/kill -HUP $MAINPID
Restart=always
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
UNIT
fi
systemctl daemon-reload

# ---- initialise (prints address, entry, username and password) ----
INFO="$(LMCWHM_HOME=$HOME_DIR "$HOME_DIR/bin/lmcwhm" init "${INIT_ARGS[@]}")"

# Open the panel port (and web ports for the edge proxy) if ufw is active.
PANEL_PORT="$(LMCWHM_HOME=$HOME_DIR "$HOME_DIR/bin/lmcwhm" port)"
if command -v ufw >/dev/null 2>&1 && ufw status | grep -q "Status: active"; then
  [[ -n "$PANEL_PORT" ]] && ufw allow "${PANEL_PORT}/tcp" >/dev/null
  ufw allow 80/tcp >/dev/null
  ufw allow 443/tcp >/dev/null
fi

systemctl enable --now lmcwhm
sleep 1
systemctl is-active --quiet lmcwhm || { red "lmcwhm failed to start:"; journalctl -u lmcwhm -n 30 --no-pager; exit 1; }

echo "$INFO"
green "Installed. Run 'lmcwhm' for the management menu."
