// Package logcenter reads the host's logs for the log center: website
// access / error logs, SSH logins and the systemd journal.
package logcenter

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// maxScan bounds how much of a file is read backwards per request.
const maxScan = 64 << 20

// Tail returns up to n lines from the end of path (oldest first) that
// contain every term of filter (case-insensitive).
func Tail(path string, n int, filter string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	terms := strings.Fields(strings.ToLower(filter))
	match := func(l string) bool {
		if len(terms) == 0 {
			return true
		}
		low := strings.ToLower(l)
		for _, t := range terms {
			if !strings.Contains(low, t) {
				return false
			}
		}
		return true
	}
	const chunk = 256 << 10
	var out []string
	var carry []byte
	pos := fi.Size()
	scanned := int64(0)
	for pos > 0 && len(out) < n && scanned < maxScan {
		size := int64(chunk)
		if pos < size {
			size = pos
		}
		pos -= size
		scanned += size
		buf := make([]byte, size, size+int64(len(carry)))
		if _, err := f.ReadAt(buf, pos); err != nil && err != io.EOF {
			return nil, err
		}
		buf = append(buf, carry...)
		lines := bytes.Split(buf, []byte("\n"))
		// The first piece may be a partial line: keep it for the next chunk.
		if pos > 0 {
			carry = append([]byte(nil), lines[0]...)
			lines = lines[1:]
		} else {
			carry = nil
		}
		for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
			l := strings.TrimRight(string(lines[i]), "\r")
			if l != "" && match(l) {
				out = append(out, l)
			}
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// ---- nginx access log ----

// Access is one line of the "main" log_format.
type Access struct {
	IP      string `json:"ip"`
	Time    int64  `json:"time"`
	Method  string `json:"method"`
	Path    string `json:"path"`
	Proto   string `json:"proto"`
	Status  int    `json:"status"`
	Bytes   int64  `json:"bytes"`
	Referer string `json:"referer"`
	UA      string `json:"ua"`
	Host    string `json:"host"`
	Block   string `json:"block,omitempty"` // why the edge refused it (ip, list, rule-1002, cc, …)
	Raw     string `json:"raw,omitempty"`   // unparsed line
}

var accessRe = regexp.MustCompile(`^(\S+) - (\S+) \[([^\]]+)\] "([^"]*)" (\d{3}) (\d+) "([^"]*)" "([^"]*)"(?: "([^"]*)")?(?: "([^"]*)")?`)

func ParseAccess(line string) Access {
	m := accessRe.FindStringSubmatch(line)
	if m == nil {
		return Access{Raw: line}
	}
	a := Access{IP: m[1], Referer: m[7], UA: m[8], Host: m[9], Block: m[10]}
	if a.Block == "-" {
		a.Block = ""
	}
	if t, err := time.Parse("02/Jan/2006:15:04:05 -0700", m[3]); err == nil {
		a.Time = t.Unix()
	}
	if p := strings.SplitN(m[4], " ", 3); len(p) == 3 {
		a.Method, a.Path, a.Proto = p[0], p[1], p[2]
	} else {
		a.Path = m[4]
	}
	a.Status, _ = strconv.Atoi(m[5])
	a.Bytes, _ = strconv.ParseInt(m[6], 10, 64)
	return a
}

// ---- SSH ----

type SSHEvent struct {
	Time   int64  `json:"time"`
	Result string `json:"result"` // accepted | failed | invalid
	User   string `json:"user"`
	IP     string `json:"ip"`
	Port   int    `json:"port"`
	Method string `json:"method"`
}

var (
	sshAccepted = regexp.MustCompile(`sshd\[\d+\]: Accepted (\S+) for (\S+) from (\S+) port (\d+)`)
	sshFailed   = regexp.MustCompile(`sshd\[\d+\]: Failed (\S+) for (?:invalid user )?(\S+) from (\S+) port (\d+)`)
	sshInvalid  = regexp.MustCompile(`sshd\[\d+\]: Invalid user (\S*) from (\S+)(?: port (\d+))?`)
)

// parseTime reads syslog timestamps: RFC 3339 (rsyslog high precision, Ubuntu
// 24.04) or the classic "Oct 28 04:09:22" (year assumed current).
func parseTime(line string, now time.Time) int64 {
	if sp := strings.IndexByte(line, ' '); sp > 0 {
		if t, err := time.Parse(time.RFC3339Nano, line[:sp]); err == nil {
			return t.Unix()
		}
		// journalctl -o short-iso before systemd 250: "+0000".
		if t, err := time.Parse("2006-01-02T15:04:05-0700", line[:sp]); err == nil {
			return t.Unix()
		}
	}
	if len(line) >= 15 {
		if t, err := time.ParseInLocation("Jan _2 15:04:05", line[:15], time.Local); err == nil {
			t = t.AddDate(now.Year(), 0, 0)
			if t.After(now.Add(24 * time.Hour)) {
				t = t.AddDate(-1, 0, 0) // December lines read in January
			}
			return t.Unix()
		}
	}
	return 0
}

func ParseSSH(line string, now time.Time) (SSHEvent, bool) {
	var e SSHEvent
	switch {
	case sshAccepted.MatchString(line):
		m := sshAccepted.FindStringSubmatch(line)
		e = SSHEvent{Result: "accepted", Method: m[1], User: m[2], IP: m[3]}
		e.Port, _ = strconv.Atoi(m[4])
	case sshFailed.MatchString(line):
		m := sshFailed.FindStringSubmatch(line)
		e = SSHEvent{Result: "failed", Method: m[1], User: m[2], IP: m[3]}
		e.Port, _ = strconv.Atoi(m[4])
	case sshInvalid.MatchString(line):
		m := sshInvalid.FindStringSubmatch(line)
		e = SSHEvent{Result: "invalid", User: m[1], IP: m[2]}
		e.Port, _ = strconv.Atoi(m[3])
	default:
		return e, false
	}
	e.Time = parseTime(line, now)
	return e, true
}

// SSHLines returns raw sshd lines: /var/log/auth.log (and .1), or the
// journal when rsyslog is not installed.
func SSHLines(ctx context.Context, n int) []string {
	var lines []string
	for _, p := range []string{"/var/log/auth.log.1", "/var/log/auth.log"} {
		if l, err := Tail(p, n, "sshd"); err == nil {
			lines = append(lines, l...)
		}
	}
	if len(lines) == 0 {
		out, _ := exec.CommandContext(ctx, "journalctl", "-u", "ssh", "-u", "sshd", "-n", strconv.Itoa(n), "-o", "short-iso", "--no-pager").Output()
		for _, l := range strings.Split(string(out), "\n") {
			if strings.Contains(l, "sshd") {
				lines = append(lines, l)
			}
		}
	}
	return lines
}

// SSHSummary counts events per IP for the "top attackers" table.
type SSHSummary struct {
	Accepted int        `json:"accepted"`
	Failed   int        `json:"failed"`
	TopIPs   []IPCount  `json:"top_ips"`
	Items    []SSHEvent `json:"items"`
}

type IPCount struct {
	IP    string `json:"ip"`
	Count int    `json:"count"`
}

func SSH(ctx context.Context, n int, filter string) SSHSummary {
	now := time.Now()
	var s SSHSummary
	fails := map[string]int{}
	terms := strings.ToLower(filter)
	for _, l := range SSHLines(ctx, 20000) {
		e, ok := ParseSSH(l, now)
		if !ok {
			continue
		}
		if terms != "" && !strings.Contains(strings.ToLower(e.User+" "+e.IP+" "+e.Result), terms) {
			continue
		}
		if e.Result == "accepted" {
			s.Accepted++
		} else {
			s.Failed++
			fails[e.IP]++
		}
		s.Items = append(s.Items, e)
	}
	sort.SliceStable(s.Items, func(i, j int) bool { return s.Items[i].Time > s.Items[j].Time })
	if len(s.Items) > n {
		s.Items = s.Items[:n]
	}
	for ip, c := range fails {
		s.TopIPs = append(s.TopIPs, IPCount{ip, c})
	}
	sort.Slice(s.TopIPs, func(i, j int) bool {
		if s.TopIPs[i].Count != s.TopIPs[j].Count {
			return s.TopIPs[i].Count > s.TopIPs[j].Count
		}
		return s.TopIPs[i].IP < s.TopIPs[j].IP
	})
	if len(s.TopIPs) > 20 {
		s.TopIPs = s.TopIPs[:20]
	}
	if s.Items == nil {
		s.Items = []SSHEvent{}
	}
	return s
}

// ---- journal ----

var unitRe = regexp.MustCompile(`^[A-Za-z0-9@._\-]{1,128}$`)

// Journal returns the last n journal lines, optionally for one unit.
func Journal(ctx context.Context, unit string, n int, filter string) ([]string, error) {
	args := []string{"-n", strconv.Itoa(n), "-o", "short-iso", "--no-pager"}
	if unit != "" {
		if !unitRe.MatchString(unit) {
			return nil, ErrBadUnit
		}
		args = append(args, "-u", unit)
	}
	if filter != "" {
		args = append(args, "-g", regexp.QuoteMeta(filter), "--case-sensitive=false")
	}
	out, err := exec.CommandContext(ctx, "journalctl", args...).Output()
	if err != nil && len(out) == 0 {
		// No journald (containers, macOS dev): fall back to syslog.
		return Tail("/var/log/syslog", n, filter)
	}
	lines := []string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if l := sc.Text(); l != "" && !strings.HasPrefix(l, "-- ") {
			lines = append(lines, l)
		}
	}
	return lines, nil
}

type errString string

func (e errString) Error() string { return string(e) }

const ErrBadUnit = errString("logs.invalid_unit")
