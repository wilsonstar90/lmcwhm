package appstore

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// A minimal RESP2 client for the Redis key browser and settings: the panel
// talks to the published loopback port with the instance's password.

type RedisConn struct {
	c net.Conn
	r *bufio.Reader
}

type RedisError string

func (e RedisError) Error() string { return string(e) }

func DialRedis(ctx context.Context, addr, password string) (*RedisConn, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	rc := &RedisConn{c: c, r: bufio.NewReader(c)}
	if password != "" {
		if _, err := rc.Do("AUTH", password); err != nil {
			c.Close()
			return nil, err
		}
	}
	return rc, nil
}

func (sv *Service) Redis(ctx context.Context, i Installed) (*RedisConn, error) {
	return DialRedis(ctx, fmt.Sprintf("127.0.0.1:%d", i.Port), i.secret)
}

func (rc *RedisConn) Close() error { return rc.c.Close() }

// Do sends one command; replies are string, int64, []any, nil or RedisError.
func (rc *RedisConn) Do(args ...string) (any, error) {
	rc.c.SetDeadline(time.Now().Add(30 * time.Second))
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := io.WriteString(rc.c, b.String()); err != nil {
		return nil, err
	}
	v, err := rc.read()
	if err != nil {
		return nil, err
	}
	if e, ok := v.(RedisError); ok {
		return nil, e
	}
	return v, nil
}

func (rc *RedisConn) line() (string, error) {
	l, err := rc.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(l, "\r\n"), nil
}

func (rc *RedisConn) read() (any, error) {
	l, err := rc.line()
	if err != nil {
		return nil, err
	}
	if l == "" {
		return nil, errors.New("redis: empty reply")
	}
	switch l[0] {
	case '+':
		return l[1:], nil
	case '-':
		return RedisError(l[1:]), nil
	case ':':
		return strconv.ParseInt(l[1:], 10, 64)
	case '$':
		n, err := strconv.Atoi(l[1:])
		if err != nil || n < 0 {
			return nil, err
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(rc.r, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(l[1:])
		if err != nil || n < 0 {
			return nil, err
		}
		out := make([]any, n)
		for i := range out {
			if out[i], err = rc.read(); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	return nil, fmt.Errorf("redis: unexpected reply %q", l)
}

func asInt(v any) int64 {
	n, _ := v.(int64)
	return n
}

func asStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

// ---- key browser ----

type RedisDB struct {
	Index int   `json:"index"`
	Keys  int64 `json:"keys"`
}

// RedisDBs lists every logical database with its key count.
func (sv *Service) RedisDBs(ctx context.Context, id int64) ([]RedisDB, error) {
	i, err := sv.engineOf(ctx, id, func(a string) bool { return a == "redis" })
	if err != nil {
		return nil, err
	}
	rc, err := sv.Redis(ctx, i)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	n := 16
	if v, err := rc.Do("CONFIG", "GET", "databases"); err == nil {
		if s := asStrings(v); len(s) == 2 {
			n, _ = strconv.Atoi(s[1])
		}
	}
	counts := map[int]int64{}
	if v, err := rc.Do("INFO", "keyspace"); err == nil {
		s, _ := v.(string)
		for _, l := range strings.Split(s, "\n") {
			// db0:keys=847,expires=12,avg_ttl=0
			k, rest, ok := strings.Cut(strings.TrimSpace(l), ":")
			if !ok || !strings.HasPrefix(k, "db") {
				continue
			}
			idx, _ := strconv.Atoi(k[2:])
			for _, f := range strings.Split(rest, ",") {
				if strings.HasPrefix(f, "keys=") {
					counts[idx], _ = strconv.ParseInt(f[5:], 10, 64)
				}
			}
		}
	}
	out := make([]RedisDB, n)
	for d := range out {
		out[d] = RedisDB{Index: d, Keys: counts[d]}
	}
	return out, nil
}

type RedisKey struct {
	Key     string `json:"key"`
	Type    string `json:"type"`
	TTL     int64  `json:"ttl"` // seconds; -1 = never expires
	Length  int64  `json:"length"`
	Preview string `json:"preview"`
	Binary  bool   `json:"binary,omitempty"`
}

type RedisKeys struct {
	Cursor string     `json:"cursor"` // "0" = no more
	Total  int64      `json:"total"`
	Keys   []RedisKey `json:"keys"`
}

func clip(s string, n int) (string, bool) {
	bin := !utf8.ValidString(s)
	if bin {
		s = strings.ToValidUTF8(s, "�")
	}
	if len(s) > n {
		s = s[:n]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		s += "…"
	}
	return s, bin
}

func (rc *RedisConn) selectDB(db int) error {
	_, err := rc.Do("SELECT", strconv.Itoa(db))
	return err
}

// describe fills type, TTL, length and a short value preview.
func (rc *RedisConn) describe(key string, previewLen int) RedisKey {
	k := RedisKey{Key: key}
	t, _ := rc.Do("TYPE", key)
	k.Type, _ = t.(string)
	ttl, _ := rc.Do("TTL", key)
	k.TTL = asInt(ttl)
	var v any
	switch k.Type {
	case "string":
		n, _ := rc.Do("STRLEN", key)
		k.Length = asInt(n)
		v, _ = rc.Do("GETRANGE", key, "0", strconv.Itoa(previewLen))
		s, _ := v.(string)
		k.Preview, k.Binary = clip(s, previewLen)
		return k
	case "list":
		n, _ := rc.Do("LLEN", key)
		k.Length = asInt(n)
		v, _ = rc.Do("LRANGE", key, "0", "9")
	case "set":
		n, _ := rc.Do("SCARD", key)
		k.Length = asInt(n)
		v, _ = rc.Do("SRANDMEMBER", key, "10")
	case "zset":
		n, _ := rc.Do("ZCARD", key)
		k.Length = asInt(n)
		v, _ = rc.Do("ZRANGE", key, "0", "9", "WITHSCORES")
	case "hash":
		n, _ := rc.Do("HLEN", key)
		k.Length = asInt(n)
		if r, err := rc.Do("HSCAN", key, "0", "COUNT", "10"); err == nil {
			if arr, ok := r.([]any); ok && len(arr) == 2 {
				v = arr[1]
			}
		}
	case "stream":
		n, _ := rc.Do("XLEN", key)
		k.Length = asInt(n)
		return k
	default:
		return k
	}
	k.Preview, k.Binary = clip(strings.Join(asStrings(v), ", "), previewLen)
	return k
}

// RedisScan returns one page of keys matching pattern ("" = all).
func (sv *Service) RedisScan(ctx context.Context, id int64, db int, cursor, pattern string, count int) (RedisKeys, error) {
	i, err := sv.engineOf(ctx, id, func(a string) bool { return a == "redis" })
	if err != nil {
		return RedisKeys{}, err
	}
	rc, err := sv.Redis(ctx, i)
	if err != nil {
		return RedisKeys{}, err
	}
	defer rc.Close()
	if err := rc.selectDB(db); err != nil {
		return RedisKeys{}, err
	}
	if cursor == "" {
		cursor = "0"
	}
	if pattern == "" {
		pattern = "*"
	} else if !strings.ContainsAny(pattern, "*?[") {
		pattern = "*" + pattern + "*"
	}
	if count < 1 || count > 500 {
		count = 50
	}
	out := RedisKeys{Keys: []RedisKey{}}
	n, _ := rc.Do("DBSIZE")
	out.Total = asInt(n)
	// SCAN may return few keys per step on a sparse match: keep going until
	// the page is full or a time budget is spent.
	seen := map[string]bool{}
	deadline := time.Now().Add(3 * time.Second)
	for {
		v, err := rc.Do("SCAN", cursor, "MATCH", pattern, "COUNT", strconv.Itoa(count*4))
		if err != nil {
			return out, err
		}
		arr, _ := v.([]any)
		if len(arr) != 2 {
			return out, errors.New("redis: bad SCAN reply")
		}
		cursor, _ = arr[0].(string)
		for _, key := range asStrings(arr[1]) {
			if !seen[key] {
				seen[key] = true
				out.Keys = append(out.Keys, rc.describe(key, 120))
			}
		}
		if cursor == "0" || len(out.Keys) >= count || time.Now().After(deadline) {
			break
		}
	}
	out.Cursor = cursor
	return out, nil
}

type RedisValue struct {
	RedisKey
	Value  string     `json:"value,omitempty"`  // string
	Items  []string   `json:"items,omitempty"`  // list, set
	Fields [][]string `json:"fields,omitempty"` // hash: field, value; zset: member, score
}

// RedisGet reads a key's value (the first 1000 elements of a collection).
func (sv *Service) RedisGet(ctx context.Context, id int64, db int, key string) (RedisValue, error) {
	i, err := sv.engineOf(ctx, id, func(a string) bool { return a == "redis" })
	if err != nil {
		return RedisValue{}, err
	}
	rc, err := sv.Redis(ctx, i)
	if err != nil {
		return RedisValue{}, err
	}
	defer rc.Close()
	if err := rc.selectDB(db); err != nil {
		return RedisValue{}, err
	}
	out := RedisValue{RedisKey: rc.describe(key, 0)}
	pairs := func(v any) [][]string {
		s := asStrings(v)
		var p [][]string
		for j := 0; j+1 < len(s); j += 2 {
			p = append(p, []string{s[j], s[j+1]})
		}
		return p
	}
	switch out.Type {
	case "none":
		return out, ErrNotInstalled
	case "string":
		v, _ := rc.Do("GETRANGE", key, "0", strconv.Itoa(4<<20))
		s, _ := v.(string)
		out.Value, out.Binary = clip(s, 4<<20)
	case "list":
		v, _ := rc.Do("LRANGE", key, "0", "999")
		out.Items = asStrings(v)
	case "set":
		if v, err := rc.Do("SSCAN", key, "0", "COUNT", "1000"); err == nil {
			if arr, ok := v.([]any); ok && len(arr) == 2 {
				out.Items = asStrings(arr[1])
			}
		}
	case "zset":
		v, _ := rc.Do("ZRANGE", key, "0", "999", "WITHSCORES")
		out.Fields = pairs(v)
	case "hash":
		if v, err := rc.Do("HSCAN", key, "0", "COUNT", "1000"); err == nil {
			if arr, ok := v.([]any); ok && len(arr) == 2 {
				out.Fields = pairs(arr[1])
			}
		}
	}
	return out, nil
}

// RedisSet writes a string key; ttl > 0 expires it, 0 keeps it forever.
func (sv *Service) RedisSet(ctx context.Context, id int64, db int, key, value string, ttl int64) error {
	if key == "" || ttl < 0 {
		return ErrBadSetting
	}
	i, err := sv.engineOf(ctx, id, func(a string) bool { return a == "redis" })
	if err != nil {
		return err
	}
	rc, err := sv.Redis(ctx, i)
	if err != nil {
		return err
	}
	defer rc.Close()
	if err := rc.selectDB(db); err != nil {
		return err
	}
	args := []string{"SET", key, value}
	if ttl > 0 {
		args = append(args, "EX", strconv.FormatInt(ttl, 10))
	}
	_, err = rc.Do(args...)
	return err
}

// RedisExpire sets a key's TTL (0 = never expires) without touching its value.
func (sv *Service) RedisExpire(ctx context.Context, id int64, db int, key string, ttl int64) error {
	i, err := sv.engineOf(ctx, id, func(a string) bool { return a == "redis" })
	if err != nil {
		return err
	}
	rc, err := sv.Redis(ctx, i)
	if err != nil {
		return err
	}
	defer rc.Close()
	if err := rc.selectDB(db); err != nil {
		return err
	}
	if ttl > 0 {
		_, err = rc.Do("EXPIRE", key, strconv.FormatInt(ttl, 10))
	} else {
		_, err = rc.Do("PERSIST", key)
	}
	return err
}

// RedisDelete removes keys; RedisFlush empties one database (all = every one).
func (sv *Service) RedisDelete(ctx context.Context, id int64, db int, keys []string) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	i, err := sv.engineOf(ctx, id, func(a string) bool { return a == "redis" })
	if err != nil {
		return 0, err
	}
	rc, err := sv.Redis(ctx, i)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	if err := rc.selectDB(db); err != nil {
		return 0, err
	}
	v, err := rc.Do(append([]string{"UNLINK"}, keys...)...)
	return asInt(v), err
}

func (sv *Service) RedisFlush(ctx context.Context, id int64, db int, all bool) error {
	i, err := sv.engineOf(ctx, id, func(a string) bool { return a == "redis" })
	if err != nil {
		return err
	}
	rc, err := sv.Redis(ctx, i)
	if err != nil {
		return err
	}
	defer rc.Close()
	if all {
		_, err = rc.Do("FLUSHALL", "ASYNC")
		return err
	}
	if err := rc.selectDB(db); err != nil {
		return err
	}
	_, err = rc.Do("FLUSHDB", "ASYNC")
	return err
}
