#!/usr/bin/env bash
# End-to-end test of M2 (App Store, runtime projects, databases) and M3
# (Docker manager, domain binding, TLS passthrough, files) on a disposable
# Ubuntu host with Docker.
# Expects lmcwhm installed with: --port 18888 --entry /console --username admin --password Passw0rd1
# Usage: sudo bash deploy/test-apps.sh
set -euo pipefail

P=https://127.0.0.1:18888
JAR="$(mktemp)"
CSRF=""
pass() { printf '\033[32mPASS\033[0m %s\n' "$*"; }
fail() {
  printf '\033[31mFAIL\033[0m %s\n' "$*"
  journalctl -u lmcwhm -n 60 --no-pager || true
  docker ps -a --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}' || true
  exit 1
}

api() { # method path [json]
  curl -sk -b "$JAR" -c "$JAR" -X "$1" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
    ${3:+-d "$3"} "$P/api/v1$2"
}
jget() { python3 -c "import sys,json; d=json.load(sys.stdin); print(eval('d'+sys.argv[1]))" "$1"; }

wait_task() { # task_id
  [[ -n "$1" ]] || fail "no task id"
  for _ in $(seq 1 900); do
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
task() { wait_task "$(api "$@" | jget ".get('task_id','')")"; }
app_id() { api GET /apps | python3 -c "import sys,json; print([a['id'] for a in json.load(sys.stdin)['items'] if a['app']==sys.argv[1] and a['version']==sys.argv[2]][0])" "$1" "$2"; }
site_id() { api GET /sites | python3 -c "import sys,json; print([s['id'] for s in json.load(sys.stdin)['items'] if s['domain']==sys.argv[1]][0])" "$1"; }
get80() { curl -s --resolve "$1:80:127.0.0.1" "http://$1${2:-/}"; }
get80_retry() { # host path want
  for _ in $(seq 1 60); do
    out="$(get80 "$1" "$2" || true)"
    [[ "$out" == *"$3"* ]] && return 0
    sleep 2
  done
  echo "    last response: $out"
  return 1
}

# ---------- login ----------
curl -sk -c "$JAR" -o /dev/null "$P/console"
CSRF="$(curl -sk -b "$JAR" -c "$JAR" -X POST "$P/api/v1/auth/login" -d '{"username":"admin","password":"Passw0rd1"}' | jget "['csrf']")"
[[ -n "$CSRF" ]] || fail "login"
task POST /web/install

# ---------- App Store: runtime + database engines ----------
task POST /apps/install '{"app":"node","version":"22"}'
task POST /apps/install '{"app":"python","version":"3.12"}'
task POST /apps/install '{"app":"mariadb","version":"11.4"}'
task POST /apps/install '{"app":"postgres","version":"17"}'
task POST /apps/install '{"app":"redis","version":"7.4"}'
[[ "$(docker inspect -f '{{.State.Status}}' lmcwhm-mariadb114)" == running ]] || fail "mariadb not running"
docker port lmcwhm-mariadb114 | grep -q "127.0.0.1:3306" || fail "mariadb not published on loopback 3306"
code="$(api POST /apps/install '{"app":"node","version":"22"}' | jget "['code']")"
[[ "$code" == app.already_installed ]] || fail "duplicate install: $code"
pass "App Store installs runtimes and database engines"

# ---------- runtime projects ----------
task POST /sites '{"domain":"node.test","type":"node","runtime_version":"22","ssl":"none"}'
get80_retry node.test / "Node.js project created by lmcwhm" || fail "node project not served"
task POST /sites '{"domain":"py.test","type":"python","runtime_version":"3.12","app_port":5055,"ssl":"none"}'
get80_retry py.test / "Python project created by lmcwhm" || fail "python project not served"
NID="$(site_id node.test)"
code="$(api POST /apps/$(app_id node 22)/uninstall | jget "['code']")"
[[ "$code" == app.in_use ]] || fail "runtime in use could be uninstalled: $code"
cat > /www/wwwroot/node.test/server.js <<'EOF'
require('http').createServer((q, s) => s.end('node-v2 ' + process.version)).listen(process.env.PORT, process.env.HOST)
EOF
[[ "$(api POST "/sites/$NID/project/restart" | jget "['ok']")" == True ]] || fail "project restart"
get80_retry node.test / "node-v2 v22" || fail "project restart did not pick up new code"
api GET "/sites/$NID/project" | grep -q '"state":"running"' || fail "project state"
pass "Node.js and Python projects behind nginx; restart deploys new code"

# ---------- databases ----------
MID="$(app_id mariadb 11.4)"
task POST /databases "{\"app_id\":$MID,\"name\":\"shop\",\"password\":\"Shop-pass-123\"}"
docker exec -e MYSQL_PWD=Shop-pass-123 lmcwhm-mariadb114 mariadb -ushop -h127.0.0.1 shop -e "CREATE TABLE t(x INT); INSERT INTO t VALUES (42)" \
  || fail "mariadb user cannot use its database"
PGID="$(app_id postgres 17)"
task POST /databases "{\"app_id\":$PGID,\"name\":\"blog\"}"
DBID="$(api GET /databases | python3 -c "import sys,json; print([d['id'] for d in json.load(sys.stdin)['items'] if d['name']=='blog'][0])")"
PGPW="$(api GET /databases | python3 -c "import sys,json; print([d['password'] for d in json.load(sys.stdin)['items'] if d['name']=='blog'][0])")"
docker exec -e PGPASSWORD="$PGPW" lmcwhm-postgres17 psql -h 127.0.0.1 -U blog -d blog -c "CREATE TABLE t(x int)" >/dev/null \
  || fail "postgres user cannot use its database"
NEWPW="$(api POST "/databases/$DBID/password" '{}' | jget "['password']")"
docker exec -e PGPASSWORD="$NEWPW" lmcwhm-postgres17 psql -h 127.0.0.1 -U blog -d blog -c "SELECT 1" >/dev/null || fail "changed password"
code="$(api POST /databases "{\"app_id\":$MID,\"name\":\"x'; DROP\"}" | jget "['code']")"
[[ "$code" == db.invalid_name ]] || fail "unsafe name accepted: $code"

# A PHP site reaches MariaDB over the loopback port.
task POST /sites '{"domain":"db.test","type":"php","php_version":"8.3","ssl":"none"}'
cat > /www/wwwroot/db.test/index.php <<'EOF'
<?php $m = new mysqli('127.0.0.1', 'shop', 'Shop-pass-123', 'shop', 3306); echo 'db-ok-' . $m->query('SELECT x FROM t')->fetch_row()[0];
EOF
[[ "$(get80 db.test /index.php)" == "db-ok-42" ]] || fail "php -> mariadb: $(get80 db.test /index.php)"
code="$(api POST /apps/$MID/uninstall | jget "['code']")"
[[ "$code" == app.in_use ]] || fail "engine with databases could be uninstalled: $code"
task POST "/databases/$DBID/delete"
pass "databases on MariaDB and PostgreSQL; PHP connects over loopback"

# ---------- App config edit with rollback ----------
RID="$(app_id redis 7.4)"
conf="$(api GET "/apps/$RID/config" | jget "['config']")"
body="$(python3 -c "import json,sys; print(json.dumps({'config': sys.argv[1] + '\nthis-is-not-a-directive yes\n'}))" "$conf")"
t="$(api PUT "/apps/$RID/config" "$body" | jget "['task_id']")"
for _ in $(seq 1 300); do st="$(api GET "/tasks/$t" | jget "['status']")"; [[ "$st" != running ]] && break; sleep 1; done
[[ "$st" == failed ]] || fail "broken redis.conf was accepted"
[[ "$(api GET "/apps/$RID/config" | jget "['config']")" == "$conf" ]] || fail "redis.conf not restored"
[[ "$(docker inspect -f '{{.State.Status}}' lmcwhm-redis74)" == running ]] || fail "redis not running after rollback"
pass "config edit restarts the service and rolls back a broken file"

# ---------- Docker manager: create container, bind a domain ----------
task POST /docker/containers '{"name":"hello","image":"nginx:stable","ports":["127.0.0.1:8181:80"],"restart":"unless-stopped"}'
api GET /docker/containers | python3 -c "import sys,json; c=[c for c in json.load(sys.stdin)['items'] if c['name']=='hello'][0]; assert c['host_ports']==[8181] and not c['managed'], c" \
  || fail "container list"
task POST /sites '{"domain":"hello.test","type":"proxy","proxy_target":"http://127.0.0.1:8181","ssl":"none","remark":"docker: hello"}'
get80_retry hello.test / "Welcome to nginx" || fail "container bound to domain"
code="$(api POST /docker/containers/lmcwhm-nginx/remove | jget "['code']")"
[[ "$code" == docker.managed ]] || fail "managed container could be removed: $code"
[[ "$(api POST /docker/containers/hello/stop | jget "['ok']")" == True ]] || fail "stop container"
[[ "$(api POST /docker/containers/hello/remove | jget "['ok']")" == True ]] || fail "remove container"
pass "Docker container created, bound to a domain, protected stack containers"

# ---------- Compose ----------
body="$(python3 -c "import json; print(json.dumps({'name':'web','compose':'services:\n  web:\n    image: nginx:stable\n    ports:\n      - \"127.0.0.1:8182:80\"\n','env':'','up':True}))")"
task POST /docker/compose "$body"
curl -s http://127.0.0.1:8182/ | grep -q "Welcome to nginx" || fail "compose project not up"
api GET /docker/compose | grep -q '"running":1' || fail "compose status"
task POST /docker/compose/web/delete '{"volumes":true}'
[[ -z "$(docker ps -q --filter label=com.docker.compose.project=web)" ]] || fail "compose containers left behind"
pass "Compose project up and delete"

# ---------- TLS passthrough: the backend's own certificate is served ----------
task POST /sites '{"domain":"pass.test","type":"proxy","proxy_target":"127.0.0.1:18888","passthrough":true,"ssl":"none"}'
subj="$(openssl s_client -connect 127.0.0.1:443 -servername pass.test </dev/null 2>/dev/null | openssl x509 -noout -subject)"
panel="$(openssl s_client -connect 127.0.0.1:18888 </dev/null 2>/dev/null | openssl x509 -noout -subject)"
[[ -n "$subj" && "$subj" == "$panel" ]] || fail "passthrough served '$subj', backend has '$panel'"
[[ "$(curl -s -o /dev/null -w '%{http_code}' --resolve pass.test:80:127.0.0.1 http://pass.test/)" == 301 ]] || fail "passthrough http redirect"
pass "TLS passthrough by SNI"

# ---------- file manager ----------
printf 'chunk-1|' | curl -sk -b "$JAR" -X POST -H "X-CSRF-Token: $CSRF" --data-binary @- "$P/api/v1/files/upload?path=/www/wwwroot/up.txt&offset=0" >/dev/null
printf 'chunk-2' | curl -sk -b "$JAR" -X POST -H "X-CSRF-Token: $CSRF" --data-binary @- "$P/api/v1/files/upload?path=/www/wwwroot/up.txt&offset=8&last=1" >/dev/null
[[ "$(cat /www/wwwroot/up.txt)" == "chunk-1|chunk-2" ]] || fail "chunked upload"
python3 - <<'EOF'
import zipfile
with zipfile.ZipFile('/www/wwwroot/evil.zip', 'w') as z:
    z.writestr('../../../tmp/zip-slip-pwned', 'x')
EOF
code="$(api POST /files/extract '{"path":"/www/wwwroot/evil.zip","dest":"/www/wwwroot/evil"}' | jget "['code']")"
[[ "$code" == files.invalid_path && ! -e /tmp/zip-slip-pwned ]] || fail "zip-slip: $code"
[[ "$(api POST /files/delete '{"paths":["/www/wwwroot/up.txt"]}' | jget "['ok']")" == True ]] || fail "delete to recycle bin"
TID="$(api GET /files/trash | python3 -c "import sys,json; print([i['id'] for i in json.load(sys.stdin)['items'] if i['path']=='/www/wwwroot/up.txt'][0])")"
api POST /files/trash/restore "{\"id\":\"$TID\"}" >/dev/null
[[ -f /www/wwwroot/up.txt ]] || fail "restore from recycle bin"
code="$(api POST /terminal/ticket '{"password":"wrong"}' | jget "['code']")"
[[ "$code" == auth.bad_password ]] || fail "terminal opened without the password: $code"
pass "files: chunked upload, zip-slip blocked, recycle bin; terminal needs the password"

# ---------- panel restart keeps projects and services ----------
systemctl restart lmcwhm
for _ in $(seq 1 30); do curl -sk -o /dev/null "$P/" && break; sleep 1; done
get80_retry node.test / "node-v2" || fail "project down after panel restart"
[[ "$(docker inspect -f '{{.State.Status}}' lmcwhm-mariadb114)" == running ]] || fail "mariadb down after panel restart"
pass "panel restart keeps projects and services running"

echo "all app tests passed"
