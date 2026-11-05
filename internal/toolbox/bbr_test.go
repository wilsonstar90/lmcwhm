package toolbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBBR(t *testing.T) {
	root := t.TempDir()
	write := func(p, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755)
		os.WriteFile(filepath.Join(root, p), []byte(s), 0o644)
	}
	write("/proc/sys/kernel/osrelease", "5.15.0-122-generic\n")
	write("/proc/sys/net/ipv4/tcp_congestion_control", "cubic\n")
	write("/proc/sys/net/core/default_qdisc", "fq_codel\n")
	write("/proc/sys/net/ipv4/tcp_available_congestion_control", "reno cubic\n")
	write("/proc/net/route", "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n"+
		"eth0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n"+
		"docker0\t000011AC\t00000000\t0001\t0\t0\t0\t0000FFFF\t0\t0\t0\n"+
		"eth0\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n")
	write("/proc/net/ipv6_route", "00000000000000000000000000000000 00 00000000000000000000000000000000 00 fe800000000000000000000000000001 00000400 00000001 00000000 00000003     eth0\n")

	var cmds []string
	sv := &Service{Root: root, Run: func(_ context.Context, name string, args ...string) (string, error) {
		c := name + " " + strings.Join(args, " ")
		cmds = append(cmds, c)
		switch {
		case name == "modinfo":
			return "tcp_bbr\n", nil
		case c == "tc qdisc show dev eth0 root":
			return "qdisc mq 0: root\n", nil
		case name == "sysctl":
			kv := strings.SplitN(args[1], "=", 2)
			write("/proc/sys/"+strings.ReplaceAll(kv[0], ".", "/"), kv[1]+"\n")
		case name == "modprobe":
			write("/proc/sys/net/ipv4/tcp_available_congestion_control", "reno cubic bbr\n")
		}
		return "", nil
	}}
	ctx := context.Background()
	st := sv.BBR(ctx)
	if !st.Supported || st.Enabled || st.Persisted || st.Current != "cubic" || len(st.Ifaces) != 1 || st.Ifaces[0] != (Iface{"eth0", "mq"}) {
		t.Fatalf("before: %+v", st)
	}
	if err := sv.SetBBR(ctx, true, nil); err != nil {
		t.Fatal(err)
	}
	st = sv.BBR(ctx)
	if !st.Enabled || !st.Persisted || st.Qdisc != "fq" {
		t.Fatalf("after enabling: %+v", st)
	}
	conf, _ := os.ReadFile(filepath.Join(root, bbrSysctl))
	mods, _ := os.ReadFile(filepath.Join(root, bbrModules))
	if !strings.Contains(string(conf), "net.ipv4.tcp_congestion_control = bbr") || !strings.Contains(string(mods), "tcp_bbr") {
		t.Errorf("files: %q %q", conf, mods)
	}
	if !strings.Contains(strings.Join(cmds, "\n"), "modprobe tcp_bbr\n") || !strings.Contains(strings.Join(cmds, "\n"), "tc qdisc replace dev eth0 root fq") ||
		strings.Contains(strings.Join(cmds, "\n"), "dev docker0") {
		t.Errorf("commands:\n%s", strings.Join(cmds, "\n"))
	}
	if err := sv.SetBBR(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	st = sv.BBR(ctx)
	if st.Enabled || st.Persisted || st.Current != "cubic" || st.Qdisc != "fq_codel" {
		t.Fatalf("after disabling: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(root, bbrModules)); !os.IsNotExist(err) {
		t.Error("modules-load file left")
	}

	// An old kernel without the module.
	write("/proc/sys/kernel/osrelease", "4.4.0-210-generic\n")
	write("/proc/sys/net/ipv4/tcp_available_congestion_control", "reno cubic\n")
	if sv.BBR(ctx).Supported || sv.SetBBR(ctx, true, nil) != ErrNoBBR {
		t.Error("BBR offered on 4.4")
	}
	if !kernelAtLeast("6.8.0-45-generic", 4, 9) || kernelAtLeast("4.4.0", 4, 9) || !kernelAtLeast("4.9", 4, 9) {
		t.Error("kernelAtLeast")
	}
}
