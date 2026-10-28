package appstore

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

// Engine settings windows (aaPanel 軟件設定): status, performance tuning,
// port, logs, binary logs and persistence for MySQL / MariaDB and Redis.

var (
	ErrBadPort    = errors.New("app.bad_port")
	ErrPortInUse  = errors.New("app.port_in_use")
	ErrWrongApp   = errors.New("app.wrong_engine")
	ErrBadSetting = errors.New("app.bad_setting")
)

func isMySQL(app string) bool { return app == "mysql" || app == "mariadb" }

func (sv *Service) engineOf(ctx context.Context, id int64, ok func(string) bool) (Installed, error) {
	i, err := sv.service(ctx, id)
	if err != nil {
		return i, err
	}
	if !ok(i.App) {
		return i, ErrWrongApp
	}
	return i, nil
}

// ---- config-file editing ----

// setCnf sets (or with value "" removes) key in the [mysqld] section of a
// my.cnf body; a bare flag is written for value "-".
func setCnf(body, key, value string) string {
	norm := func(s string) string { return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "-", "_") }
	var lines []string
	if strings.TrimSpace(body) != "" {
		lines = strings.Split(strings.TrimRight(body, "\n"), "\n")
	}
	section, insertAt, done := "", -1, false
	var out []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			if section == "mysqld" && insertAt < 0 {
				insertAt = len(out)
			}
			section = strings.Trim(t, "[]")
		}
		if section == "mysqld" && !strings.HasPrefix(t, "[") {
			name := strings.TrimLeft(t, "#; ")
			if i := strings.IndexAny(name, "= \t"); i >= 0 {
				name = name[:i]
			}
			if norm(name) == norm(key) && (!strings.HasPrefix(t, "#") || strings.Contains(t, "=")) {
				if done || value == "" {
					if !strings.HasPrefix(t, "#") {
						continue // drop the line
					}
					out = append(out, l)
					continue
				}
				if value == "-" {
					out = append(out, key)
				} else {
					out = append(out, key+" = "+value)
				}
				done = true
				continue
			}
		}
		out = append(out, l)
	}
	if !done && value != "" {
		line := key + " = " + value
		if value == "-" {
			line = key
		}
		switch {
		case section == "mysqld" && insertAt < 0:
			out = append(out, line)
		case insertAt >= 0:
			out = append(out[:insertAt], append([]string{line}, out[insertAt:]...)...)
		default:
			out = append(out, "[mysqld]", line)
		}
	}
	return strings.Join(out, "\n") + "\n"
}

// setRedisConf sets (value "" removes) a directive; "save" takes several
// values, one per line.
func setRedisConf(body, key string, values ...string) string {
	var out []string
	inserted := false
	for _, l := range strings.Split(strings.TrimRight(body, "\n"), "\n") {
		f := strings.Fields(l)
		if len(f) > 0 && strings.EqualFold(f[0], key) {
			if !inserted {
				for _, v := range values {
					if v != "" {
						out = append(out, key+" "+v)
					}
				}
				inserted = true
			}
			continue
		}
		out = append(out, l)
	}
	if !inserted {
		for _, v := range values {
			if v != "" {
				out = append(out, key+" "+v)
			}
		}
	}
	return strings.Join(out, "\n") + "\n"
}

func redisConfValues(body, key string) []string {
	var out []string
	for _, l := range strings.Split(body, "\n") {
		f := strings.Fields(l)
		if len(f) > 1 && strings.EqualFold(f[0], key) {
			out = append(out, strings.Join(f[1:], " "))
		}
	}
	return out
}

func (sv *Service) confPath(i Installed) string {
	return filepath.Join(sv.dir(i.App, i.Version), configFile(i.App))
}

// ---- MySQL / MariaDB ----

func (sv *Service) mysqlKV(ctx context.Context, i Installed, q string) (map[string]string, error) {
	out, err := sv.Query(ctx, i, q)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(l, "\t"); ok {
			m[strings.ToLower(k)] = v
		}
	}
	return m, nil
}

type MySQLStatus struct {
	Version  string            `json:"version"`
	Uptime   int64             `json:"uptime"`
	Status   map[string]string `json:"status"`
	Vars     map[string]string `json:"vars"`
	BinFile  string            `json:"bin_file"`
	BinPos   string            `json:"bin_pos"`
	DataSize int64             `json:"data_size"`
}

// statusKeys and varKeys are what the status and tuning views use.
var statusKeys = []string{"uptime", "connections", "bytes_sent", "bytes_received", "questions", "com_commit", "com_rollback",
	"threads_connected", "threads_created", "max_used_connections", "key_reads", "key_read_requests",
	"innodb_buffer_pool_reads", "innodb_buffer_pool_read_requests", "qcache_hits", "qcache_inserts", "qcache_not_cached",
	"created_tmp_disk_tables", "created_tmp_tables", "open_tables", "opened_tables", "select_full_join", "select_range_check",
	"sort_merge_passes", "table_locks_waited", "slow_queries", "aborted_clients", "aborted_connects"}

var TuneKeys = []string{"key_buffer_size", "query_cache_size", "tmp_table_size", "innodb_buffer_pool_size", "innodb_log_buffer_size",
	"max_connections", "sort_buffer_size", "read_buffer_size", "read_rnd_buffer_size", "join_buffer_size", "thread_stack",
	"binlog_cache_size", "thread_cache_size", "table_open_cache"}

var extraVars = []string{"version", "port", "log_bin", "slow_query_log", "long_query_time", "slow_query_log_file",
	"binlog_expire_logs_seconds", "have_query_cache", "query_cache_type", "max_heap_table_size"}

func (sv *Service) MySQLStatus(ctx context.Context, id int64) (MySQLStatus, error) {
	i, err := sv.engineOf(ctx, id, isMySQL)
	if err != nil {
		return MySQLStatus{}, err
	}
	all, err := sv.mysqlKV(ctx, i, "SHOW GLOBAL STATUS;")
	if err != nil {
		return MySQLStatus{}, err
	}
	vars, err := sv.mysqlKV(ctx, i, "SHOW GLOBAL VARIABLES;")
	if err != nil {
		return MySQLStatus{}, err
	}
	st := MySQLStatus{Status: map[string]string{}, Vars: map[string]string{}}
	for _, k := range statusKeys {
		st.Status[k] = all[k]
	}
	for _, k := range append(append([]string{}, TuneKeys...), extraVars...) {
		st.Vars[k] = vars[k]
	}
	st.Version = vars["version"]
	st.Uptime, _ = strconv.ParseInt(all["uptime"], 10, 64)
	// SHOW MASTER STATUS is gone in MySQL 8.4.
	for _, q := range []string{"SHOW BINARY LOG STATUS;", "SHOW MASTER STATUS;"} {
		if out, err := sv.Query(ctx, i, q); err == nil {
			if f := strings.Split(strings.TrimSpace(out), "\t"); len(f) >= 2 {
				st.BinFile, st.BinPos = f[0], f[1]
			}
			break
		}
	}
	st.DataSize = dirSize(filepath.Join(sv.dir(i.App, i.Version), "data"))
	return st, nil
}

func dirSize(dir string) int64 {
	var n int64
	filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			n += fi.Size()
		}
		return nil
	})
	return n
}

var cnfValueRe = regexp.MustCompile(`^[0-9]+[KMG]?$`)

// SaveMySQLTuning writes the tuning values (bytes or counts, "" = engine
// default) and restarts with rollback (task).
func (sv *Service) SaveMySQLTuning(ctx context.Context, id int64, values map[string]string) (string, error) {
	i, err := sv.engineOf(ctx, id, isMySQL)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(sv.confPath(i))
	if err != nil {
		return "", err
	}
	body := string(b)
	allowed := map[string]bool{}
	for _, k := range TuneKeys {
		allowed[k] = true
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := strings.ToUpper(strings.TrimSpace(values[k]))
		if !allowed[k] || (v != "" && !cnfValueRe.MatchString(v)) {
			return "", fmt.Errorf("%w: %s", ErrBadSetting, k)
		}
		if k == "query_cache_size" && i.App == "mysql" {
			continue // removed in MySQL 8
		}
		body = setCnf(body, k, v)
		if k == "tmp_table_size" {
			body = setCnf(body, "max_heap_table_size", v) // the smaller of the two applies
		}
	}
	return sv.SaveConfig(ctx, id, body)
}

// SetSlowLog turns the slow query log on or off, live and in the config.
func (sv *Service) SetSlowLog(ctx context.Context, id int64, on bool, seconds float64) error {
	i, err := sv.engineOf(ctx, id, isMySQL)
	if err != nil {
		return err
	}
	if seconds <= 0 || seconds > 3600 {
		seconds = 3
	}
	sec := strconv.FormatFloat(seconds, 'f', -1, 64)
	q := fmt.Sprintf("SET GLOBAL slow_query_log_file = '/var/lib/mysql/lmcwhm-slow.log'; SET GLOBAL long_query_time = %s; SET GLOBAL slow_query_log = %d;", sec, map[bool]int{true: 1, false: 0}[on])
	if _, err := sv.Query(ctx, i, q); err != nil {
		return err
	}
	b, err := os.ReadFile(sv.confPath(i))
	if err != nil {
		return err
	}
	body := setCnf(string(b), "slow_query_log", map[bool]string{true: "1", false: "0"}[on])
	body = setCnf(body, "long_query_time", sec)
	body = setCnf(body, "slow_query_log_file", "/var/lib/mysql/lmcwhm-slow.log")
	return os.WriteFile(sv.confPath(i), []byte(body), 0o644)
}

// tailFile returns up to max bytes from the end of path, starting at a line.
func tailFile(path string, max int64) (string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	off := fi.Size() - max
	if off < 0 {
		off = 0
	}
	f.Seek(off, io.SeekStart)
	b, err := io.ReadAll(f)
	if off > 0 {
		if i := strings.IndexByte(string(b), '\n'); i >= 0 {
			b = b[i+1:]
		}
	}
	return string(b), err
}

func (sv *Service) SlowLog(ctx context.Context, id int64) (string, error) {
	i, err := sv.engineOf(ctx, id, isMySQL)
	if err != nil {
		return "", err
	}
	return tailFile(filepath.Join(sv.dir(i.App, i.Version), "data", "lmcwhm-slow.log"), 256<<10)
}

// ClearSlowLog empties the slow log file in place (the server keeps it open).
func (sv *Service) ClearSlowLog(ctx context.Context, id int64) error {
	i, err := sv.engineOf(ctx, id, isMySQL)
	if err != nil {
		return err
	}
	err = os.Truncate(filepath.Join(sv.dir(i.App, i.Version), "data", "lmcwhm-slow.log"), 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

type Binlog struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}

type Binlogs struct {
	Enabled       bool     `json:"enabled"`
	ExpireSeconds int64    `json:"expire_seconds"`
	Total         int64    `json:"total"`
	Files         []Binlog `json:"files"`
}

func (sv *Service) Binlogs(ctx context.Context, id int64) (Binlogs, error) {
	i, err := sv.engineOf(ctx, id, isMySQL)
	if err != nil {
		return Binlogs{}, err
	}
	vars, err := sv.mysqlKV(ctx, i, "SHOW GLOBAL VARIABLES WHERE Variable_name IN ('log_bin', 'binlog_expire_logs_seconds');")
	if err != nil {
		return Binlogs{}, err
	}
	out := Binlogs{Enabled: strings.EqualFold(vars["log_bin"], "ON"), Files: []Binlog{}}
	out.ExpireSeconds, _ = strconv.ParseInt(vars["binlog_expire_logs_seconds"], 10, 64)
	if !out.Enabled {
		return out, nil
	}
	list, err := sv.Query(ctx, i, "SHOW BINARY LOGS;")
	if err != nil {
		return out, err
	}
	data := filepath.Join(sv.dir(i.App, i.Version), "data")
	for _, l := range strings.Split(strings.TrimSpace(list), "\n") {
		f := strings.Split(l, "\t")
		if len(f) < 2 {
			continue
		}
		b := Binlog{Name: f[0]}
		b.Size, _ = strconv.ParseInt(f[1], 10, 64)
		if fi, err := os.Stat(filepath.Join(data, f[0])); err == nil {
			b.Modified = fi.ModTime().Unix()
		}
		out.Total += b.Size
		out.Files = append(out.Files, b)
	}
	return out, nil
}

var binlogNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// PurgeBinlogs deletes every binary log before name (the server only purges in order).
func (sv *Service) PurgeBinlogs(ctx context.Context, id int64, before string) error {
	i, err := sv.engineOf(ctx, id, isMySQL)
	if err != nil {
		return err
	}
	if !binlogNameRe.MatchString(before) {
		return ErrBadSetting
	}
	_, err = sv.Query(ctx, i, fmt.Sprintf("PURGE BINARY LOGS TO '%s';", before))
	return err
}

// SetBinlog turns binary logging on or off (restart, task); expireDays > 0
// also sets how long the logs are kept.
func (sv *Service) SetBinlog(ctx context.Context, id int64, on bool, expireDays int) (string, error) {
	i, err := sv.engineOf(ctx, id, isMySQL)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(sv.confPath(i))
	if err != nil {
		return "", err
	}
	body := string(b)
	if on {
		body = setCnf(body, "skip-log-bin", "")
		body = setCnf(body, "log_bin", "mysql-bin")
		body = setCnf(body, "server_id", "1")
	} else {
		body = setCnf(body, "log_bin", "")
		if i.App == "mysql" {
			body = setCnf(body, "skip-log-bin", "-") // on by default in MySQL 8
		}
	}
	if expireDays > 0 && expireDays <= 365 {
		body = setCnf(body, "binlog_expire_logs_seconds", strconv.Itoa(expireDays*86400))
	}
	return sv.SaveConfig(ctx, id, body)
}

// ---- port and memory protection (container recreated) ----

func portFree(port int) bool {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// SetPort moves a service to another loopback port (task). Websites that
// connect to the old port have to be updated.
func (sv *Service) SetPort(ctx context.Context, id int64, port int) (string, error) {
	i, err := sv.service(ctx, id)
	if err != nil {
		return "", err
	}
	if port < 1024 || port > 65535 {
		return "", ErrBadPort
	}
	if port == i.Port {
		return "", nil
	}
	if sv.UsedPorts(ctx)[port] || !portFree(port) {
		return "", ErrPortInUse
	}
	return sv.tasks.Start("app.port:"+i.App+"-"+i.Version, func(ctx context.Context, log tasks.Logf) error {
		old := i.Port
		if _, err := sv.st.DB.ExecContext(ctx, `UPDATE apps SET port = ? WHERE id = ?`, port, i.ID); err != nil {
			return err
		}
		i.Port = port
		if err := sv.recreate(ctx, i, log); err != nil {
			log("reverting to port %d", old)
			sv.st.DB.ExecContext(ctx, `UPDATE apps SET port = ? WHERE id = ?`, old, i.ID)
			i.Port = old
			sv.recreate(ctx, i, log)
			return err
		}
		sv.RefreshPMA(ctx)
		log("%s %s now listens on 127.0.0.1:%d", i.Name, i.Version, port)
		return nil
	}), nil
}

func (sv *Service) recreate(ctx context.Context, i Installed, log tasks.Logf) error {
	if i.App == "redis" {
		// The config names the published port in its header comment only.
		sv.prepareRedisComment(i)
	}
	if err := webenv.EnsureContainer(ctx, log, i.Container, sv.args(i), nil, true); err != nil {
		return err
	}
	return sv.waitReady(ctx, i)
}

func (sv *Service) prepareRedisComment(i Installed) {
	p := sv.confPath(i)
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	re := regexp.MustCompile(`(?m)^# Generated by lmcwhm; the port is published on 127\.0\.0\.1:\d+\.$`)
	os.WriteFile(p, re.ReplaceAll(b, []byte(fmt.Sprintf("# Generated by lmcwhm; the port is published on 127.0.0.1:%d.", i.Port))), 0o644)
}

// SetOOMProtect lowers the kernel's OOM score of the service so it is the
// last process killed when memory runs out (container recreated, task).
func (sv *Service) SetOOMProtect(ctx context.Context, id int64, on bool) (string, error) {
	i, err := sv.service(ctx, id)
	if err != nil {
		return "", err
	}
	return sv.tasks.Start("app.oom:"+i.App+"-"+i.Version, func(ctx context.Context, log tasks.Logf) error {
		if _, err := sv.st.DB.ExecContext(ctx, `UPDATE apps SET oom_protect = ? WHERE id = ?`, on, i.ID); err != nil {
			return err
		}
		i.OOMProtect = on
		return sv.recreate(ctx, i, log)
	}), nil
}

// ---- Redis ----

type RedisSettings struct {
	Port        int      `json:"port"`
	Timeout     string   `json:"timeout"`
	MaxClients  string   `json:"maxclients"`
	Databases   string   `json:"databases"`
	MaxMemory   string   `json:"maxmemory"` // bytes or with unit, "0" = unlimited
	Policy      string   `json:"maxmemory_policy"`
	AppendOnly  string   `json:"appendonly"`
	AppendFsync string   `json:"appendfsync"`
	Save        []string `json:"save"` // "900 1", ...
	Password    string   `json:"password"`
}

func (sv *Service) RedisSettings(ctx context.Context, id int64) (RedisSettings, error) {
	i, err := sv.engineOf(ctx, id, func(a string) bool { return a == "redis" })
	if err != nil {
		return RedisSettings{}, err
	}
	b, err := os.ReadFile(sv.confPath(i))
	if err != nil {
		return RedisSettings{}, err
	}
	body := string(b)
	first := func(k, def string) string {
		if v := redisConfValues(body, k); len(v) > 0 {
			return v[len(v)-1]
		}
		return def
	}
	s := RedisSettings{Port: i.Port, Password: i.secret,
		Timeout: first("timeout", "0"), MaxClients: first("maxclients", "10000"), Databases: first("databases", "16"),
		MaxMemory: first("maxmemory", "0"), Policy: first("maxmemory-policy", "noeviction"),
		AppendOnly: first("appendonly", "no"), AppendFsync: first("appendfsync", "everysec"), Save: redisConfValues(body, "save")}
	if s.Save == nil {
		// Redis 7 defaults when the config names none.
		s.Save = []string{"3600 1", "300 100", "60 10000"}
	}
	return s, nil
}

var (
	redisNumRe    = regexp.MustCompile(`^[0-9]+$`)
	redisMemRe    = regexp.MustCompile(`^[0-9]+(?i:[kmg]b?)?$`)
	redisSaveRe   = regexp.MustCompile(`^[0-9]+ [0-9]+$`)
	redisPolicies = map[string]bool{"noeviction": true, "allkeys-lru": true, "allkeys-lfu": true, "allkeys-random": true,
		"volatile-lru": true, "volatile-lfu": true, "volatile-random": true, "volatile-ttl": true}
)

// SaveRedisSettings writes the directives and applies them live with CONFIG
// SET (no restart, the data stays in memory); a changed database count needs
// a restart, which runs as a task with rollback.
func (sv *Service) SaveRedisSettings(ctx context.Context, id int64, s RedisSettings) (string, error) {
	i, err := sv.engineOf(ctx, id, func(a string) bool { return a == "redis" })
	if err != nil {
		return "", err
	}
	cur, err := sv.RedisSettings(ctx, id)
	if err != nil {
		return "", err
	}
	for _, v := range []string{s.Timeout, s.MaxClients, s.Databases} {
		if !redisNumRe.MatchString(v) {
			return "", ErrBadSetting
		}
	}
	if n, _ := strconv.Atoi(s.Databases); n < 1 || n > 256 {
		return "", ErrBadSetting
	}
	if !redisMemRe.MatchString(s.MaxMemory) || !redisPolicies[s.Policy] ||
		(s.AppendOnly != "yes" && s.AppendOnly != "no") || (s.AppendFsync != "always" && s.AppendFsync != "everysec" && s.AppendFsync != "no") {
		return "", ErrBadSetting
	}
	for _, r := range s.Save {
		if !redisSaveRe.MatchString(r) {
			return "", ErrBadSetting
		}
	}
	b, err := os.ReadFile(sv.confPath(i))
	if err != nil {
		return "", err
	}
	body := string(b)
	set := [][2]string{{"timeout", s.Timeout}, {"maxclients", s.MaxClients}, {"databases", s.Databases}, {"maxmemory", strings.ToLower(s.MaxMemory)},
		{"maxmemory-policy", s.Policy}, {"appendonly", s.AppendOnly}, {"appendfsync", s.AppendFsync}}
	for _, kv := range set {
		body = setRedisConf(body, kv[0], kv[1])
	}
	save := s.Save
	if len(save) == 0 {
		save = []string{`""`} // persistence snapshots off
	}
	body = setRedisConf(body, "save", save...)
	if s.Databases != cur.Databases {
		return sv.SaveConfig(ctx, id, body)
	}
	if err := os.WriteFile(sv.confPath(i), []byte(body), 0o644); err != nil {
		return "", err
	}
	rc, err := sv.Redis(ctx, i)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	for _, kv := range set {
		if kv[0] == "databases" {
			continue
		}
		if _, err := rc.Do("CONFIG", "SET", kv[0], kv[1]); err != nil {
			return "", fmt.Errorf("%s: %w", kv[0], err)
		}
	}
	saveArg := strings.Join(s.Save, " ")
	if _, err := rc.Do("CONFIG", "SET", "save", saveArg); err != nil {
		return "", fmt.Errorf("save: %w", err)
	}
	return "", nil
}

// RedisInfo returns INFO as fields (all sections).
func (sv *Service) RedisInfo(ctx context.Context, id int64) (map[string]string, error) {
	i, err := sv.engineOf(ctx, id, func(a string) bool { return a == "redis" })
	if err != nil {
		return nil, err
	}
	rc, err := sv.Redis(ctx, i)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	v, err := rc.Do("INFO", "everything")
	if err != nil {
		return nil, err
	}
	s, _ := v.(string)
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		if k, val, ok := strings.Cut(strings.TrimSpace(sc.Text()), ":"); ok && !strings.HasPrefix(k, "#") {
			out[k] = val
		}
	}
	return out, nil
}

// SaveNow triggers a background RDB snapshot.
func (sv *Service) RedisSaveNow(ctx context.Context, id int64) error {
	i, err := sv.engineOf(ctx, id, func(a string) bool { return a == "redis" })
	if err != nil {
		return err
	}
	rc, err := sv.Redis(ctx, i)
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = rc.Do("BGSAVE")
	return err
}

// ---- other engines ----

// ServiceInfo is the common header of the settings window.
type ServiceInfo struct {
	Installed
	DataSize int64 `json:"data_size"`
	Started  int64 `json:"started"`
}

func (sv *Service) ServiceInfo(ctx context.Context, id int64) (ServiceInfo, error) {
	i, err := sv.service(ctx, id)
	if err != nil {
		return ServiceInfo{}, err
	}
	i.State = dockerx.State(ctx, i.Container)
	si := ServiceInfo{Installed: i, DataSize: dirSize(filepath.Join(sv.dir(i.App, i.Version), "data"))}
	if out, err := dockerx.Run(ctx, "inspect", "-f", "{{.State.StartedAt}}", i.Container); err == nil {
		if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(out)); err == nil {
			si.Started = t.Unix()
		}
	}
	return si, nil
}
