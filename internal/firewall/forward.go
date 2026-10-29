package firewall

// Port forwarding: connections to a listen port (TCP, UDP or both) go to a
// port of this server (redirect) or of another IPv4 host (DNAT + masquerade).
// The rules live in their own table, so they work with the firewall on or
// off; the firewall's input chain accepts forwarded connections.

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const FwdTable = "lmcwhm_fwd"

var (
	ErrBadForward  = errors.New("fw.invalid_forward")
	ErrForwardPort = errors.New("fw.forward_port_taken")
)

type Forward struct {
	ID         int64  `json:"id"`
	Proto      string `json:"proto"` // tcp | udp | both
	Listen     int    `json:"listen"`
	TargetHost string `json:"target_host"` // "" = this server
	TargetPort int    `json:"target_port"`
	Source     string `json:"source"`
	Remark     string `json:"remark"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  int64  `json:"created_at"`
	// Packets / Bytes are the live counters (since the rules were installed).
	Packets int64 `json:"packets"`
	Bytes   int64 `json:"bytes"`
}

func (f Forward) local() bool { return f.TargetHost == "" }

func protos(p string) []string {
	if p == "both" {
		return []string{"tcp", "udp"}
	}
	return []string{p}
}

func (sv *Service) Forwards(ctx context.Context) ([]Forward, error) {
	rows, err := sv.st.DB.QueryContext(ctx, `SELECT id, proto, listen, target_host, target_port, source, remark, enabled, created_at FROM port_forwards ORDER BY listen, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Forward{}
	for rows.Next() {
		var f Forward
		if err := rows.Scan(&f.ID, &f.Proto, &f.Listen, &f.TargetHost, &f.TargetPort, &f.Source, &f.Remark, &f.Enabled, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	counters := sv.forwardCounters(ctx)
	for i := range out {
		c := counters[out[i].ID]
		out[i].Packets, out[i].Bytes = c[0], c[1]
	}
	return out, nil
}

// validate normalises f; used are the ports something else listens on.
func (sv *Service) validate(ctx context.Context, f *Forward, used map[int]string) error {
	f.Remark = strings.TrimSpace(f.Remark)
	if len(f.Remark) > 200 {
		f.Remark = f.Remark[:200]
	}
	if f.Proto != "tcp" && f.Proto != "udp" && f.Proto != "both" || f.Listen < 1 || f.Listen > 65535 || f.TargetPort < 1 || f.TargetPort > 65535 {
		return ErrBadForward
	}
	switch h := strings.TrimSpace(f.TargetHost); h {
	case "", "127.0.0.1", "localhost", "::1":
		f.TargetHost = ""
		if f.TargetPort == f.Listen {
			return ErrBadForward
		}
	default:
		a, err := netip.ParseAddr(h)
		if err != nil || !a.Unmap().Is4() || a.IsUnspecified() || a.IsMulticast() {
			return ErrBadForward // another host: an IPv4 address
		}
		f.TargetHost = a.Unmap().String()
	}
	if s := strings.TrimSpace(f.Source); s != "" {
		_, norm, err := NormIP(s)
		if err != nil {
			return ErrBadForward
		}
		f.Source = norm
	}
	// The edge, SSH and the panel keep their ports.
	reserved := map[int]bool{80: true, 443: true}
	for _, p := range SSHPorts() {
		reserved[p] = true
	}
	if p := sv.PanelPort(); p > 0 {
		reserved[p] = true
	}
	if reserved[f.Listen] {
		return fmt.Errorf("%w: %d", ErrForwardPort, f.Listen)
	}
	if who, ok := used[f.Listen]; ok && f.Proto != "udp" {
		return fmt.Errorf("%w: %d (%s)", ErrForwardPort, f.Listen, who)
	}
	all, err := sv.Forwards(ctx)
	if err != nil {
		return err
	}
	for _, o := range all {
		if o.ID == f.ID || o.Listen != f.Listen {
			continue
		}
		for _, a := range protos(o.Proto) {
			for _, b := range protos(f.Proto) {
				if a == b {
					return fmt.Errorf("%w: %d/%s", ErrForwardPort, f.Listen, a)
				}
			}
		}
	}
	return nil
}

// SaveForward creates (ID 0) or replaces a forward and installs the rules.
// used are the TCP ports already published (e.g. by Docker), by name.
func (sv *Service) SaveForward(ctx context.Context, f Forward, used map[int]string) (Forward, error) {
	if err := sv.validate(ctx, &f, used); err != nil {
		return f, err
	}
	if f.ID == 0 {
		f.CreatedAt = time.Now().Unix()
		res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO port_forwards(proto, listen, target_host, target_port, source, remark, enabled, created_at) VALUES(?,?,?,?,?,?,?,?)`,
			f.Proto, f.Listen, f.TargetHost, f.TargetPort, f.Source, f.Remark, f.Enabled, f.CreatedAt)
		if err != nil {
			return f, err
		}
		f.ID, _ = res.LastInsertId()
	} else {
		res, err := sv.st.DB.ExecContext(ctx, `UPDATE port_forwards SET proto=?, listen=?, target_host=?, target_port=?, source=?, remark=?, enabled=? WHERE id=?`,
			f.Proto, f.Listen, f.TargetHost, f.TargetPort, f.Source, f.Remark, f.Enabled, f.ID)
		if err != nil {
			return f, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return f, ErrNotFound
		}
	}
	return f, sv.InstallForwards(ctx)
}

func (sv *Service) DeleteForward(ctx context.Context, id int64) error {
	res, err := sv.st.DB.ExecContext(ctx, `DELETE FROM port_forwards WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return sv.InstallForwards(ctx)
}

// ForwardRuleset renders the forwarding table ("" when nothing is forwarded).
func ForwardRuleset(fs []Forward) string {
	var pre, post strings.Builder
	n := 0
	for _, f := range fs {
		if !f.Enabled {
			continue
		}
		n++
		src := ""
		if f.Source != "" {
			if p, _, _ := NormIP(f.Source); p.Addr().Is6() {
				src = "ip6 saddr " + f.Source + " "
			} else {
				src = "ip saddr " + f.Source + " "
			}
		}
		match := fmt.Sprintf("tcp dport %d", f.Listen)
		switch f.Proto {
		case "udp":
			match = fmt.Sprintf("udp dport %d", f.Listen)
		case "both":
			match = fmt.Sprintf("meta l4proto { tcp, udp } th dport %d", f.Listen)
		}
		comment := fmt.Sprintf(`comment "forward %d"`, f.ID)
		if f.local() {
			fmt.Fprintf(&pre, "\t\t%s%s counter redirect to :%d %s\n", src, match, f.TargetPort, comment)
			continue
		}
		if strings.HasPrefix(src, "ip6") {
			continue // an IPv4 target cannot be reached from an IPv6 source
		}
		fmt.Fprintf(&pre, "\t\tmeta nfproto ipv4 %s%s counter dnat ip to %s:%d %s\n", src, match, f.TargetHost, f.TargetPort, comment)
		pm := strings.Replace(match, strconv.Itoa(f.Listen), strconv.Itoa(f.TargetPort), 1)
		fmt.Fprintf(&post, "\t\tct status dnat ip daddr %s %s masquerade\n", f.TargetHost, pm)
	}
	if n == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Generated by lmcwhm (port forwarding) — edit in the panel.\ntable inet %s {\n", FwdTable)
	b.WriteString("\tchain prerouting {\n\t\ttype nat hook prerouting priority dstnat; policy accept;\n")
	b.WriteString(pre.String())
	b.WriteString("\t}\n\tchain postrouting {\n\t\ttype nat hook postrouting priority srcnat; policy accept;\n")
	b.WriteString(post.String())
	b.WriteString("\t}\n}\n")
	return b.String()
}

// InstallForwards replaces the forwarding table (or removes it).
func (sv *Service) InstallForwards(ctx context.Context) error {
	fs, err := sv.Forwards(ctx)
	if err != nil {
		return err
	}
	if !sv.Available() {
		for _, f := range fs {
			if f.Enabled {
				return ErrNoNft
			}
		}
		return nil
	}
	rs := ForwardRuleset(fs)
	script := fmt.Sprintf("table inet %[1]s\ndelete table inet %[1]s\n", FwdTable) + rs
	if rs != "" {
		for _, f := range fs {
			if f.Enabled && !f.local() {
				// Forwarding to another host needs IPv4 routing (Docker turns it on too).
				os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1\n"), 0o644)
				break
			}
		}
		if err := sv.run(ctx, script, "-c"); err != nil {
			return err
		}
	}
	return sv.run(ctx, script)
}

var counterRe = regexp.MustCompile(`counter packets (\d+) bytes (\d+) .*comment "forward (\d+)"`)

// forwardCounters reads the live packet / byte counters per forward.
func (sv *Service) forwardCounters(ctx context.Context) map[int64][2]int64 {
	out := map[int64][2]int64{}
	if !sv.Available() {
		return out
	}
	b, err := execOut(ctx, sv.nft, "list", "table", "inet", FwdTable)
	if err != nil {
		return out
	}
	for _, m := range counterRe.FindAllStringSubmatch(b, -1) {
		id, _ := strconv.ParseInt(m[3], 10, 64)
		p, _ := strconv.ParseInt(m[1], 10, 64)
		by, _ := strconv.ParseInt(m[2], 10, 64)
		c := out[id]
		out[id] = [2]int64{c[0] + p, c[1] + by}
	}
	return out
}

func execOut(ctx context.Context, bin string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, bin, args...).Output()
	return string(out), err
}
