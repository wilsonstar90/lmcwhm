package toolbox

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
)

// BBR is Google's TCP congestion control: the server sends at the rate the
// path can carry instead of backing off on every lost packet, which helps
// most on long or lossy routes (overseas visitors, mobile networks). It only
// changes how this server sends TCP; nothing listens or routes differently.
//
// Enabling loads tcp_bbr (and loads it at boot), sets the default qdisc to
// fq and the congestion control to bbr in a sysctl file of its own, and
// switches the uplink interfaces to fq. Disabling restores cubic and
// fq_codel (Ubuntu's defaults).

var ErrNoBBR = errors.New("toolbox.bbr_unsupported")

const (
	bbrSysctl  = "/etc/sysctl.d/91-lmcwhm-bbr.conf"
	bbrModules = "/etc/modules-load.d/lmcwhm-bbr.conf"
)

type BBRStatus struct {
	Kernel    string   `json:"kernel"`
	Supported bool     `json:"supported"` // the kernel has (or can load) tcp_bbr
	Enabled   bool     `json:"enabled"`   // bbr is the congestion control now
	Persisted bool     `json:"persisted"` // set at boot by lmcwhm
	Current   string   `json:"current"`   // net.ipv4.tcp_congestion_control
	Qdisc     string   `json:"qdisc"`     // net.core.default_qdisc
	Available []string `json:"available"` // tcp_available_congestion_control
	Ifaces    []Iface  `json:"ifaces"`    // uplink interfaces and their root qdisc
}

type Iface struct {
	Name  string `json:"name"`
	Qdisc string `json:"qdisc"`
}

func (sv *Service) readProc(p string) string {
	b, err := os.ReadFile(sv.path(p))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// kernelAtLeast compares a "6.8.0-45-generic" style release with major.minor.
func kernelAtLeast(release string, major, minor int) bool {
	parts := strings.SplitN(release, ".", 3)
	if len(parts) < 2 {
		return false
	}
	ma, _ := strconv.Atoi(parts[0])
	mi, _ := strconv.Atoi(strings.TrimFunc(parts[1], func(r rune) bool { return r < '0' || r > '9' }))
	return ma > major || ma == major && mi >= minor
}

// uplinks are the interfaces with a default route (from /proc/net/route and
// /proc/net/ipv6_route), never loopback or container bridges.
func (sv *Service) uplinks() []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		if name == "" || name == "lo" || seen[name] || name == "docker0" || strings.HasPrefix(name, "br-") || strings.HasPrefix(name, "veth") {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, l := range strings.Split(sv.readProc("/proc/net/route"), "\n")[1:] {
		f := strings.Fields(l)
		if len(f) > 7 && f[1] == "00000000" && f[7] == "00000000" {
			add(f[0])
		}
	}
	for _, l := range strings.Split(sv.readProc("/proc/net/ipv6_route"), "\n") {
		f := strings.Fields(l)
		if len(f) == 10 && f[0] == strings.Repeat("0", 32) && f[1] == "00" {
			add(f[9])
		}
	}
	return out
}

func (sv *Service) rootQdisc(ctx context.Context, iface string) string {
	out, err := sv.Run(ctx, "tc", "qdisc", "show", "dev", iface, "root")
	if err != nil {
		return ""
	}
	if f := strings.Fields(out); len(f) > 1 && f[0] == "qdisc" {
		return f[1]
	}
	return ""
}

func (sv *Service) BBR(ctx context.Context) BBRStatus {
	st := BBRStatus{
		Kernel:    sv.readProc("/proc/sys/kernel/osrelease"),
		Current:   sv.readProc("/proc/sys/net/ipv4/tcp_congestion_control"),
		Qdisc:     sv.readProc("/proc/sys/net/core/default_qdisc"),
		Available: strings.Fields(sv.readProc("/proc/sys/net/ipv4/tcp_available_congestion_control")),
		Ifaces:    []Iface{},
	}
	if st.Available == nil {
		st.Available = []string{}
	}
	st.Enabled = st.Current == "bbr"
	_, err := os.Stat(sv.path(bbrSysctl))
	st.Persisted = err == nil
	st.Supported = st.Enabled || contains(st.Available, "bbr")
	if !st.Supported && kernelAtLeast(st.Kernel, 4, 9) {
		// Not loaded yet: is the module there?
		_, err := sv.Run(ctx, "modinfo", "-F", "name", "tcp_bbr")
		st.Supported = err == nil
	}
	for _, i := range sv.uplinks() {
		st.Ifaces = append(st.Ifaces, Iface{Name: i, Qdisc: sv.rootQdisc(ctx, i)})
	}
	return st
}

func contains(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}

// SetBBR turns BBR on (bbr + fq) or back to Ubuntu's defaults (cubic +
// fq_codel), now and at boot.
func (sv *Service) SetBBR(ctx context.Context, on bool, log func(string, ...any)) error {
	if log == nil {
		log = func(string, ...any) {}
	}
	cc, qdisc := "cubic", "fq_codel"
	if on {
		st := sv.BBR(ctx)
		if !st.Supported {
			return ErrNoBBR
		}
		if !contains(st.Available, "bbr") && !st.Enabled {
			log("modprobe tcp_bbr")
			if _, err := sv.Run(ctx, "modprobe", "tcp_bbr"); err != nil {
				return err
			}
		}
		if err := os.MkdirAll(sv.path("/etc/modules-load.d"), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(sv.path(bbrModules), []byte("# lmcwhm: TCP BBR\ntcp_bbr\n"), 0o644); err != nil {
			return err
		}
		cc, qdisc = "bbr", "fq"
		body := "# lmcwhm: TCP BBR congestion control (App Store)\nnet.core.default_qdisc = fq\nnet.ipv4.tcp_congestion_control = bbr\n"
		if err := os.MkdirAll(sv.path("/etc/sysctl.d"), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(sv.path(bbrSysctl), []byte(body), 0o644); err != nil {
			return err
		}
	} else {
		for _, f := range []string{bbrSysctl, bbrModules} {
			if err := os.Remove(sv.path(f)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	for _, kv := range []string{"net.core.default_qdisc=" + qdisc, "net.ipv4.tcp_congestion_control=" + cc} {
		log("sysctl -w %s", kv)
		if _, err := sv.Run(ctx, "sysctl", "-w", kv); err != nil {
			return err
		}
	}
	// The default qdisc applies to new interfaces only: switch the uplinks
	// now. BBR paces by itself since Linux 4.13, so a failure is not fatal.
	for _, i := range sv.uplinks() {
		log("tc qdisc replace dev %s root %s", i, qdisc)
		if _, err := sv.Run(ctx, "tc", "qdisc", "replace", "dev", i, "root", qdisc); err != nil {
			log("%v", err)
		}
	}
	return nil
}
