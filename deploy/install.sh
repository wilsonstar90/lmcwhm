#!/usr/bin/env bash
# lmcwhm installer (Ubuntu 20.04 / 22.04 / 24.04).
#
#   curl -fsSL https://raw.githubusercontent.com/wilsonstar90/lmcwhm/main/deploy/install.sh | sudo bash
#
# Fresh server  -> installs lmcwhm and prints the panel address and credentials.
# Already installed -> repair / update mode: program files and the service are
#   rebuilt, while website files (/www/wwwroot), firewall rules and all panel
#   settings (/opt/lmcwhm/data) are kept. The data directory is backed up first
#   and everything is rolled back automatically if the new version fails to start.
#
# Options:
#   --version vX.Y.Z   install a specific release (default: latest)
#   --repair           reinstall the currently installed version
#   --reset-panel      lock-out recovery: new entry + admin password, self-signed
#                      SSL, 2FA / authorized IP / domain / BasicAuth off
#   --binary FILE      install a local build instead of downloading
#   --port N --entry /path --username U --password P
#                      fresh install: initial values; upgrade: applied as changes
#   --skip-docker      do not install Docker
#   --force            continue on an unsupported OS
set -euo pipefail

HOME_DIR=/opt/lmcwhm
DATA_DIR=$HOME_DIR/data
BACKUP_DIR=$HOME_DIR/backup
BIN=$HOME_DIR/bin/lmcwhm
UNIT=/etc/systemd/system/lmcwhm.service
REPO=wilsonstar90/lmcwhm
KEEP_BACKUPS=5

VERSION=latest
BINARY=""
REPAIR=0
RESET_PANEL=0
SKIP_DOCKER=0
FORCE=0
OPT_PORT="" OPT_ENTRY="" OPT_USER="" OPT_PASS=""

red()    { printf '\033[31m%s\033[0m\n' "$*"; }
green()  { printf '\033[32m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }
step()   { green ">> $*"; }
die()    { red "ERROR: $*"; exit 1; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)     VERSION="$2"; shift 2 ;;
    --repair)      REPAIR=1; shift ;;
    --reset-panel) RESET_PANEL=1; shift ;;
    --binary)      BINARY="$2"; shift 2 ;;
    --port)        OPT_PORT="$2"; shift 2 ;;
    --entry)       OPT_ENTRY="$2"; shift 2 ;;
    --username)    OPT_USER="$2"; shift 2 ;;
    --password)    OPT_PASS="$2"; shift 2 ;;
    --skip-docker) SKIP_DOCKER=1; shift ;;
    --force)       FORCE=1; shift ;;
    *) die "unknown option $1" ;;
  esac
done

[[ $EUID -eq 0 ]] || die "please run as root"
export LMCWHM_HOME=$HOME_DIR

# ---- OS / arch ----
# Read os-release in a subshell: sourcing it directly would overwrite our
# VERSION (it defines VERSION="24.04.4 LTS (Noble Numbat)").
os_field() { (. /etc/os-release; eval "printf '%s' \"\${$1:-}\"") ; }
OS_ID="$(os_field ID)"
OS_VERSION_ID="$(os_field VERSION_ID)"
OS_CODENAME="$(os_field VERSION_CODENAME)"
OS_PRETTY="$(os_field PRETTY_NAME)"
case "${OS_ID}-${OS_VERSION_ID}" in
  ubuntu-20.04) yellow "Note: Ubuntu 20.04 standard support ended in 2025-05 (ESM only). 22.04 / 24.04 is recommended." ;;
  ubuntu-22.04|ubuntu-24.04) ;;
  *) [[ $FORCE -eq 1 ]] || die "unsupported OS ${OS_PRETTY}; use --force to continue anyway" ;;
esac
case "$(uname -m)" in
  x86_64)  ARCH=amd64 ;;
  aarch64) ARCH=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac

# ---- mode ----
MODE=fresh
if [[ -f "$DATA_DIR/panel.db" ]]; then
  MODE=upgrade
fi
OLD_VERSION=""
if [[ -x "$BIN" ]]; then
  OLD_VERSION="$("$BIN" version 2>/dev/null | awk '{print $2}' || true)"
fi
if [[ $REPAIR -eq 1 ]]; then
  if [[ "$OLD_VERSION" == v* ]]; then VERSION="$OLD_VERSION"; else VERSION=latest; fi
fi

if [[ $MODE == upgrade ]]; then
  green "==================================================================="
  green " lmcwhm repair / update mode (installed: ${OLD_VERSION:-unknown}, target: $VERSION)"
  green " Kept: websites (/www/wwwroot), installed apps & databases, Docker containers,"
  green "       firewall rules, panel settings & data"
  if [[ $RESET_PANEL -eq 1 ]]; then yellow " Panel access will be reset (--reset-panel)"; fi
  green "==================================================================="
fi

# ---- base packages ----
step "Checking base packages"
export DEBIAN_FRONTEND=noninteractive
need_pkgs=()
for p in curl ca-certificates tar gnupg nftables; do dpkg -s "$p" >/dev/null 2>&1 || need_pkgs+=("$p"); done
if [[ ${#need_pkgs[@]} -gt 0 ]]; then
  apt-get update -qq
  apt-get install -y -qq "${need_pkgs[@]}" >/dev/null
fi

# ---- Docker: install when missing, repair when broken ----
install_docker() {
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  echo "deb [arch=${ARCH} signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu ${OS_CODENAME} stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -qq
  apt-get install -y -qq "$@" docker-ce docker-ce-cli containerd.io docker-compose-plugin >/dev/null
  systemctl enable --now docker
}
if [[ $SKIP_DOCKER -eq 0 ]]; then
  if ! command -v docker >/dev/null 2>&1; then
    step "Installing Docker Engine"
    install_docker
  elif ! docker info >/dev/null 2>&1; then
    step "Docker is not healthy, restarting"
    systemctl restart docker || true
    if ! docker info >/dev/null 2>&1; then
      step "Reinstalling Docker packages (containers, images and volumes are kept)"
      install_docker --reinstall
    fi
  fi
fi

# ---- fetch the new binary (before touching anything) ----
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
NEW_BIN="$WORK/lmcwhm"
if [[ -n "$BINARY" ]]; then
  step "Using local binary $BINARY"
  cp "$BINARY" "$NEW_BIN"
else
  if [[ "$VERSION" == latest ]]; then
    BASE="https://github.com/${REPO}/releases/latest/download"
  else
    BASE="https://github.com/${REPO}/releases/download/${VERSION}"
  fi
  URL="${LMCWHM_DOWNLOAD_URL:-$BASE/lmcwhm-linux-${ARCH}}"
  step "Downloading $URL"
  curl -fL --retry 3 --progress-bar "$URL" -o "$NEW_BIN" || die "download failed: $URL"
  if [[ -z "${LMCWHM_DOWNLOAD_URL:-}" ]] && curl -fsSL "$BASE/SHA256SUMS" -o "$WORK/SHA256SUMS" 2>/dev/null; then
    want="$(awk -v f="lmcwhm-linux-${ARCH}" '$2==f {print $1}' "$WORK/SHA256SUMS")"
    got="$(sha256sum "$NEW_BIN" | awk '{print $1}')"
    [[ -z "$want" || "$want" == "$got" ]] || die "checksum mismatch for lmcwhm-linux-${ARCH}"
  fi
fi
chmod 0755 "$NEW_BIN"
NEW_VERSION="$("$NEW_BIN" version 2>/dev/null | awk '{print $2}')"
[[ -n "$NEW_VERSION" ]] || die "downloaded binary does not run on this system"
step "New version: $NEW_VERSION"

# ---- back up the data directory (settings, users, certificates) ----
BACKUP_FILE=""
if [[ $MODE == upgrade ]]; then
  install -d -m 0700 "$BACKUP_DIR"
  BACKUP_FILE="$BACKUP_DIR/reinstall-$(date +%Y%m%d-%H%M%S).tar.gz"
  step "Backing up $DATA_DIR -> $BACKUP_FILE"
  # Stop first so the SQLite WAL is checkpointed and the snapshot is consistent.
  if systemctl is-active --quiet lmcwhm 2>/dev/null; then systemctl stop lmcwhm; fi
  tar -czf "$BACKUP_FILE" -C "$HOME_DIR" data
  # keep only the newest $KEEP_BACKUPS reinstall backups
  ls -1t "$BACKUP_DIR"/reinstall-*.tar.gz 2>/dev/null | tail -n +$((KEEP_BACKUPS + 1)) | xargs -r rm -f
fi

# ---- systemd unit (always rewritten: part of the program files) ----
write_unit() {
  cat > "$UNIT" <<'UNITEOF'
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
UNITEOF
  systemctl daemon-reload
}

# ---- install program files ----
step "Installing program files"
install -d -m 0700 "$HOME_DIR" "$HOME_DIR/bin"
if systemctl is-active --quiet lmcwhm 2>/dev/null; then systemctl stop lmcwhm; fi
if [[ -f "$BIN" ]]; then cp -p "$BIN" "$BIN.prev"; fi
install -m 0755 "$NEW_BIN" "$BIN"
ln -sf "$BIN" /usr/bin/lmcwhm
write_unit

rollback() {
  red "New version failed; rolling back"
  systemctl stop lmcwhm 2>/dev/null || true
  if [[ -f "$BIN.prev" ]]; then install -m 0755 "$BIN.prev" "$BIN"; fi
  if [[ -n "$BACKUP_FILE" && -f "$BACKUP_FILE" ]]; then
    rm -rf "$DATA_DIR.failed"
    mv "$DATA_DIR" "$DATA_DIR.failed"
    tar -xzf "$BACKUP_FILE" -C "$HOME_DIR"
  fi
  systemctl start lmcwhm || true
  journalctl -u lmcwhm -n 30 --no-pager || true
  die "rolled back to ${OLD_VERSION:-previous version}; failed data kept in $DATA_DIR.failed"
}

# ---- initialise or migrate ----
INFO=""
if [[ $MODE == fresh ]]; then
  step "Initialising panel"
  init_args=()
  if [[ -n "$OPT_PORT" ]];  then init_args+=(--port "$OPT_PORT"); fi
  if [[ -n "$OPT_ENTRY" ]]; then init_args+=(--entry "$OPT_ENTRY"); fi
  if [[ -n "$OPT_USER" ]];  then init_args+=(--username "$OPT_USER"); fi
  if [[ -n "$OPT_PASS" ]];  then init_args+=(--password "$OPT_PASS"); fi
  INFO="$("$BIN" init ${init_args[@]+"${init_args[@]}"})"
else
  step "Migrating database"
  # Any command that opens the database applies pending schema migrations.
  "$BIN" port >/dev/null || rollback
  if [[ $RESET_PANEL -eq 1 ]]; then
    reset_args=()
    if [[ -n "$OPT_PORT" ]];  then reset_args+=(--port "$OPT_PORT"); fi
    if [[ -n "$OPT_ENTRY" ]]; then reset_args+=(--entry "$OPT_ENTRY"); fi
    if [[ -n "$OPT_PASS" ]];  then reset_args+=(--password "$OPT_PASS"); fi
    INFO="$("$BIN" reset-panel ${reset_args[@]+"${reset_args[@]}"})" || rollback
  else
    if [[ -n "$OPT_PORT" ]];  then "$BIN" port "$OPT_PORT" || rollback; fi
    if [[ -n "$OPT_ENTRY" ]]; then "$BIN" entry "$OPT_ENTRY" || rollback; fi
    if [[ -n "$OPT_PASS" ]];  then "$BIN" passwd "$OPT_PASS" || rollback; fi
  fi
  if [[ -n "$OPT_USER" ]]; then "$BIN" username "$OPT_USER" || rollback; fi
fi
PANEL_PORT="$("$BIN" port)"

# ---- firewall: only ever add rules, never remove or reset ----
if command -v ufw >/dev/null 2>&1 && ufw status | grep -q "Status: active"; then
  step "Allowing ports ${PANEL_PORT}, 80, 443 in ufw (existing rules untouched)"
  ufw allow "${PANEL_PORT}/tcp" >/dev/null
  ufw allow 80/tcp >/dev/null
  ufw allow 443/tcp >/dev/null
fi

# ---- start and health check ----
step "Starting lmcwhm"
systemctl enable lmcwhm >/dev/null 2>&1
systemctl restart lmcwhm
healthy=0
for _ in $(seq 1 15); do
  sleep 1
  systemctl is-active --quiet lmcwhm || continue
  code="$(curl -sk -o /dev/null -w '%{http_code}' "https://127.0.0.1:${PANEL_PORT}/" 2>/dev/null || true)"
  if [[ -z "$code" || "$code" == 000 ]]; then
    code="$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${PANEL_PORT}/" 2>/dev/null || true)"
  fi
  if [[ -n "$code" && "$code" != 000 ]]; then healthy=1; break; fi
done
if [[ $healthy -ne 1 ]]; then
  if [[ $MODE == upgrade ]]; then rollback; fi
  red "lmcwhm failed to start:"
  journalctl -u lmcwhm -n 30 --no-pager || true
  exit 1
fi
rm -f "$BIN.prev"

# ---- summary ----
if [[ -n "$INFO" ]]; then
  echo "$INFO"
else
  "$BIN" default
fi
if [[ $MODE == upgrade ]]; then
  green "Done: ${OLD_VERSION:-unknown} -> ${NEW_VERSION}. Websites, firewall rules and panel settings were kept."
  green "Data backup: $BACKUP_FILE"
else
  green "Installed ${NEW_VERSION}. Run 'lmcwhm' for the management menu."
fi
