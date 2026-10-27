#!/usr/bin/env bash
# End-to-end test of deploy/install.sh on a disposable Ubuntu host (CI runner).
# Usage: sudo bash deploy/test-installer.sh bin/lmcwhm-linux-amd64
set -euo pipefail

NEW_BIN="$(realpath "$1")"
INSTALL="$(dirname "$(realpath "$0")")/install.sh"
JAR="$(mktemp)"
pass() { printf '\033[32mPASS\033[0m %s\n' "$*"; }
fail() { printf '\033[31mFAIL\033[0m %s\n' "$*"; journalctl -u lmcwhm -n 40 --no-pager || true; exit 1; }

port()  { lmcwhm port; }
code()  { curl -sk -o /dev/null -w '%{http_code}' -b "$JAR" -c "$JAR" "https://127.0.0.1:$(port)$1"; }
login() {
  rm -f "$JAR"
  [[ "$(code "$1")" == 302 ]] || return 1
  curl -sk -b "$JAR" -c "$JAR" -X POST "https://127.0.0.1:$(port)/api/v1/auth/login" \
    -d "{\"username\":\"$2\",\"password\":\"$3\"}" | grep -q '"csrf"'
}

# ---------- 1. fresh install ----------
ufw allow 22/tcp >/dev/null && ufw --force enable >/dev/null
bash "$INSTALL" --binary "$NEW_BIN" --skip-docker --port 18888 --entry /console --username admin --password Passw0rd1
systemctl is-active --quiet lmcwhm || fail "service not active after fresh install"
[[ "$(rm -f "$JAR"; code /)" == 404 ]] || fail "panel reachable without security entry"
login /console admin Passw0rd1 || fail "fresh login"
ufw status | grep -q 18888 || fail "panel port not allowed in ufw"
pass "fresh install"

# state that must survive repair / update
lmcwhm entry /keepme >/dev/null
mkdir -p /www/wwwroot/example.com && echo "hello site" > /www/wwwroot/example.com/index.html
ufw allow 2222/tcp >/dev/null

# ---------- 2. repair / update keeps everything ----------
bash "$INSTALL" --binary "$NEW_BIN" --skip-docker
systemctl is-active --quiet lmcwhm || fail "service not active after upgrade"
login /keepme admin Passw0rd1 || fail "settings or password lost on upgrade"
[[ "$(cat /www/wwwroot/example.com/index.html)" == "hello site" ]] || fail "website files changed"
ufw status | grep -q 2222 || fail "custom firewall rule removed"
ls /opt/lmcwhm/backup/reinstall-*.tar.gz >/dev/null || fail "no data backup"
[[ "$(port)" == 18888 ]] || fail "port changed"
pass "upgrade keeps settings, sites, firewall"

# ---------- 3. reset-panel ----------
bash "$INSTALL" --binary "$NEW_BIN" --skip-docker --reset-panel --entry /rescue1 --password NewPass123
login /rescue1 admin NewPass123 || fail "reset-panel login"
[[ "$(rm -f "$JAR"; code /keepme)" == 404 ]] || fail "old entry still works after reset"
[[ "$(cat /www/wwwroot/example.com/index.html)" == "hello site" ]] || fail "website files changed by reset"
[[ "$(port)" == 18888 ]] || fail "reset-panel changed the port"
pass "reset-panel"

# ---------- 4. broken release rolls back ----------
BROKEN="$(mktemp)"
cat > "$BROKEN" <<'EOF'
#!/bin/sh
[ "$1" = version ] && { echo "lmcwhm v9.9.9-broken"; exit 0; }
exit 1
EOF
chmod +x "$BROKEN"
if bash "$INSTALL" --binary "$BROKEN" --skip-docker; then fail "broken install reported success"; fi
systemctl is-active --quiet lmcwhm || fail "service not restored after rollback"
lmcwhm version | grep -qv broken || fail "broken binary left installed"
login /rescue1 admin NewPass123 || fail "data not restored after rollback"
pass "rollback"

# ---------- 5. lmcwhm update / repair CLI wiring ----------
lmcwhm update --help | grep -q -- --version || fail "update command"
lmcwhm repair --help | grep -q -- --reset-panel || fail "repair command"
pass "cli commands"

# ---------- 6. real downloads from GitHub Releases ----------
# Regression: /etc/os-release used to overwrite VERSION and break the URL.
bash "$INSTALL" --skip-docker --version v0.1.1
lmcwhm version | grep -q 'v0.1.1' || fail "pinned release not installed"
login /rescue1 admin NewPass123 || fail "settings lost on pinned download"
bash "$INSTALL" --skip-docker
lmcwhm version | grep -q ' v' || fail "latest release not installed"
login /rescue1 admin NewPass123 || fail "settings lost on latest download"
pass "release downloads (pinned + latest)"
