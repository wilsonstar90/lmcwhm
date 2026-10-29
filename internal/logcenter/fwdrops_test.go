package logcenter

import (
	"testing"
	"time"
)

func TestParseDrop(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	lines := []string{
		"2026-10-30T11:58:01+0000 web1 kernel: lmcwhm-drop block IN=eth0 OUT= MAC=f2:3c SRC=198.51.100.7 DST=203.0.113.1 LEN=60 TOS=0x00 PREC=0x00 TTL=50 ID=1 DF PROTO=TCP SPT=51234 DPT=22 WINDOW=64240 RES=0x00 SYN URGP=0",
		"2026-10-30T11:59:01+0000 web1 kernel: lmcwhm-drop policy IN=eth0 OUT= MAC=f2:3c SRC=198.51.100.7 DST=203.0.113.1 LEN=40 PROTO=UDP SPT=5353 DPT=161 LEN=20",
		"2026-10-30T11:59:30+0000 web1 kernel: lmcwhm-drop rule7 IN=eth0 OUT= SRC=2001:db8::9 DST=2001:db8::1 LEN=104 TC=0 HOPLIMIT=60 FLOWLBL=0 PROTO=ICMPv6 TYPE=128 CODE=0",
		"2026-10-30T11:59:40+0000 web1 kernel: something else SRC=1.1.1.1",
	}
	s := summarize(lines, "", now)
	if len(s.Items) != 3 || s.ByProto["TCP"] != 1 || s.ByProto["UDP"] != 1 || s.ByProto["ICMPv6"] != 1 {
		t.Fatalf("%+v", s)
	}
	if d := s.Items[2]; d.Reason != "block" || d.Src != "198.51.100.7" || d.DPort != 22 || d.SPort != 51234 || d.Iface != "eth0" || d.Time == 0 {
		t.Fatalf("tcp drop %+v", d)
	}
	if d := s.Items[1]; d.Proto != "UDP" || d.DPort != 161 || d.Reason != "policy" {
		t.Fatalf("udp drop %+v", d)
	}
	if s.TopIPs[0].IP != "198.51.100.7" || s.TopIPs[0].Count != 2 {
		t.Fatalf("top %+v", s.TopIPs)
	}
	if f := summarize(lines, "udp", now); len(f.Items) != 1 {
		t.Fatalf("filter %+v", f.Items)
	}
}
