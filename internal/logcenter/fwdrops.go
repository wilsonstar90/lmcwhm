package logcenter

import (
	"context"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Packets the firewall dropped, from the kernel log lines its log rules
// write ("lmcwhm-drop <reason> IN=eth0 ... SRC=… DST=… PROTO=UDP SPT=… DPT=…").

// DropPrefix matches firewall.DropPrefix.
const DropPrefix = "lmcwhm-drop "

type Drop struct {
	Time   int64  `json:"time"`
	Reason string `json:"reason"` // block | ban | policy | rule<N>
	Proto  string `json:"proto"`  // TCP | UDP | ICMP | ICMPv6 | …
	Src    string `json:"src"`
	Dst    string `json:"dst"`
	SPort  int    `json:"sport,omitempty"`
	DPort  int    `json:"dport,omitempty"`
	Iface  string `json:"iface"`
}

var (
	dropRe  = regexp.MustCompile(`lmcwhm-drop (\S+) `)
	fieldRe = regexp.MustCompile(`\b(IN|SRC|DST|PROTO|SPT|DPT)=(\S*)`)
)

func ParseDrop(line string, now time.Time) (Drop, bool) {
	m := dropRe.FindStringSubmatch(line)
	if m == nil {
		return Drop{}, false
	}
	d := Drop{Reason: m[1], Time: parseTime(line, now)}
	for _, f := range fieldRe.FindAllStringSubmatch(line[strings.Index(line, m[0]):], -1) {
		switch f[1] {
		case "IN":
			d.Iface = f[2]
		case "SRC":
			d.Src = f[2]
		case "DST":
			d.Dst = f[2]
		case "PROTO":
			d.Proto = strings.ToUpper(f[2])
			if d.Proto == "ICMPV6" {
				d.Proto = "ICMPv6"
			}
		case "SPT":
			d.SPort, _ = strconv.Atoi(f[2])
		case "DPT":
			d.DPort, _ = strconv.Atoi(f[2])
		}
	}
	return d, d.Src != ""
}

// DropSummary counts the dropped packets by protocol and source.
type DropSummary struct {
	ByProto map[string]int `json:"by_proto"`
	TopIPs  []IPCount      `json:"top_ips"`
	Items   []Drop         `json:"items"`
}

// Drops reads the newest n logged drops (journal, or kern.log without one).
func Drops(ctx context.Context, n int, filter string) DropSummary {
	out, _ := exec.CommandContext(ctx, "journalctl", "-k", "-g", DropPrefix, "-n", strconv.Itoa(n), "-o", "short-iso", "--no-pager").Output()
	lines := strings.Split(string(out), "\n")
	if !strings.Contains(string(out), DropPrefix) {
		lines, _ = Tail("/var/log/kern.log", n, DropPrefix)
	}
	return summarize(lines, filter, time.Now())
}

func summarize(lines []string, filter string, now time.Time) DropSummary {
	s := DropSummary{ByProto: map[string]int{}, Items: []Drop{}}
	count := map[string]int{}
	filter = strings.ToLower(filter)
	for _, l := range lines {
		d, ok := ParseDrop(l, now)
		if !ok || filter != "" && !strings.Contains(strings.ToLower(l), filter) {
			continue
		}
		s.ByProto[d.Proto]++
		count[d.Src]++
		s.Items = append(s.Items, d)
	}
	sort.SliceStable(s.Items, func(i, j int) bool { return s.Items[i].Time > s.Items[j].Time })
	for ip, c := range count {
		s.TopIPs = append(s.TopIPs, IPCount{IP: ip, Count: c})
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
	return s
}
