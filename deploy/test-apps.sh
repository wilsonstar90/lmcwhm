#!/usr/bin/env bash
# End-to-end test of M2 (App Store, runtime projects, databases), M3
# (Docker manager, domain binding, TLS passthrough, files) and M4–M10 on a
# disposable Ubuntu host with Docker.
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

wait_task() { # task_id [seconds]
  [[ -n "$1" ]] || fail "no task id"
  for _ in $(seq 1 "${2:-900}"); do
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
# PHP 8.3 is built on an older patch release, so the update check has something to find.
docker pull -q php:8.3.0-fpm >/dev/null && docker tag php:8.3.0-fpm php:8.3-fpm
task POST /web/install

# ---------- App Store: runtime + database engines ----------
task POST /apps/install '{"app":"node","version":"22"}'
task POST /apps/install '{"app":"python","version":"3.12"}'
task POST /apps/install '{"app":"mariadb","version":"11.4"}'
task POST /apps/install '{"app":"postgres","version":"17"}'
# An older patch release stands in for redis:7.4, so the update check has something to find.
docker pull -q redis:7.4.0 >/dev/null && docker tag redis:7.4.0 redis:7.4
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
# Dropping a database keeps a dump in the recycle bin; restoring brings back the data, user and password.
task POST /databases "{\"app_id\":$MID,\"name\":\"trashme\",\"password\":\"Trash-pass-123\"}"
docker exec -e MYSQL_PWD=Trash-pass-123 lmcwhm-mariadb114 mariadb -utrashme -h127.0.0.1 trashme -e "CREATE TABLE t(x INT); INSERT INTO t VALUES (7)"
TID="$(api GET /databases | python3 -c "import sys,json; print([d['id'] for d in json.load(sys.stdin)['items'] if d['name']=='trashme'][0])")"
task POST "/databases/$TID/delete"
BIN="$(api GET /databases/trash | python3 -c "import sys,json; print([d['id'] for d in json.load(sys.stdin)['items'] if d['name']=='trashme'][0])")"
task POST "/databases/trash/$BIN/restore"
out="$(docker exec -e MYSQL_PWD=Trash-pass-123 lmcwhm-mariadb114 mariadb -N -utrashme -h127.0.0.1 trashme -e "SELECT x FROM t")"
[[ "$out" == 7 ]] || fail "database restored from the recycle bin: $out"
api GET /databases/trash | grep -q '"trashme"' && fail "restored database still in the bin"
TID="$(api GET /databases | python3 -c "import sys,json; print([d['id'] for d in json.load(sys.stdin)['items'] if d['name']=='trashme'][0])")"
task POST "/databases/$TID/delete"
api POST /databases/trash/empty >/dev/null
pass "database recycle bin: drop keeps a dump, restore brings it back"
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

# phpMyAdmin: one server per MySQL / MariaDB, signed in by one-time links:
# a database's link shows only that database, the root link every one.
task POST /apps/install '{"app":"mysql","version":"8.4"}'
MYID="$(app_id mysql 8.4)"
task POST /databases "{\"app_id\":$MYID,\"name\":\"pmtest\"}"
task POST /databases "{\"app_id\":$MYID,\"name\":\"pmother\"}"
task POST /apps/install '{"app":"phpmyadmin","version":"5.2"}'
ss -ltnH | awk '{print $4}' | grep -E ':8091$' | grep -qv '^127\.0\.0\.1:' && fail "phpMyAdmin listens beyond loopback: $(ss -ltn | grep 8091)"
pma_server() { api GET /databases | python3 -c "import sys,json; print(json.load(sys.stdin)['phpmyadmin'][sys.argv[1]])" "$1"; }
db_id() { api GET /databases | python3 -c "import sys,json; print([d['id'] for d in json.load(sys.stdin)['items'] if d['name']==sys.argv[1]][0])" "$1"; }
PJ="$(mktemp)"; cp "$JAR" "$PJ"
pma_signon() { curl -sk -b "$PJ" -c "$PJ" -L -o /dev/null -w '%{http_code}' "$P$(api GET "$1" | jget "['url']")"; }
pma_dbs() { curl -sk -b "$PJ" -c "$PJ" "$P/phpmyadmin/index.php?route=/server/databases&server=$1"; }
[[ "$(pma_signon "/databases/$(db_id pmtest)/phpmyadmin")" == 200 ]] || fail "phpMyAdmin sign-on as the database user"
out="$(pma_dbs "$(pma_server "$MYID")")"
[[ "$out" == *pmtest* && "$out" != *pmother* ]] || { echo "$out" | tail -40; fail "phpMyAdmin for one database shows others"; }
[[ "$(pma_signon "/apps/$MYID/phpmyadmin")" == 200 ]] || fail "phpMyAdmin root sign-on"
out="$(pma_dbs "$(pma_server "$MYID")")"
[[ "$out" == *pmtest* && "$out" == *pmother* ]] || { echo "$out" | tail -40; fail "phpMyAdmin root cannot see every database"; }
[[ "$(pma_signon "/apps/$MID/phpmyadmin")" == 200 ]] || fail "phpMyAdmin root sign-on (MariaDB)"
out="$(pma_dbs "$(pma_server "$MID")")"
[[ "$out" == *shop* ]] || { echo "$out" | tail -40; fail "phpMyAdmin cannot open MariaDB"; }
# A link works once; the sign-on script without one, or straight on loopback, signs nobody in.
URL="$(api GET "/databases/$(db_id pmtest)/phpmyadmin" | jget "['url']")"
curl -sk -b "$PJ" -o /dev/null "$P$URL"
[[ "$(curl -sk -b "$JAR" -o /dev/null -w '%{http_code}' "$P$URL")" == 403 ]] || fail "phpMyAdmin sign-on link reused"
[[ "$(curl -s -o /dev/null -w '%{http_code}' -H 'X-Lmcwhm-Signon: x' http://127.0.0.1:8091/phpmyadmin/lmcwhm-signon.php)" == 403 ]] || fail "forged sign-on header accepted"
out="$(curl -s -L http://127.0.0.1:8091/phpmyadmin/index.php?route=/server/databases)"
[[ "$out" != *pmtest* ]] || fail "phpMyAdmin on loopback is signed in without a link"
J2="$(mktemp)"; curl -sk -c "$J2" -o /dev/null "$P/console" # security entry passed, not signed in
[[ "$(curl -sk -b "$J2" -o /dev/null -w '%{http_code}' "$P/phpmyadmin/")" == 404 ]] || fail "phpMyAdmin reachable without a panel session"
[[ "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8091/phpmyadmin/)" =~ ^(200|302)$ ]] || fail "phpMyAdmin not on loopback 8091"
pass "phpMyAdmin: per-database sign-on shows only that database; root link; one-time links; MariaDB and MySQL 8.4"

# Database settings window: MySQL status, slow log, binary logs, tuning with restart.
api GET "/apps/$MYID/mysql/status" | python3 -c "import sys,json; s=json.load(sys.stdin); assert s['uptime']>0 and s['vars']['max_connections'] and s['status']['connections'], s" || fail "mysql status"
api POST "/apps/$MYID/mysql/slowlog" '{"on":true,"seconds":0.1}' | grep -q '"ok":true' || fail "slow log on"
docker exec -e MYSQL_PWD="$(api GET "/apps/$MYID/credentials" | jget "['password']")" lmcwhm-mysql84 mysql -uroot -h127.0.0.1 -e "SELECT SLEEP(0.3)" >/dev/null
for _ in 1 2 3 4 5; do api GET "/apps/$MYID/mysql/slowlog" | grep -q 'SLEEP' && break; sleep 1; done
api GET "/apps/$MYID/mysql/slowlog" | grep -q 'SLEEP' || fail "slow query not logged: $(api GET "/apps/$MYID/mysql/slowlog")"
api GET "/apps/$MYID/mysql/binlogs" | python3 -c "import sys,json; b=json.load(sys.stdin); assert b['enabled'] and b['files'], b" || fail "mysql 8.4 binary logs"
task PUT "/apps/$MYID/mysql/tuning" '{"values":{"max_connections":"321","tmp_table_size":"48M"}}'
api GET "/apps/$MYID/mysql/status" | python3 -c "import sys,json; v=json.load(sys.stdin)['vars']; assert v['max_connections']=='321' and v['tmp_table_size']==str(48<<20), v" || fail "tuning not applied"
code="$(api PUT "/apps/$MYID/mysql/tuning" '{"values":{"max_connections":"1; DROP"}}' | jget "['code']")"
[[ "$code" == app.bad_setting ]] || fail "bad tuning value accepted: $code"
api GET "/apps/$MYID/service" | python3 -c "import sys,json; s=json.load(sys.stdin); assert s['state']=='running' and s['data_size']>0, s" || fail "service info"
pass "database settings: MySQL status, slow log, binary logs, tuning applied with restart"

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

# Redis key browser and settings applied live (no restart, keys kept); port move.
api POST "/apps/$RID/redis/key" '{"db":3,"key":"lmc:a","value":"hello","ttl":0}' | grep -q '"ok":true' || fail "redis set"
api POST "/apps/$RID/redis/key" '{"db":3,"key":"lmc:b","value":"x","ttl":600}' >/dev/null
api GET "/apps/$RID/redis/dbs" | python3 -c "import sys,json; d=json.load(sys.stdin)['items']; assert len(d)>=4 and d[3]['keys']==2, d" || fail "redis db counts"
api GET "/apps/$RID/redis/keys?db=3&match=lmc" | python3 -c "import sys,json; k={x['key']:x for x in json.load(sys.stdin)['keys']}; assert k['lmc:a']['preview']=='hello' and k['lmc:a']['ttl']==-1 and 0<k['lmc:b']['ttl']<=600, k" || fail "redis scan"
api GET "/apps/$RID/redis/key?db=3&key=lmc:a" | grep -q '"value":"hello"' || fail "redis get"
api POST "/apps/$RID/redis/delete" '{"db":3,"keys":["lmc:b"]}' | grep -q '"deleted":1' || fail "redis delete"
cur="$(api GET "/apps/$RID/redis/settings")"
new="$(python3 -c "import json,sys; s=json.loads(sys.argv[1]); s.update(maxmemory='64mb', maxmemory_policy='allkeys-lfu', save=['600 5']); print(json.dumps(s))" "$cur")"
[[ "$(api PUT "/apps/$RID/redis/settings" "$new" | jget "['task_id']")" == "" ]] || fail "redis settings restarted instead of applying live"
api GET "/apps/$RID/redis/info" | python3 -c "import sys,json; i=json.load(sys.stdin); assert i['maxmemory']==str(64<<20) and i['maxmemory_policy']=='allkeys-lfu', {k:i.get(k) for k in ('maxmemory','maxmemory_policy')}" || fail "redis settings not live"
api GET "/apps/$RID/redis/keys?db=3" | grep -q 'lmc:a' || fail "redis lost keys on a live setting"
api GET "/apps/$RID/config" | jget "['config']" | grep -q '^save 600 5$' || fail "redis.conf not written"
task POST "/apps/$RID/port" '{"port":16379}'
api GET "/apps/$RID/redis/key?db=3&key=lmc:a" | grep -q '"value":"hello"' || fail "redis after port move"
ss -ltnH | awk '{print $4}' | grep -q '^127.0.0.1:16379$' || fail "redis not on the new port"
code="$(api POST "/apps/$RID/port" '{"port":22}' | jget "['code']")"
[[ "$code" == app.bad_port ]] || fail "privileged port accepted: $code"
task POST "/apps/$RID/port" '{"port":6379}'
pass "Redis: key browser, settings applied live, port moved"

# ---------- compile install: switch Redis to a source build and back, data kept ----------
code="$(api POST "/apps/$RID/build" '{"build_mode":"source","build_args":"x; rm -rf /"}' | jget "['code']")"
[[ "$code" == app.invalid_build_args ]] || fail "shell characters in build options accepted: $code"
code="$(api POST /apps/install '{"app":"sqlserver","version":"2022","build_mode":"source"}' | jget "['code']")"
[[ "$code" == app.no_source_build ]] || fail "SQL Server compile install accepted: $code"
task POST "/apps/$RID/build" '{"build_mode":"source","build_cflags":"-O2"}'
[[ "$(docker inspect -f '{{.Config.Image}}' lmcwhm-redis74)" == lmcwhm/src-redis:7.4 ]] || fail "redis not on the compiled image"
docker image inspect -f '{{index .Config.Labels "lmcwhm.build"}}' lmcwhm/src-redis:7.4 | grep -q source || fail "compiled image not labelled"
docker exec lmcwhm-redis74 redis-server --version | grep -q 'v=7.4.0' || fail "compiled redis version"
api GET "/apps/$RID/redis/key?db=3&key=lmc:a" | grep -q '"value":"hello"' || fail "redis lost data on the compile switch"
api GET /apps | python3 -c "import sys,json; a=[a for a in json.load(sys.stdin)['items'] if a['app']=='redis'][0]; assert a['build_mode']=='source' and a['build_cflags']=='-O2', a" \
  || fail "build mode not stored"
ls /opt/lmcwhm/logs/build/redis-7.4.log >/dev/null || fail "no build log"
task POST "/apps/$RID/build" '{"build_mode":"fast"}'
[[ "$(docker inspect -f '{{.Config.Image}}' lmcwhm-redis74)" == redis:7.4 ]] || fail "redis not back on the official image"
api GET "/apps/$RID/redis/key?db=3&key=lmc:a" | grep -q '"value":"hello"' || fail "redis lost data switching back"
docker image inspect lmcwhm/src-redis:7.4 >/dev/null 2>&1 && fail "compiled image left behind"
pass "compile install: Redis compiled from source and back to the pre-built image, data kept"

# PHP compiled from source keeps the official configure options (a quoting
# bug once passed the image's CFLAGS to ./configure) and still runs a site.
wait_task "$(api POST /php/8.4/install '{"build_mode":"source","build_cflags":"-O2"}' | jget "['task_id']")" 2400
docker exec lmcwhm-php84 php -v | grep -q '^PHP 8\.4' || fail "compiled PHP 8.4 does not run"
docker exec lmcwhm-php84 php -m | grep -qx mysqli || fail "compiled PHP 8.4 lacks the panel's extensions"
docker exec lmcwhm-php84 php-config --configure-options | grep -q -- '--enable-fpm' || fail "compiled PHP 8.4 lost the official options"
task POST /sites '{"domain":"src84.test","type":"php","php_version":"8.4","ssl":"none"}'
echo '<?php echo "src-" . PHP_MAJOR_VERSION . PHP_MINOR_VERSION;' > /www/wwwroot/src84.test/index.php
[[ "$(get80 src84.test /index.php)" == src-84 ]] || fail "site on compiled PHP 8.4: $(get80 src84.test /index.php)"
task POST "/sites/$(site_id src84.test)/delete" '{"files":true}'
api POST /php/8.4/remove >/dev/null
docker inspect lmcwhm-php84 >/dev/null 2>&1 && fail "PHP 8.4 not removed"
pass "compile install: PHP 8.4 compiled from source runs a site"

# ---------- software updates: exact versions, patch update with data kept ----------
api GET /apps | python3 -c "import sys,json; a=[a for a in json.load(sys.stdin)['items'] if a['app']=='redis'][0]; assert a['full_version']=='7.4.0', a" \
  || fail "redis full version"
api POST /apps/updates/check | python3 -c "
import sys,json; u=json.load(sys.stdin)['items']['app:'+sys.argv[1]]
assert u['available'] and u['current']=='7.4.0' and u['latest'].startswith('7.4.') and u['latest']!='7.4.0', u" "$RID" || fail "update check did not find the redis patch"
task POST "/apps/$RID/update"
api GET /apps | python3 -c "import sys,json; a=[a for a in json.load(sys.stdin)['items'] if a['app']=='redis'][0]; assert a['full_version'].startswith('7.4.') and a['full_version']!='7.4.0', a" \
  || fail "redis not updated"
api GET "/apps/$RID/redis/key?db=3&key=lmc:a" | grep -q '"value":"hello"' || fail "redis lost data in the update"
api GET /apps/updates | python3 -c "import sys,json; u=json.load(sys.stdin)['items']['app:'+sys.argv[1]]; assert not u['available'], u" "$RID" || fail "update still offered"
docker image inspect lmcwhm/rollback:redis-7.4 >/dev/null 2>&1 && fail "rollback image left behind"
task POST "/apps/$RID/update" # already the newest: nothing to do
task POST /apps/install '{"app":"memcached","version":"1.6"}'
task POST "/apps/$(app_id memcached 1.6)/uninstall"
docker image inspect memcached:1.6 >/dev/null 2>&1 && fail "uninstall left the image"
pass "software updates: exact versions, patch update found and applied with data kept; uninstall removes the image"

# ---------- Docker manager: create container, bind a domain ----------
task POST /docker/containers '{"name":"hello","image":"nginx:stable","ports":["127.0.0.1:8181:80"],"restart":"unless-stopped"}'
api GET /docker/containers | python3 -c "import sys,json; c=[c for c in json.load(sys.stdin)['items'] if c['name']=='hello'][0]; assert c['host_ports']==[8181] and not c['managed'], c" \
  || fail "container list"
task POST /sites '{"domain":"hello.test","type":"proxy","proxy_target":"http://127.0.0.1:8181","ssl":"none","remark":"docker: hello"}'
get80_retry hello.test / "Welcome to nginx" || fail "container bound to domain"
code="$(api POST /docker/containers/lmcwhm-nginx/remove | jget "['code']")"
[[ "$code" == docker.managed ]] || fail "managed container could be removed: $code"
pass "Docker container created, bound to a domain, protected stack containers"

# ---------- Docker container settings: rename, policy, networks, logs, commit, upgrade, edit ----------
api GET /docker/overview | python3 -c "import sys,json; o=json.load(sys.stdin); assert o['containers']['running']>=1 and o['images']['total']>=1 and o['networks']['total']>=3, o" \
  || fail "docker overview"
[[ "$(api POST /docker/containers/hello/rename '{"name":"hello2"}' | jget "['ok']")" == True ]] || fail "rename container"
[[ "$(docker inspect -f '{{.Name}}' hello2)" == /hello2 ]] || fail "container not renamed"
code="$(api POST /docker/containers/lmcwhm-nginx/rename '{"name":"x"}' | jget "['code']")"
[[ "$code" == docker.managed ]] || fail "managed container could be renamed: $code"
[[ "$(api POST /docker/containers/hello2/restart-policy '{"policy":"on-failure","max_retry":3}' | jget "['ok']")" == True ]] || fail "restart policy"
[[ "$(docker inspect -f '{{.HostConfig.RestartPolicy.Name}}:{{.HostConfig.RestartPolicy.MaximumRetryCount}}' hello2)" == on-failure:3 ]] || fail "restart policy not applied"
api GET /docker/containers/hello2/info | python3 -c "import sys,json; c=json.load(sys.stdin); assert c['host_ports']==[8181] and c['restart']=='on-failure' and c['state']=='running' and c['networks'], c" \
  || fail "container info"
docker network create lmcwhm-e2e-net >/dev/null
[[ "$(api POST /docker/containers/hello2/networks/connect '{"network":"lmcwhm-e2e-net"}' | jget "['ok']")" == True ]] || fail "network connect"
docker inspect -f '{{json .NetworkSettings.Networks}}' hello2 | grep -q lmcwhm-e2e-net || fail "network not connected"
[[ "$(api POST /docker/containers/hello2/networks/disconnect '{"network":"lmcwhm-e2e-net"}' | jget "['ok']")" == True ]] || fail "network disconnect"
docker network rm lmcwhm-e2e-net >/dev/null
get80_retry hello.test / "Welcome to nginx" || fail "hello2 still served"
api GET "/docker/containers/hello2/logs?since=$(( $(date +%s) - 3*86400 ))&tail=100" | grep -q 'GET / HTTP' || fail "container logs (last 3 days)"
api GET /docker/containers/hello2/logs/download | grep -q 'GET / HTTP' || fail "container logs download"
[[ "$(api POST /docker/containers/hello2/logs/clear | jget "['ok']")" == True ]] || fail "clear logs"
[[ ! -s "$(docker inspect -f '{{.LogPath}}' hello2)" ]] || fail "log file not truncated"
task POST /docker/containers/hello2/commit '{"repository":"lmcwhm-e2e","tag":"v1","message":"e2e"}'
docker image inspect lmcwhm-e2e:v1 >/dev/null || fail "image not committed"
old="$(docker inspect -f '{{.Id}}' hello2)"
task POST /docker/containers/hello2/upgrade '{"force_pull":false,"force_recreate":false}'
[[ "$(docker inspect -f '{{.Id}}' hello2)" == "$old" ]] || fail "upgrade recreated an unchanged image"
task POST /docker/containers/hello2/upgrade '{"image":"lmcwhm-e2e:v1","force_pull":false,"force_recreate":false}'
[[ "$(docker inspect -f '{{.Id}}' hello2)" != "$old" ]] || fail "upgrade did not recreate"
[[ "$(docker inspect -f '{{.Config.Image}} {{.HostConfig.RestartPolicy.Name}}' hello2)" == "lmcwhm-e2e:v1 on-failure" ]] || fail "upgrade lost settings"
get80_retry hello.test / "Welcome to nginx" || fail "upgraded container not served"
spec="$(api GET /docker/containers/hello2/spec | python3 -c "import sys,json; s=json.load(sys.stdin); assert s['ports']==['127.0.0.1:8181:80'], s; s['env'].append('E2E=1'); s['image']='nginx:stable'; print(json.dumps(s))")"
task POST /docker/containers/hello2/recreate "$spec"
docker inspect -f '{{json .Config.Env}}' hello2 | grep -q '"E2E=1"' || fail "edit not applied"
get80_retry hello.test / "Welcome to nginx" || fail "edited container not served"
[[ -z "$(docker ps -aq -f name=hello2_lmcwhm_old)" ]] || fail "old container left behind"
[[ "$(api POST /docker/containers/hello2/stop | jget "['ok']")" == True ]] || fail "stop container"
[[ "$(api POST /docker/containers/hello2/remove | jget "['ok']")" == True ]] || fail "remove container"
docker rmi lmcwhm-e2e:v1 >/dev/null
pass "Docker container settings: rename, restart policy, networks, logs (range, download, clear), commit, upgrade, edit"

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

# ---------- SNI matching: exact > deeper wildcard > wildcard; no partial-label matches ----------
task POST /sites '{"domain":"w.test","aliases":["*.w.test"],"type":"static","ssl":"none"}'
task POST /sites '{"domain":"deep.w.test","type":"proxy","proxy_target":"127.0.0.1:18888","passthrough":true,"ssl":"none"}'
# Fingerprints, not subjects: the default and panel certificates are both self-signed "lmcwhm".
sni_fp() { openssl s_client -connect 127.0.0.1:443 -servername "$1" </dev/null 2>/dev/null | openssl x509 -noout -fingerprint -sha256; }
panel_fp="$(openssl s_client -connect 127.0.0.1:18888 </dev/null 2>/dev/null | openssl x509 -noout -fingerprint -sha256)"
[[ -n "$panel_fp" && "$(sni_fp deep.w.test)" == "$panel_fp" ]] || fail "exact passthrough inside a wildcard site"
[[ "$(sni_fp other.w.test)" != "$panel_fp" ]] || fail "wildcard host was passed through"
[[ "$(sni_fp pass.test.evil.example)" != "$panel_fp" ]] || fail "suffix-label hostname matched pass.test"
[[ "$(sni_fp x.pass.test)" != "$panel_fp" ]] || fail "subdomain matched pass.test"
[[ "$(get80 other.w.test /)" == *"w.test"* ]] || fail "wildcard alias not served by nginx"
api GET '/routes/resolve?host=a.b.w.test' | grep -q '"match":"wildcard"' || fail "resolve wildcard"
api GET '/routes/resolve?host=deep.w.test' | python3 -c "import sys,json; r=json.load(sys.stdin); assert r['match']=='exact' and r['route']['mode']=='passthrough', r" || fail "resolve exact"
api GET '/routes?check=1' | python3 -c "
import sys,json
rs={r['host']:r for r in json.load(sys.stdin)['items']}
assert rs['*.w.test']['overrides']==['deep.w.test'], rs['*.w.test']
assert rs['hello.test']['health'] in ('up','down'), rs['hello.test']
assert 'route.no_cert' in rs['w.test']['warnings'], rs['w.test']
" || fail "routes list"
pass "SNI: exact beats wildcard, no partial matches; routes API"

# ---------- dashboard update / repair ----------
api GET /system/update | python3 -c "import sys,json; r=json.load(sys.stdin); assert r['latest'].startswith('v') or r.get('error'), r" || fail "update check"
code="$(api POST /system/repair '{"password":"wrong"}' | jget "['code']")"
[[ "$code" == auth.bad_password ]] || fail "repair without the password: $code"
api GET /system/maintenance | grep -q '"running":false' || fail "maintenance status"
pass "update check; repair needs the password"

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

# ---------- M4: monitor, cron, log center, IP geolocation ----------
api GET '/monitor/series?range=15m' | python3 -c "import sys,json; p=json.load(sys.stdin)['items']; assert len(p)>=2 and 0<=p[-1]['cpu']<=100 and p[-1]['mem']>0, p[-3:]" || fail "monitor samples"
api GET '/monitor/processes?n=5' | python3 -c "import sys,json; assert any(p['name']=='lmcwhm' for p in json.load(sys.stdin)['items'])" || fail "process list"
api GET /monitor/services | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['docker'] and any(s['name']=='lmcwhm-haproxy' for s in d['items'])" || fail "services"
JID="$(api POST /cron '{"name":"ci","type":"shell","schedule":"@yearly","command":"echo cron-ok; exit 4"}' | jget "['id']")"
api POST "/cron/$JID/run" >/dev/null
for _ in $(seq 1 20); do st="$(api GET "/cron/$JID/runs" | python3 -c "import sys,json; r=json.load(sys.stdin)['items']; print(r[0]['status'] if r else '')")"; [[ "$st" && "$st" != running ]] && break; sleep 1; done
api GET "/cron/$JID/runs" | python3 -c "import sys,json; r=json.load(sys.stdin)['items'][0]; assert r['status']=='failed' and r['exit_code']==4 and 'cron-ok' in r['output'], r" || fail "cron run"
curl -s --resolve db.test:80:127.0.0.1 http://db.test/index.php >/dev/null
RID="$(api POST /cron '{"name":"rotate","type":"logrotate","schedule":"@daily","command":"{\"keep\":3}"}' | jget "['id']")"
api POST "/cron/$RID/run" >/dev/null
for _ in $(seq 1 20); do ls /www/wwwlogs/db.test.log.*.gz >/dev/null 2>&1 && break; sleep 1; done
ls /www/wwwlogs/db.test.log.*.gz >/dev/null 2>&1 || fail "log rotation"
curl -s --resolve db.test:80:127.0.0.1 http://db.test/after-rotate >/dev/null
sleep 1
api GET '/logs/site?domain=db.test&kind=access&lines=20' | python3 -c "import sys,json; i=json.load(sys.stdin)['items']; assert any(a['path']=='/after-rotate' and a['ip'] for a in i), i" \
  || fail "access log parsed after rotation (nginx reopened)"
api GET '/logs/ssh?limit=5' | python3 -c "import sys,json; d=json.load(sys.stdin); assert 'items' in d" || fail "ssh log"
api GET '/logs/journal?unit=lmcwhm&lines=20' | grep -q 'panel listening' || fail "panel journal"
for _ in $(seq 1 60); do api GET /geoip/status | grep -q '"has_asn":true' && break; sleep 2; done
api GET /geoip/status | grep -q '"edition":"country"' || fail "geoip database: $(api GET /geoip/status)"
api POST /geoip/lookup '{"ips":["8.8.8.8","10.0.0.1"]}' | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['8.8.8.8']['country']=='US' and d['8.8.8.8']['asn']==15169 and d['10.0.0.1']['private'], d" || fail "geoip lookup"
pass "M4: monitor, cron (shell + log rotation), site / SSH / journal logs, IP geolocation"

# ---------- Built-in scheduled tasks ----------
cron_job() { api GET /cron | python3 -c "import sys,json; print([j['id'] for j in json.load(sys.stdin)['items'] if j['type']==sys.argv[1] and j['command'].startswith(sys.argv[2])][0])" "$1" "$2"; }
cron_run() { # job id -> waits for the run, prints status and output
  api POST "/cron/$1/run" >/dev/null
  for _ in $(seq 1 120); do
    rid="$(api GET "/cron/$1/runs?limit=1" | python3 -c "import sys,json; r=json.load(sys.stdin)['items']; print(r[0]['id'] if r and r[0]['status']!='running' else '')")"
    [[ -n "$rid" ]] && break; sleep 1
  done
  api GET "/cron/runs/$rid" | python3 -c "import sys,json; r=json.load(sys.stdin); print(r['status']); print(r.get('output',''))"
}
for c in ssl_renew time_sync release_memory security_scan app_updates; do
  out="$(cron_run "$(cron_job system "$c")")"
  [[ "$(head -1 <<<"$out")" == success ]] || fail "built-in task $c: $out"
done
out="$(cron_run "$(cron_job malware_scan '{')")"
[[ "$(head -1 <<<"$out")" == success && "$out" == *"files scanned"* ]] || fail "malware scan task: $out"
pass "built-in scheduled tasks: certificates, time sync, memory, security and webshell scans"

# ---------- M5: firewall (nftables), WAF, CrowdSec ----------
# A container on the default bridge is the "outside" client: its traffic to
# the host (172.17.0.1) goes through the input chain like remote traffic.
docker pull -q busybox >/dev/null
probe() { docker run --rm busybox wget -T 4 -qO- "http://172.17.0.1:$1/" 2>&1 || true; }
fwput() { # json patch over the current config
  cur="$(api GET /firewall)"
  body="$(python3 -c "import sys,json; c=json.loads(sys.argv[1]); c.update(json.loads(sys.argv[2])); print(json.dumps(c))" "$cur" "$1")"
  api PUT /firewall "$body"
}
api GET /firewall | grep -q '"available":true' || fail "nft not available"
fwput '{"enabled":true,"policy":"accept","ping":true,"protect":true}' | grep -q '"confirm":false' || fail "enable firewall"
nft list table inet lmcwhm >/dev/null || fail "nft table missing"
# The old "always allow" switch is now three system port rules, listed with the others.
api GET /firewall | python3 -c "import sys,json; r=json.load(sys.stdin)['rules']; assert sorted(x['system'] for x in r if x.get('system'))==['panel','ssh','web'], r" || fail "system rules: $(api GET /firewall)"
nft list chain inet lmcwhm input | grep -q 'comment "system ssh"' || fail "system ssh rule not installed"
nft list chain inet lmcwhm input | grep -q 'log prefix "lmcwhm-drop block "' || fail "drops not logged"
python3 -m http.server 18190 --bind 0.0.0.0 >/dev/null 2>&1 &
HTTPD=$!
sleep 1
[[ "$(probe 18190)" == *"Directory listing"* ]] || fail "test service not reachable from a container: $(probe 18190)"
BID="$(api POST /firewall/ips '{"ip":"172.17.0.0/16","list":"block"}' | jget "['id']")"
[[ "$(probe 18190)" != *"Directory listing"* ]] || fail "blocked range still reaches the host"
[[ "$(probe 80)" != *"404"* ]] || fail "blocked range reaches the web edge"
api POST "/firewall/ips/$BID/delete" >/dev/null
[[ "$(probe 18190)" == *"Directory listing"* ]] || fail "unblocked range cannot reach the host"
code="$(api POST /firewall/ips '{"ip":"127.0.0.1","list":"block"}' | jget "['code']")"
[[ "$code" == fw.self_block ]] || fail "own address could be blocked: $code"
RLID="$(api POST /firewall/rules '{"action":"drop","proto":"tcp","ports":"18190","enabled":true}' | jget "['rule']['id']")"
[[ "$(probe 18190)" != *"Directory listing"* ]] || fail "port drop rule not enforced"
sleep 1
api GET '/logs/firewall?q=18190' | python3 -c "import sys,json; d=json.load(sys.stdin); assert any(x['dport']==18190 and x['proto']=='TCP' for x in d['items']), d" || echo "    (kernel log not readable here: firewall log check skipped)"
[[ "$(probe 80)" == *"404"* ]] || fail "protected port 80 affected by a rule"
api POST "/firewall/rules/$RLID/delete" >/dev/null
[[ "$(probe 18190)" == *"Directory listing"* ]] || fail "deleted rule still enforced"
# Deny default without confirmation reverts to allow.
fwput '{"policy":"drop"}' | grep -q '"confirm":true' || fail "drop policy not guarded"
nft list chain inet lmcwhm input | grep -q "policy drop" || fail "drop policy not installed"
sleep 65
api GET /firewall | grep -q '"policy":"accept"' || fail "unconfirmed drop policy not reverted"
nft list chain inet lmcwhm input | grep -q "policy accept" || fail "revert not installed"
# Confirmed from a new connection: kept.
fwput '{"policy":"drop"}' >/dev/null
sleep 1
[[ "$(api POST /firewall/confirm | jget "['ok']")" == True ]] || fail "confirm"
sleep 2
api GET /firewall | grep -q '"policy":"drop"' || fail "confirmed drop policy lost"
# Public blocklist feed: overlapping CIDRs and bogons from a list URL land in
# the ban sets without re-arming the drop-policy confirmation.
FEEDDIR="$(mktemp -d)"
printf '# test list\n10.0.0.0/8\n1.10.16.0/20\n1.10.20.0/24\n198.51.100.0/24\n45.155.205.233\t5\n2a0e:1c80::/29\n' > "$FEEDDIR/list.txt"
python3 -m http.server 18191 --bind 127.0.0.1 --directory "$FEEDDIR" >/dev/null 2>&1 &
FEEDHTTPD=$!
sleep 1
api PUT /firewall/feeds '{"enabled":[],"custom":[{"name":"test","url":"http://127.0.0.1:18191/list.txt"}],"hours":6}' | grep -q '"custom1"' || fail "feed not saved"
for _ in $(seq 1 20); do api GET /firewall/feeds | grep -q '"blocking":4' && break; sleep 1; done
api GET /firewall/feeds | python3 -c "import sys,json; f=json.load(sys.stdin); s=f['status']['custom1']; assert f['blocking']==4 and s['entries']==4 and s['skipped']==2 and not s.get('error'), f" || fail "feed refresh: $(api GET /firewall/feeds)"
nft list set inet lmcwhm block4 | grep -q "1.10.16.0/20" || fail "feed CIDR not in block4: $(nft list set inet lmcwhm block4)"
nft list set inet lmcwhm ban4 | grep -q "45.155.205.233" || fail "feed IP not in ban4"
nft list set inet lmcwhm block6 | grep -q "2a0e:1c80::/29" || fail "feed IPv6 CIDR not in block6"
! nft list set inet lmcwhm block4 | grep -q "10.0.0.0/8" || fail "bogon from a feed blocked"
api GET '/firewall/ips?list=block' | python3 -c "import sys,json; assert not any(x['source'].startswith('feed:') for x in json.load(sys.stdin)['items'])" || fail "feed entries in the IP list"
api GET /firewall | grep -q '"pending":false' || fail "feed refresh armed the drop-policy confirmation"
# Trust feed: the banned address becomes exempt from drops, without being accepted on every port.
printf '45.155.205.233\n2a0e:1c80::/32\n' > "$FEEDDIR/trust.txt"
api PUT /firewall/feeds '{"enabled":[],"custom":[{"name":"test","url":"http://127.0.0.1:18191/list.txt"},{"name":"trusted","url":"http://127.0.0.1:18191/trust.txt","kind":"trust"}],"hours":6}' >/dev/null
for _ in $(seq 1 20); do api GET /firewall/feeds | grep -q '"trusted":2' && break; sleep 1; done
api GET /firewall/feeds | grep -q '"trusted":2' || fail "trust feed: $(api GET /firewall/feeds)"
nft list set inet lmcwhm trust4 | grep -q "45.155.205.233" || fail "trusted IP not in trust4"
nft list chain inet lmcwhm input | grep -q 'ip saddr @ban4 ip saddr != @trust4' || fail "bans do not exempt trusted IPs"
! nft list chain inet lmcwhm input | grep -q '@trust4 accept' || fail "trusted IPs accepted on every port"
api PUT /firewall/feeds '{"enabled":[],"custom":[],"hours":6}' >/dev/null
api GET /firewall/feeds | grep -q '"blocking":0' || fail "unsubscribed feed kept its blocks"
! nft list set inet lmcwhm block4 | grep -q "1.10.16.0/20" || fail "feed CIDR left in nft"
kill $FEEDHTTPD
fwput '{"policy":"accept"}' >/dev/null
kill $HTTPD
pass "firewall: block list, port rules, protected ports, drop policy confirm / revert, public blocklist and trusted-IP feeds"

# WAF: OWASP CRS blocks an SQL injection; a site can opt out.
task PUT /waf '{"enabled":true,"engine":"On","paranoia":1,"anomaly":5}'
[[ "$(docker inspect -f '{{.Config.Image}}' lmcwhm-nginx)" == owasp/modsecurity-crs:* ]] || fail "nginx not switched to the WAF image"
[[ "$(get80 db.test /index.php)" == "db-ok-42" ]] || fail "WAF blocks a normal request: $(get80 db.test /index.php)"
code="$(curl -s -o /dev/null -w '%{http_code}' --resolve db.test:80:127.0.0.1 "http://db.test/index.php?id=1%27%20OR%20%271%27=%271")"
[[ "$code" == 403 ]] || fail "SQL injection not blocked: $code"
sleep 1
api GET '/waf/events?lines=50' | python3 -c "import sys,json; e=json.load(sys.stdin)['items']; assert any(x['blocked'] and any(m['id'].startswith('942') for m in x['messages']) for x in e), e[:2]" || fail "WAF event not recorded"
DBSITE="$(site_id db.test)"
task PATCH "/sites/$DBSITE" '{"waf_off":true}'
code="$(curl -s -o /dev/null -w '%{http_code}' --resolve db.test:80:127.0.0.1 "http://db.test/index.php?id=1%27%20OR%20%271%27=%271")"
[[ "$code" == 200 ]] || fail "per-site WAF off ignored: $code"
task PATCH "/sites/$DBSITE" '{"waf_off":false}'
# The CRS rule list, the merged blocked log, and switching the rules that fired off for every site.
api GET /waf/crs | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['available'], d; c=[x for x in d['categories'] if x['code']=='942'][0]; assert any(r['level']>1 for r in c['rules']) and any(r['hits']>0 for r in c['rules']), c['rules'][:3]" || fail "CRS rule list: $(api GET /waf/crs | head -c 300)"
api GET '/security/events?source=waf&site=db.test' | python3 -c "import sys,json; e=json.load(sys.stdin)['items']; assert e and e[0]['source']=='waf' and e[0]['site']=='db.test' and e[0]['rule'].startswith('9'), e[:1]" || fail "WAF event not in the merged log"
FIRED="$(api GET '/waf/events?lines=50' | python3 -c "import sys,json; e=[x for x in json.load(sys.stdin)['items'] if x['blocked']][0]; print(json.dumps([m['id'] for m in e['messages']]))")"
task PUT /waf/crs/disabled "{\"ids\":$FIRED}"
grep -q "SecRuleRemoveById" /opt/lmcwhm/web/nginx/nginx.conf || fail "disabled rules not in nginx.conf"
code="$(curl -s -o /dev/null -w '%{http_code}' --resolve db.test:80:127.0.0.1 "http://db.test/index.php?id=1%27%20OR%20%271%27=%271")"
[[ "$code" == 200 ]] || fail "rules switched off for every site still block: $code (ids $FIRED)"
task PUT /waf/crs/disabled '{"ids":[]}'
task PUT /waf '{"enabled":false,"engine":"On","paranoia":1,"anomaly":5,"disabled":[]}'
[[ "$(docker inspect -f '{{.Config.Image}}' lmcwhm-nginx)" == nginx:stable ]] || fail "nginx not switched back"
[[ "$(get80 db.test /index.php)" == "db-ok-42" ]] || fail "site down after WAF off"
pass "WAF: CRS blocks SQL injection, events, rule list, merged log, rules off for all sites, per-site opt-out, switch back"

# CrowdSec: decisions are enforced in the firewall's ban sets.
task POST /crowdsec/install '{"community":false}'
api GET /crowdsec | grep -q '"state":"running"' || fail "crowdsec not running"
[[ "$(api POST /crowdsec/decisions '{"ip":"203.0.113.77","hours":1}' | jget "['ok']")" == True ]] || fail "crowdsec ban"
api GET '/firewall/ips?list=block' | python3 -c "import sys,json; assert any(x['ip']=='203.0.113.77' and x['source']=='crowdsec' for x in json.load(sys.stdin)['items'])" || fail "crowdsec ban not in the firewall"
nft list set inet lmcwhm ban4 | grep -q 203.0.113.77 || fail "crowdsec ban not in nft"
[[ "$(api POST /crowdsec/decisions/delete '{"ip":"203.0.113.77"}' | jget "['ok']")" == True ]] || fail "crowdsec unban"
! nft list set inet lmcwhm ban4 | grep -q 203.0.113.77 || fail "crowdsec unban not applied"
api GET '/crowdsec/alerts?limit=5' | grep -q '"items"' || fail "crowdsec alerts"
pass "CrowdSec: local API, decisions mirrored into nftables"

# SSH rescue: the CLI turns the firewall off.
lmcwhm firewall-off >/dev/null
! nft list table inet lmcwhm >/dev/null 2>&1 || fail "firewall-off left the table"
api GET /firewall | grep -q '"enabled":false' || fail "firewall-off not saved"
pass "lmcwhm firewall-off"

# ---------- M6: backups ----------
bk_id() { api GET "/backups?kind=$1" | python3 -c "import sys,json; print([b['id'] for b in json.load(sys.stdin)['items'] if b['target']==sys.argv[1]][0])" "$2"; }
task POST /backups/run '{"kind":"site","target":"db.test","dests":["local"],"keep":2}'
SITEBK="$(bk_id site db.test)"
echo broken > /www/wwwroot/db.test/index.php
task POST "/backups/$SITEBK/restore" '{}'
[[ "$(get80 db.test /index.php)" == "db-ok-42" ]] || fail "site restore: $(get80 db.test /index.php)"
[[ "$(stat -c %u /www/wwwroot/db.test/index.php)" == 33 ]] || fail "restored files not owned by www-data"
SHOPID="$(api GET /databases | python3 -c "import sys,json; print([d['id'] for d in json.load(sys.stdin)['items'] if d['name']=='shop'][0])")"
task POST /backups/run "{\"kind\":\"database\",\"target\":\"$SHOPID\",\"dests\":[\"local\"],\"keep\":2}"
DBBK="$(bk_id database shop)"
docker exec -e MYSQL_PWD=Shop-pass-123 lmcwhm-mariadb114 mariadb -ushop -h127.0.0.1 shop -e "DELETE FROM t"
task POST "/backups/$DBBK/restore" '{}'
[[ "$(get80 db.test /index.php)" == "db-ok-42" ]] || fail "database restore: $(get80 db.test /index.php)"
[[ "$(curl -sk -b "$JAR" "$P/api/v1/backups/$DBBK/download" | head -c 2 | od -An -tx1 | tr -d ' ')" == 1f8b ]] || fail "download is not gzip"
docker volume create lmcwhm-e2e-vol >/dev/null
docker run --rm -v lmcwhm-e2e-vol:/v busybox sh -c 'echo vol-data > /v/f.txt'
task POST /backups/run '{"kind":"volume","target":"lmcwhm-e2e-vol","dests":["local"]}'
docker run --rm -v lmcwhm-e2e-vol:/v busybox rm /v/f.txt
task POST "/backups/$(bk_id volume lmcwhm-e2e-vol)/restore" '{}'
[[ "$(docker run --rm -v lmcwhm-e2e-vol:/v busybox cat /v/f.txt)" == vol-data ]] || fail "volume restore"
# A scheduled backup is a cron job of type "backup".
CJ="$(api POST /cron '{"name":"bk","type":"backup","schedule":"0 3 * * *","command":"{\"kind\":\"site\",\"target\":\"*\",\"dests\":[\"local\"],\"keep\":1}","enabled":true,"timeout":3600}' | jget "['id']")"
api POST "/cron/$CJ/run" >/dev/null
for _ in $(seq 1 60); do grep -q '"status":"success"' <<<"$(api GET "/cron/$CJ/runs")" && break; sleep 2; done
grep -q '"status":"success"' <<<"$(api GET "/cron/$CJ/runs")" || { api GET "/cron/$CJ/runs"; fail "backup cron job"; }
[[ "$(api GET '/backups?kind=site' | python3 -c "import sys,json; print(len([b for b in json.load(sys.stdin)['items'] if b['target']=='db.test']))")" == 1 ]] || fail "keep=1 did not prune"
grep -q "panel_" <<<"$(lmcwhm backup panel)" || fail "lmcwhm backup panel"
grep -q panel <<<"$(lmcwhm backup list)" || fail "lmcwhm backup list"
pass "backups: site, MariaDB, Docker volume and panel; restore; download; scheduled job with pruning"

# ---------- M7: MCP ----------
TOK="$(api POST /tokens '{"name":"ci","scope":"read","ips":[],"ttl_days":1}' | jget "['secret']")"
mcp() { curl -sk -X POST "$P/mcp" -H "Authorization: Bearer $1" -H 'Content-Type: application/json' -d "$2"; }
grep -q '"protocolVersion":"2025-06-18"' <<<"$(mcp "$TOK" '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"ci","version":"1"}}}')" || fail "mcp initialize"
grep -q '"analyze_access_log"' <<<"$(mcp "$TOK" '{"jsonrpc":"2.0","id":2,"method":"tools/list"}')" || fail "mcp tools/list"
for _ in 1 2 3; do get80 db.test /index.php >/dev/null; done
mcp "$TOK" '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"analyze_access_log","arguments":{"domain":"db.test"}}}' | python3 -c "import sys,json; r=json.load(sys.stdin)['result']; assert not r.get('isError') and r['structuredContent']['requests']>0, r" || fail "mcp analyze_access_log"
grep -q '"isError":true' <<<"$(mcp "$TOK" '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"run_shell","arguments":{"command":"id"}}}')" || fail "read token ran an admin tool"
[[ "$(curl -sk -o /dev/null -w '%{http_code}' -X POST "$P/mcp" -d '{}')" == 404 ]] || fail "/mcp without a token is not hidden"
out="$(printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"run_shell","arguments":{"command":"echo e2e-$((6*7))"}}}' | lmcwhm mcp --stdio)"
[[ "$out" == *e2e-42* ]] || fail "mcp --stdio: $out"
grep -q "name: lmcwhm-ops" <<<"$(curl -sk -b "$JAR" "$P/api/v1/mcp/skill")" || fail "skill download"
pass "MCP: tokens, scopes, tools over HTTPS and stdio, skill"

# ---------- M8 ----------
# Webhook receiver for alerts.
cat > /tmp/hook.py <<'EOF'
import http.server
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        b = self.rfile.read(int(self.headers.get('Content-Length', 0)))
        open('/tmp/hook.log', 'ab').write(b + b'\n')
        self.send_response(200); self.end_headers()
    def log_message(self, *a): pass
http.server.HTTPServer(('127.0.0.1', 18192), H).serve_forever()
EOF
python3 /tmp/hook.py & HOOK=$!
for _ in $(seq 1 30); do curl -s -o /dev/null -X POST -d x http://127.0.0.1:18192/ && break; sleep 0.3; done
: > /tmp/hook.log
CH="$(api POST /alerts/channels '{"name":"hook","kind":"webhook","enabled":true,"config":{"url":"http://127.0.0.1:18192/"}}' | jget "['id']")"
out="$(api POST "/alerts/channels/$CH/test")"
[[ "$(jget "['ok']" <<<"$out")" == True ]] || fail "alert channel test: $out"
grep -q "test notification" /tmp/hook.log || fail "test notification not delivered"
grep -q '"id"' <<<"$(api POST /alerts/rules "{\"name\":\"probes\",\"kind\":\"probe_down\",\"channels\":[$CH],\"enabled\":true}")" || fail "alert rule"
grep -q "db.test" /etc/hosts || echo "127.0.0.1 db.test" >> /etc/hosts
PR="$(api POST /probes '{"name":"db","type":"http","target":"http://db.test/index.php","keyword":"db-ok","interval":30,"fails":1,"enabled":true}' | jget "['id']")"
[[ "$(api POST "/probes/$PR/check" | jget "['ok']")" == True ]] || fail "http probe"
PD="$(api POST /probes '{"name":"closed","type":"tcp","target":"127.0.0.1:1","interval":30,"timeout":2,"fails":1,"enabled":true}' | jget "['id']")"
api POST "/probes/$PD/check" >/dev/null
for _ in $(seq 1 10); do grep -q "DOWN: closed" /tmp/hook.log && break; sleep 1; done
grep -q "DOWN: closed" /tmp/hook.log || fail "probe_down alert not delivered"
api POST "/probes/$PD/delete" >/dev/null
pass "alerts: webhook channel; uptime probes (HTTP keyword, TCP) raise probe_down"

# Process guardian: a systemd service that is restarted when it dies.
grep -q '"state":"active"' <<<"$(api POST /processes '{"name":"e2e-worker","command":"exec python3 -m http.server 18191 --bind 127.0.0.1","dir":"/tmp","user":"root","env":["A=1"]}')" || fail "process not active"
for _ in $(seq 1 20); do curl -s -o /dev/null http://127.0.0.1:18191/ && break; sleep 0.5; done
curl -s -o /dev/null http://127.0.0.1:18191/ || fail "guarded process not serving"
systemctl kill -s KILL lmcwhm-proc-e2e-worker.service
sleep 6
curl -s -o /dev/null http://127.0.0.1:18191/ || fail "guarded process not restarted"
PID="$(api GET /processes | python3 -c "import sys,json; print([p['id'] for p in json.load(sys.stdin)['items'] if p['name']=='e2e-worker'][0])")"
api POST "/processes/$PID/delete" >/dev/null
[[ ! -e /etc/systemd/system/lmcwhm-proc-e2e-worker.service ]] || fail "unit not removed"
pass "process guardian: systemd service restarted after a crash, removed on delete"

# Website advanced settings render into nginx.
task PATCH "/sites/$DBSITE" '{"advanced":{"deny_ua":["BadBot"],"redirects":[{"from":"/old","to":"/index.php","code":301}],"block_xmlrpc":true,"rate_limit":50,"rate_burst":100}}'
[[ "$(curl -s -o /dev/null -w '%{http_code}' -A 'BadBot/1.0' --resolve db.test:80:127.0.0.1 http://db.test/index.php)" == 403 ]] || fail "user agent not blocked"
[[ "$(curl -s -o /dev/null -w '%{http_code}' --resolve db.test:80:127.0.0.1 http://db.test/old)" == 301 ]] || fail "redirect"
[[ "$(curl -s -o /dev/null -w '%{http_code}' --resolve db.test:80:127.0.0.1 http://db.test/xmlrpc.php)" == 403 ]] || fail "xmlrpc not blocked"
[[ "$(get80 db.test /index.php)" == "db-ok-42" ]] || fail "site broken by advanced settings"
task PATCH "/sites/$DBSITE" '{"advanced":{}}'
grep -q '"total"' <<<"$(api GET "/sitestats?domain=db.test&days=7")" || fail "site report"
pass "website advanced settings: UA block, redirect, xmlrpc, rate limit"

# Composer in the PHP image.
echo '{"require":{}}' > /www/wwwroot/db.test/composer.json
chown 33:33 /www/wwwroot/db.test/composer.json
task POST "/sites/$DBSITE/composer" '{"action":"install"}'
[[ -f /www/wwwroot/db.test/vendor/autoload.php ]] || fail "composer install"
pass "Composer"

# WordPress toolkit.
task POST /sites '{"domain":"wp.test","type":"php","php_version":"8.3","ssl":"none"}'
WPSITE="$(site_id wp.test)"
task POST "/wordpress/install/$WPSITE" "{\"title\":\"CI Blog\",\"admin_user\":\"wpadmin\",\"admin_pass\":\"Wp-pass-12345\",\"admin_email\":\"ci@example.com\",\"locale\":\"en_US\",\"db_app_id\":$MID}"
get80_retry wp.test / "CI Blog" || fail "WordPress front page"
api GET "/wordpress/$WPSITE" | python3 -c "import sys,json; i=json.load(sys.stdin); assert i['version'] and 'wpadmin' in i['admins'], i" || fail "WordPress info"
task POST "/wordpress/$WPSITE/action" '{"action":"verify"}'
pass "WordPress: install with database and admin, WP-CLI info and checksum verification"

# SFTP (chrooted) and FTP accounts.
command -v sshpass >/dev/null || DEBIAN_FRONTEND=noninteractive apt-get install -y -qq openssh-server sshpass >/dev/null
systemctl start ssh 2>/dev/null || systemctl start sshd
grep -q '"id"' <<<"$(api POST /ftp/accounts '{"kind":"sftp","username":"e2esftp","password":"Sftp-pass-123","site_id":'"$DBSITE"'}')" || fail "sftp account"
out="$(printf 'ls /site\n' | sshpass -p Sftp-pass-123 sftp -oStrictHostKeyChecking=no -oUserKnownHostsFile=/dev/null -oBatchMode=no e2esftp@127.0.0.1 2>&1)"
[[ "$out" == *index.php* ]] || fail "sftp listing: $out"
! grep -q uid <<<"$(sshpass -p Sftp-pass-123 ssh -oStrictHostKeyChecking=no -oUserKnownHostsFile=/dev/null e2esftp@127.0.0.1 id 2>/dev/null)" || fail "sftp user got a shell"
task POST /ftp/enabled '{"on":true}'
grep -q '"id"' <<<"$(api POST /ftp/accounts '{"kind":"ftp","username":"e2eftp","password":"Ftp-pass-1234","site_id":'"$DBSITE"'}')" || fail "ftp account"
grep -q index.php <<<"$(curl -s --user e2eftp:Ftp-pass-1234 ftp://127.0.0.1/)" || fail "ftp listing"
grep -q 'FTP (lmcwhm)' <<<"$(api GET /firewall)" || fail "ftp firewall rule"
task POST /ftp/enabled '{"on":false}'
for a in $(api GET /ftp | python3 -c "import sys,json; print(' '.join(str(x['id']) for x in json.load(sys.stdin)['items']))"); do api POST "/ftp/accounts/$a/delete" >/dev/null; done
! id e2esftp >/dev/null 2>&1 || fail "sftp system user not removed"
pass "SFTP chroot and Pure-FTPd accounts"


# ---------- M9 / M10 ----------
# PHP extensions, php.ini and FPM pool per version.
task PUT /php/8.3 '{"extensions":["redis"],"ini":{"memory_limit":"384M"},"disable_functions":["exec"],"custom_ini":"","fpm":{"pm":"dynamic","max_children":8,"start_servers":2,"min_spare":1,"max_spare":4,"max_requests":500,"request_timeout":0,"slowlog_timeout":5}}'
docker exec lmcwhm-php83 php -m | grep -qx redis || fail "redis extension not loaded"
[[ "$(docker exec lmcwhm-php83 php -r 'echo ini_get("memory_limit");')" == 384M ]] || fail "php.ini not applied"
api GET /php/8.3 | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['status']['pool']=='www' and 'redis' in d['modules'], d" || fail "php status"
[[ "$(get80 db.test /index.php)" == "db-ok-42" ]] || fail "site broken by PHP settings"
api GET /php | python3 -c "import sys,json; v=[v for v in json.load(sys.stdin)['items'] if v['version']=='8.3'][0]; assert v['full_version']=='8.3.0', v" \
  || fail "php full version"
api POST /apps/updates/check | python3 -c "import sys,json; u=json.load(sys.stdin)['items']['php:8.3']; assert u['available'] and u['current']=='8.3.0', u" \
  || fail "update check did not find the PHP patch"
task POST /php/8.3/update
api GET /php | python3 -c "import sys,json; v=[v for v in json.load(sys.stdin)['items'] if v['version']=='8.3'][0]; assert v['full_version'].startswith('8.3.') and v['full_version']!='8.3.0' and v['state']=='running', v" \
  || fail "php not updated"
docker exec lmcwhm-php83 php -m | grep -qx redis || fail "redis extension lost in the PHP update"
[[ "$(docker exec lmcwhm-php83 php -r 'echo ini_get("memory_limit");')" == 384M ]] || fail "php.ini lost in the PHP update"
task POST /php/8.3/update # already the newest: nothing to do
pass "PHP: redis extension built, php.ini and FPM pool applied, FPM status read, patch update rebuilt with extensions and settings kept"

# Varnish page cache for one site.
task PATCH "/sites/$DBSITE" '{"advanced":{"varnish":true,"varnish_ttl":300}}'
curl -s -o /dev/null --resolve db.test:80:127.0.0.1 http://db.test/index.php
hit="$(curl -s -D - -o /dev/null --resolve db.test:80:127.0.0.1 http://db.test/index.php | tr -d '\r' | grep -i '^x-cache:' || true)"
[[ "$hit" == *HIT* ]] || fail "varnish did not cache: $hit"
api GET /varnish | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['stats']['state']=='running' and d['stats']['hits']>=1, d" || fail "varnish stats"
api POST /varnish/purge "{\"site_id\":$DBSITE}" >/dev/null
task PATCH "/sites/$DBSITE" '{"advanced":{}}'
for _ in $(seq 1 10); do [[ -z "$(docker ps -aq -f name=lmcwhm-varnish)" ]] && break; sleep 1; done
[[ -z "$(docker ps -aq -f name=lmcwhm-varnish)" ]] || fail "varnish not removed when unused"
pass "Varnish: second request is a cache HIT, removed when no site uses it"

# Toolbox.
api GET /toolbox/system | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['hostname'] and d['mem_total']>0, d" || fail "toolbox system"
api GET /toolbox/processes | python3 -c "import sys,json; assert any(p['name']=='lmcwhm' for p in json.load(sys.stdin)['items'])" || fail "process list"
grep -q LISTEN <<<"$(api GET /toolbox/connections)" || fail "connections"
api GET /toolbox/ssh | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['port']>0, d" || fail "ssh settings"
grep -q system_logs <<<"$(api GET /toolbox/junk)" || fail "junk scan"
pass "toolbox: system, processes, connections, SSH, cleanup scan"

# Security scan, webshell scan and quarantine.
task POST /secscan/run
api GET /secscan | python3 -c "import sys,json; r=json.load(sys.stdin)['items'][0]; assert 0<=r['score']<=100 and r['hostname'], r" || fail "security scan"
printf '<?php @eval(base64_decode($_POST["x"]));' > /www/wwwroot/db.test/shell.php
task POST /malware/scan '{"paths":["/www/wwwroot/db.test"]}'
SCAN="$(api GET /malware/scans | jget "['items'][0]['id']")"
api GET "/malware/scans/$SCAN" | python3 -c "import sys,json; f=json.load(sys.stdin)['findings']; assert [x for x in f if x['path'].endswith('/shell.php') and x['rule']=='eval_decode'], f" || fail "webshell not found"
api POST "/malware/scans/$SCAN/quarantine" '{"path":"/www/wwwroot/db.test/shell.php"}' >/dev/null
[[ ! -e /www/wwwroot/db.test/shell.php ]] || fail "not quarantined"
pass "security scan report; webshell detected and quarantined"

# Tamper protection: watch + restore, then lock.
api POST /tamper '{"path":"/www/wwwroot/db.test","mode":"watch","restore":true,"enabled":true,"exclude":["uploads"]}' | grep -q '"watching"' || fail "tamper watch"
echo '<?php echo "hacked";' > /www/wwwroot/db.test/index.php
for _ in $(seq 1 20); do [[ "$(cat /www/wwwroot/db.test/index.php)" != *hacked* ]] && break; sleep 0.5; done
[[ "$(get80 db.test /index.php)" == "db-ok-42" ]] || fail "tampered file not restored"
TP="$(api GET /tamper | jget "['items'][0]['id']")"
grep -q restored <<<"$(api GET /tamper/events)" || fail "tamper event"
api PUT "/tamper/$TP" '{"path":"/www/wwwroot/db.test","mode":"lock","enabled":true}' | grep -q '"locked"' || fail "tamper lock"
! (echo x >> /www/wwwroot/db.test/index.php) 2>/dev/null || fail "locked file was writable"
api POST "/tamper/$TP/delete" >/dev/null
echo -n >> /www/wwwroot/db.test/index.php || fail "file still locked after the rule was deleted"
pass "tamper protection: changes undone in watch mode, immutable in lock mode"

# Sub-user with module permissions; REST API token; OpenAPI.
api POST /users '{"username":"ops","password":"Ops-pass-123","perms":["files"],"enabled":true}' | grep -q '"ops"' || fail "sub-user"
J2="$(mktemp)"
curl -sk -c "$J2" -o /dev/null "$P/console"
curl -sk -b "$J2" -c "$J2" -o /dev/null -X POST "$P/api/v1/auth/login" -d '{"username":"ops","password":"Ops-pass-123"}'
[[ "$(curl -sk -b "$J2" -o /dev/null -w '%{http_code}' "$P/api/v1/settings")" == 403 ]] || fail "sub-user reached settings"
[[ "$(curl -sk -b "$J2" -o /dev/null -w '%{http_code}' "$P/api/v1/files/list?path=/www")" == 200 ]] || fail "sub-user files"
RT="$(api POST /tokens '{"name":"rest","scope":"read","ips":[],"ttl_days":1}' | jget "['secret']")"
grep -q db.test <<<"$(curl -sk -H "Authorization: Bearer $RT" "$P/api/v1/sites")" || fail "REST token GET"
[[ "$(curl -sk -o /dev/null -w '%{http_code}' -X POST -H "Authorization: Bearer $RT" "$P/api/v1/sites")" == 404 ]] || fail "read token wrote"
grep -q '"openapi"' <<<"$(curl -sk -H "Authorization: Bearer $RT" "$P/api/v1/openapi.json")" || fail "openapi"
pass "sub-user permissions, REST API token scopes, OpenAPI description"

# File search, share link, remote download.
grep -q index.php <<<"$(api GET '/files/search?dir=/www/wwwroot/db.test&q=db-ok&content=1')" || fail "content search"
SH="$(api POST /files/shares '{"path":"/www/wwwroot/db.test/index.php","hours":1}' | jget "['token']")"
out="$(curl -sk -X POST "$P/s/$SH")"
grep -q 'new mysqli' <<<"$out" || fail "share link download (without the security entry): ${out:0:200}"
task POST /files/fetch '{"url":"https://raw.githubusercontent.com/wilsonstar90/lmcwhm/main/README.md","dir":"/tmp","name":"fetched.md"}'
grep -q lmcwhm /tmp/fetched.md || fail "remote download"
pass "file search, share link, remote download"

# DNS templates (preview needs no Cloudflare call); panel snapshot.
api POST /dns/zones/none/template '{"template":"self_mail","domain":"e2e.test","params":{"mailhost":"mail.e2e.test"},"preview":true}' | grep -q 'mail.e2e.test' || fail "dns template"
task POST /backups/run '{"kind":"snapshot","dests":["local"],"keep":1}'
SNAP="$(api GET '/backups?kind=snapshot' | jget "['items'][0]['id']")"
SS="$(api POST "/backups/$SNAP/share" | jget "['token']")"
curl -sk -X POST "$P/s/$SS" -o /tmp/snap.tar.gz
LIST="$(tar -tzf /tmp/snap.tar.gz)" || fail "snapshot is not a tar.gz: $(head -c 200 /tmp/snap.tar.gz)"
grep -qx 'home/data/panel.db' <<<"$LIST" || fail "snapshot lacks the panel database"
grep -qx 'www/db.test/index.php' <<<"$LIST" || fail "snapshot lacks website files"
grep -qx 'snapshot.json' <<<"$(head -1 <<<"$LIST")" || fail "snapshot manifest not first"
pass "DNS template preview; snapshot with panel, apps and websites, downloadable for migration"

# Manifest for the installable app (PWA).
grep -q '"start_url":"/console"' <<<"$(curl -sk -b "$JAR" "$P/manifest.webmanifest")" || fail "PWA manifest"
pass "PWA manifest"

api GET /system/summary | python3 -c "import sys,json; s=json.load(sys.stdin); assert 0 <= s['score'] <= 100 and s['backups'] > 0 and s['probes'] >= 1, s" || fail "dashboard summary"
kill $HOOK
pass "dashboard summary"

# ---------- panel restart keeps projects and services ----------
systemctl restart lmcwhm
for _ in $(seq 1 30); do curl -sk -o /dev/null "$P/" && break; sleep 1; done
get80_retry node.test / "node-v2" || fail "project down after panel restart"
[[ "$(docker inspect -f '{{.State.Status}}' lmcwhm-mariadb114)" == running ]] || fail "mariadb down after panel restart"
pass "panel restart keeps projects and services running"

echo "all app tests passed"
