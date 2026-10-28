package appstore

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestSetCnf(t *testing.T) {
	in := "[client]\nport = 3306\n[mysqld]\ncharacter-set-server = utf8mb4\nmax_connections = 500\n# innodb_buffer_pool_size = 256M\n[mysqldump]\nquick\n"
	out := setCnf(in, "max_connections", "800")
	out = setCnf(out, "innodb_buffer_pool_size", "1G")
	out = setCnf(out, "slow_query_log", "1")
	out = setCnf(out, "character_set_server", "") // dashes and underscores are the same option
	out = setCnf(out, "skip-log-bin", "-")
	want := "[client]\nport = 3306\n[mysqld]\nmax_connections = 800\ninnodb_buffer_pool_size = 1G\nslow_query_log = 1\nskip-log-bin\n[mysqldump]\nquick\n"
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
	if got := setCnf("", "log_bin", "mysql-bin"); got != "[mysqld]\nlog_bin = mysql-bin\n" {
		t.Fatalf("empty file: %q", got)
	}
	// The [client] port is not the server's.
	if got := setCnf("[client]\nport = 1\n", "port", "2"); !strings.Contains(got, "[client]\nport = 1\n[mysqld]\nport = 2") {
		t.Fatalf("client section changed: %q", got)
	}
}

func TestSetRedisConf(t *testing.T) {
	in := "# x\nbind 0.0.0.0\nsave 900 1\nsave 300 10\nappendonly yes\n"
	out := setRedisConf(in, "save", "60 1000")
	out = setRedisConf(out, "appendonly", "no")
	out = setRedisConf(out, "maxmemory", "256mb")
	if out != "# x\nbind 0.0.0.0\nsave 60 1000\nappendonly no\nmaxmemory 256mb\n" {
		t.Fatalf("got %q", out)
	}
	if v := redisConfValues(out, "save"); len(v) != 1 || v[0] != "60 1000" {
		t.Fatalf("values %v", v)
	}
}

// fakeRedis answers a scripted set of commands.
func fakeRedis(t *testing.T, reply func(args []string) string) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					var n int
					if _, err := fmt.Fscanf(r, "*%d\r\n", &n); err != nil {
						return
					}
					args := make([]string, n)
					for i := range args {
						var l int
						fmt.Fscanf(r, "$%d\r\n", &l)
						b := make([]byte, l+2)
						r.Read(b)
						args[i] = string(b[:l])
					}
					c.Write([]byte(reply(args)))
				}
			}(c)
		}
	}()
	return l.Addr().String()
}

func TestRedisClient(t *testing.T) {
	addr := fakeRedis(t, func(a []string) string {
		switch strings.ToUpper(a[0]) {
		case "AUTH":
			if a[1] != "pw" {
				return "-WRONGPASS invalid password\r\n"
			}
			return "+OK\r\n"
		case "GET":
			return "$5\r\nhe\r\nl\r\n" // bulk strings are binary-safe
		case "SCAN":
			return "*2\r\n$1\r\n0\r\n*2\r\n$1\r\na\r\n$1\r\nb\r\n"
		case "DBSIZE":
			return ":2\r\n"
		case "NIL":
			return "$-1\r\n"
		}
		return "-ERR unknown\r\n"
	})
	if _, err := DialRedis(context.Background(), addr, "bad"); err == nil || !strings.Contains(err.Error(), "WRONGPASS") {
		t.Fatalf("bad password: %v", err)
	}
	rc, err := DialRedis(context.Background(), addr, "pw")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if v, err := rc.Do("GET", "k"); err != nil || v != "he\r\nl" {
		t.Fatalf("GET %q %v", v, err)
	}
	v, err := rc.Do("SCAN", "0")
	if arr, ok := v.([]any); err != nil || !ok || len(arr) != 2 || strings.Join(asStrings(arr[1]), ",") != "a,b" {
		t.Fatalf("SCAN %#v %v", v, err)
	}
	if v, err := rc.Do("NIL"); err != nil || v != nil {
		t.Fatalf("nil bulk %#v %v", v, err)
	}
	if _, err := rc.Do("BOGUS"); err == nil {
		t.Fatal("error reply not returned")
	}
	if n, _ := rc.Do("DBSIZE"); n != int64(2) {
		t.Fatalf("DBSIZE %#v", n)
	}
}

func TestClip(t *testing.T) {
	if s, bin := clip("héllo world", 3); s != "hé…" || bin {
		t.Fatalf("clip %q %v", s, bin)
	}
	if _, bin := clip("\xff\xfe", 10); !bin {
		t.Fatal("binary not flagged")
	}
}
