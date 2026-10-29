#!/usr/bin/env bash
# End-to-end test of website hosting on a disposable Ubuntu host with Docker.
# Expects lmcwhm installed with: --port 18888 --entry /console --username admin --password Passw0rd1
# Usage: sudo bash deploy/test-sites.sh
set -euo pipefail

P=https://127.0.0.1:18888
JAR="$(mktemp)"
CSRF=""
pass() { printf '\033[32mPASS\033[0m %s\n' "$*"; }
fail() {
  printf '\033[31mFAIL\033[0m %s\n' "$*"
  journalctl -u lmcwhm -n 60 --no-pager || true
  docker logs --tail 40 lmcwhm-haproxy 2>&1 || true
  docker logs --tail 40 lmcwhm-nginx 2>&1 || true
  exit 1
}

api() { # method path [json]
  curl -sk -b "$JAR" -c "$JAR" -X "$1" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
    ${3:+-d "$3"} "$P/api/v1$2"
}
jget() { python3 -c "import sys,json; d=json.load(sys.stdin); print(eval('d'+sys.argv[1]))" "$1"; }

wait_task() { # task_id
  for _ in $(seq 1 600); do
    t="$(api GET "/tasks/$1")"
    st="$(jget "['status']" <<<"$t")"
    if [[ "$st" != running ]]; then
      python3 -c "import sys,json; print('\n'.join(json.load(sys.stdin)['log']))" <<<"$t" | sed 's/^/    /'
      [[ "$st" == success ]] || fail "task $1 failed"
      return
    fi
    sleep 1
  done
  fail "task $1 timed out"
}
site_id() { api GET /sites | python3 -c "import sys,json; print([s['id'] for s in json.load(sys.stdin)['items'] if s['domain']==sys.argv[1]][0])" "$1"; }
get80()  { curl -s  --resolve "$1:80:127.0.0.1"  "http://$1${2:-/}"; }
get443() { curl -sk --resolve "$1:443:127.0.0.1" "https://$1${2:-/}"; }
issuer() { openssl s_client -connect 127.0.0.1:443 -servername "$1" </dev/null 2>/dev/null | openssl x509 -noout -issuer -subject; }

# ---------- login ----------
curl -sk -c "$JAR" -o /dev/null "$P/console"
CSRF="$(curl -sk -b "$JAR" -c "$JAR" -X POST "$P/api/v1/auth/login" -d '{"username":"admin","password":"Passw0rd1"}' | jget "['csrf']")"
[[ -n "$CSRF" ]] || fail "login"

# ---------- install web stack ----------
wait_task "$(api POST /web/install | jget "['task_id']")"
docker exec lmcwhm-haproxy haproxy -c -f /opt/lmcwhm/edge/haproxy.cfg 2>&1 | grep -i "warning\|alert" && fail "haproxy config has warnings"
[[ "$(docker inspect -f '{{.State.Status}}' lmcwhm-haproxy)" == running ]] || fail "haproxy not running"
[[ "$(docker inspect -f '{{.State.Status}}' lmcwhm-nginx)" == running ]] || fail "nginx not running"
pass "web stack installed"

# ---------- PHP site ----------
wait_task "$(api POST /sites '{"domain":"php.test","aliases":["www.php.test"],"type":"php","php_version":"8.3","ssl":"none"}' | jget "['task_id']")"
cat > /www/wwwroot/php.test/index.php <<'EOF'
<?php echo "php-ok-" . PHP_MAJOR_VERSION . "." . PHP_MINOR_VERSION . " https=" . ($_SERVER['HTTPS'] ?? 'off') . " ip=" . $_SERVER['REMOTE_ADDR'];
EOF
out="$(get80 php.test /index.php)"; echo "    $out"
[[ "$out" == php-ok-8.3* ]] || fail "php over http: $out"
[[ "$(get80 www.php.test /index.php)" == php-ok-8.3* ]] || fail "alias"
out6="$(curl -s -g --resolve 'php.test:80:[::1]' http://php.test/index.php)"
[[ "$out6" == php-ok-8.3* ]] || fail "php over IPv6: $out6"
out="$(get443 php.test /index.php)"; echo "    $out"
[[ "$out" == *"https=on"* ]] || fail "php over https (default cert): $out"
[[ "$out" == *"ip=127.0.0.1"* ]] || fail "real client IP not passed through: $out"
pass "PHP 8.3 site over HTTP, HTTPS, IPv4 and IPv6"

# ---------- visitor address behind a CDN ----------
# 127.0.0.1 (curl) plays the CDN: only its header is believed, Cloudflare's is not.
api PUT /firewall/cdn '{"ranges":["127.0.0.1"],"header":"X-Real-IP"}' | grep -q '"header":"X-Real-IP"' || fail "save CDN settings"
docker exec lmcwhm-haproxy haproxy -c -f /opt/lmcwhm/edge/haproxy.cfg 2>&1 | grep -i "warning\|alert" && fail "haproxy config has warnings (CDN)"
out="$(curl -s -H 'X-Real-IP: 203.0.113.9' --resolve php.test:80:127.0.0.1 http://php.test/index.php)"
[[ "$out" == *"ip=203.0.113.9"* ]] || fail "CDN header not used: $out"
out="$(curl -sk -H 'X-Real-IP: 203.0.113.9' --resolve php.test:443:127.0.0.1 https://php.test/index.php)"
[[ "$out" == *"ip=203.0.113.9"* ]] || fail "CDN header not used over https: $out"
out="$(curl -s -H 'CF-Connecting-IP: 198.51.100.1' --resolve php.test:80:127.0.0.1 http://php.test/index.php)"
[[ "$out" == *"ip=127.0.0.1"* ]] || fail "CF-Connecting-IP believed from a non-Cloudflare address: $out"
if command -v nft >/dev/null; then
  api PUT /firewall '{"enabled":true,"policy":"accept","ping":true}' | grep -q '"ok":true' || fail "enable firewall"
  api POST /firewall/ips '{"ip":"203.0.113.9","list":"block"}' >/dev/null
  sleep 4 # the edge lists follow within a few seconds, without a reload
  code="$(curl -s -o /dev/null -w '%{http_code}' -H 'X-Real-IP: 203.0.113.9' --resolve php.test:80:127.0.0.1 http://php.test/index.php)"
  [[ "$code" == 403 ]] || fail "blocked visitor behind the CDN got $code"
  [[ "$(get80 php.test /index.php)" == php-ok-8.3* ]] || fail "unblocked visitor refused"
  id="$(api GET '/firewall/ips?list=block' | jget "['items'][0]['id']")"
  api POST "/firewall/ips/$id/delete" >/dev/null
  sleep 4
  code="$(curl -s -o /dev/null -w '%{http_code}' -H 'X-Real-IP: 203.0.113.9' --resolve php.test:80:127.0.0.1 http://php.test/index.php)"
  [[ "$code" == 200 ]] || fail "unblocked visitor still refused ($code)"
  api PUT /firewall '{"enabled":false,"policy":"accept","ping":true}' >/dev/null
  pass "blocked by the real visitor address at the edge"
fi
api PUT /firewall/cdn '{"ranges":[],"header":""}' >/dev/null
pass "visitor address from a trusted CDN header only"

# ---------- User-Agent / URL black and white lists ----------
api POST /firewall/patterns '{"kind":"ua","list":"block","match":"contains","value":"sqlmap","enabled":true}' | grep -q '"id"' || fail "add UA block"
api POST /firewall/patterns/import '{"kind":"url","list":"block","match":"prefix","values":["/blocked","#comment",""]}' | grep -q '"added":1' || fail "import URL block"
api POST /firewall/patterns '{"kind":"url","list":"allow","match":"prefix","value":"/blocked/hook","enabled":true}' >/dev/null
docker exec lmcwhm-haproxy haproxy -c -f /opt/lmcwhm/edge/haproxy.cfg 2>&1 | grep -i "warning\|alert" && fail "haproxy config has warnings (lists)"
code() { curl -s -o /dev/null -w '%{http_code}' "$@"; }
[[ "$(code -A 'sqlmap/1.7' --resolve php.test:80:127.0.0.1 http://php.test/index.php)" == 403 ]] || fail "UA black list"
[[ "$(code -kA 'SQLMap/1.7' --resolve php.test:443:127.0.0.1 https://php.test/index.php)" == 403 ]] || fail "UA black list (https, any case)"
[[ "$(code --resolve php.test:80:127.0.0.1 http://php.test/blocked/x)" == 403 ]] || fail "URL black list"
[[ "$(code --resolve php.test:80:127.0.0.1 http://php.test/blocked/hook)" != 403 ]] || fail "URL white list does not win over the black list"
[[ "$(get80 php.test /index.php)" == php-ok-8.3* ]] || fail "ordinary request refused by the lists"
api POST /firewall/patterns/clear '{"kind":"ua","list":"block"}' | grep -q '"removed":1' || fail "clear UA list"
[[ "$(code -A 'sqlmap/1.7' --resolve php.test:80:127.0.0.1 http://php.test/index.php)" == 200 ]] || fail "cleared UA list still enforced"
api POST /firewall/patterns/clear '{"kind":"url","list":"block"}' >/dev/null
api POST /firewall/patterns/clear '{"kind":"url","list":"allow"}' >/dev/null
pass "User-Agent and URL black / white lists at the edge"

# ---------- static site + custom certificate (SNI selection) ----------
wait_task "$(api POST /sites '{"domain":"static.test","type":"static","ssl":"none"}' | jget "['task_id']")"
[[ "$(get80 static.test)" == *"Site created by lmcwhm"* ]] || fail "static default page"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 30 -subj "/CN=static.test" \
  -addext "subjectAltName=DNS:static.test" -keyout /tmp/s.key -out /tmp/s.crt 2>/dev/null
SID="$(site_id static.test)"
body="$(python3 -c "import json; print(json.dumps({'cert':open('/tmp/s.crt').read(),'key':open('/tmp/s.key').read()}))")"
[[ "$(api POST "/sites/$SID/ssl/upload" "$body" | jget "['ok']")" == True ]] || fail "upload cert"
issuer static.test | grep -q "CN *= *static.test" || fail "SNI static.test did not get its certificate"
issuer php.test | grep -q "lmcwhm" || fail "SNI php.test should get the default certificate"
pass "SNI picks the per-site certificate"

wait_task "$(api PATCH "/sites/$SID" '{"force_https":true}' | jget "['task_id']")"
loc="$(curl -s -o /dev/null -w '%{http_code} %{redirect_url}' --resolve static.test:80:127.0.0.1 http://static.test/x)"
[[ "$loc" == "301 https://static.test/x" ]] || fail "force https: $loc"
pass "force HTTPS redirect"

# The ACME challenge path must reach the panel's responder over HTTPS too
# (validators follow Cloudflare's HTTP->HTTPS redirect).
[[ "$(get443 static.test /.well-known/acme-challenge/nope)" == "404 page not found" ]] || fail "acme path over https not routed to responder"
pass "ACME challenge path routed on HTTPS"

# ---------- switch PHP version, stop, delete ----------
PID="$(site_id php.test)"
wait_task "$(api PATCH "/sites/$PID" '{"php_version":"8.2"}' | jget "['task_id']")"
[[ "$(get80 php.test /index.php)" == php-ok-8.2* ]] || fail "php switch to 8.2"
pass "switch PHP 8.3 -> 8.2"

wait_task "$(api PATCH "/sites/$PID" '{"stopped":true}' | jget "['task_id']")"
[[ "$(curl -s -o /dev/null -w '%{http_code}' --resolve php.test:80:127.0.0.1 http://php.test/)" == 503 ]] || fail "stopped site"
wait_task "$(api PATCH "/sites/$PID" '{"stopped":false}' | jget "['task_id']")"
pass "stop / start site"

wait_task "$(api POST "/sites/$SID/delete" '{"files":true}' | jget "['task_id']")"
[[ ! -d /www/wwwroot/static.test ]] || fail "files not deleted"
[[ "$(curl -s -o /dev/null -w '%{http_code}' --resolve static.test:80:127.0.0.1 http://static.test/)" == 404 ]] || fail "deleted site still served"
pass "delete site"

# ---------- ACME HTTP-01 against Pebble ----------
cat > /tmp/pebble.json <<'EOF'
{"pebble":{"listenAddress":"0.0.0.0:14000","managementListenAddress":"0.0.0.0:15000",
 "certificate":"test/certs/localhost/cert.pem","privateKey":"test/certs/localhost/key.pem",
 "httpPort":80,"tlsPort":443,"ocspResponderURL":"","externalAccountBindingRequired":false}}
EOF
docker run -d --name challtestsrv --network host ghcr.io/letsencrypt/pebble-challtestsrv:latest \
  -defaultIPv4 127.0.0.1 -defaultIPv6 "" >/dev/null
docker run -d --name pebble --network host -e PEBBLE_VA_NOSLEEP=1 -e PEBBLE_WFE_NONCEREJECT=0 \
  -v /tmp/pebble.json:/config.json:ro ghcr.io/letsencrypt/pebble:latest -config /config.json -dnsserver 127.0.0.1:8053 >/dev/null
curl -fsSL https://raw.githubusercontent.com/letsencrypt/pebble/main/test/certs/pebble.minica.pem -o /opt/lmcwhm/pebble.pem
mkdir -p /etc/systemd/system/lmcwhm.service.d
printf '[Service]\nEnvironment=LEGO_CA_CERTIFICATES=/opt/lmcwhm/pebble.pem\n' > /etc/systemd/system/lmcwhm.service.d/pebble.conf
systemctl daemon-reload && systemctl restart lmcwhm
for _ in $(seq 1 30); do curl -sk -o /dev/null "$P/" && break; sleep 1; done
for _ in $(seq 1 30); do curl -sk -o /dev/null https://localhost:14000/dir && break; sleep 1; done
for _ in $(seq 1 30); do dig +short @127.0.0.1 -p 8053 acme.test 2>/dev/null | grep -q 127.0.0.1 && break; sleep 1; done
dig +short @127.0.0.1 -p 8053 acme.test | grep -q 127.0.0.1 || { docker logs challtestsrv; fail "challtestsrv DNS not answering"; }
curl -sk -c "$JAR" -o /dev/null "$P/console"
CSRF="$(curl -sk -b "$JAR" -c "$JAR" -X POST "$P/api/v1/auth/login" -d '{"username":"admin","password":"Passw0rd1"}' | jget "['csrf']")"

api PUT /web/config '{"acme_email":"test@example.com","acme_ca":"custom","acme_directory":"https://localhost:14000/dir"}' >/dev/null
wait_task "$(api POST /sites '{"domain":"acme.test","type":"static","ssl":"http","force_https":true}' | jget "['task_id']")"
api GET "/sites/$(site_id acme.test)" | grep -q '"cert_issuer":"Pebble' || fail "certificate not issued: $(api GET "/sites/$(site_id acme.test)")"
issuer acme.test | grep -qi "pebble" || fail "edge does not serve the Pebble certificate"
[[ "$(curl -s -o /dev/null -w '%{http_code}' --resolve acme.test:80:127.0.0.1 http://acme.test/)" == 301 ]] || fail "acme.test not redirected to https"
pass "ACME HTTP-01 certificate issued and served"

# ---------- panel restart regenerates the stack ----------
rm -f /opt/lmcwhm/web/nginx/conf.d/acme.test.conf
systemctl restart lmcwhm
for _ in $(seq 1 30); do [[ -f /opt/lmcwhm/web/nginx/conf.d/acme.test.conf ]] && break; sleep 1; done
[[ -f /opt/lmcwhm/web/nginx/conf.d/acme.test.conf ]] || fail "config not regenerated on boot"
pass "configs regenerated from database on restart"
