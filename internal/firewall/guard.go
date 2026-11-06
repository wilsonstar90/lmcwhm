package firewall

// Guarded ports: database engines published on every address for remote
// access. Docker forwards their IPv4 traffic (DNAT), which never reaches the
// input chain where port rules live, so for these ports the forward chain
// repeats the accept rules (matching the original destination port) and
// drops every other new connection; IPv6 arrives through docker-proxy on the
// input chain, where the same drop follows the rules. Only the sources the
// firewall rules allow get through, whatever the policy.

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// portsCover reports whether a rule's ports ("80,443,8000-9000") include p.
func portsCover(ports string, p int) bool {
	for _, part := range strings.Split(ports, ",") {
		part = strings.TrimSpace(part)
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(strings.TrimSpace(lo))
		if err != nil {
			continue
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(strings.TrimSpace(hi)); err != nil {
				continue
			}
		}
		if p >= a && p <= b {
			return true
		}
	}
	return false
}

// tcpAccept reports whether r lets new TCP connections to p through.
func (r Rule) tcpAccept(p int) bool {
	return r.Enabled && r.Action == "accept" && (r.Proto == "tcp" || r.Proto == "both") && portsCover(r.Ports, p)
}

// guardRules writes the guard for ports into the input (after the rules) and
// forward chains.
func guardRules(rules []Rule, ports []int) (input, forward string) {
	var in, fw strings.Builder
	for _, p := range ports {
		for _, r := range rules {
			if !r.tcpAccept(p) {
				continue
			}
			for _, src := range r.sourceMatches() {
				fmt.Fprintf(&fw, "\t\t%smeta l4proto tcp ct original proto-dst %d ct state new accept comment \"rule %d\"\n", src, p, r.ID)
			}
		}
		fmt.Fprintf(&fw, "\t\tmeta l4proto tcp ct original proto-dst %d ct status dnat ct state new %s\n", p, logDrop("db"+strconv.Itoa(p)))
		fmt.Fprintf(&fw, "\t\tmeta l4proto tcp ct original proto-dst %d ct status dnat ct state new drop comment \"database guard\"\n", p)
		// Containers reach the host over the bridges; leave them be.
		fmt.Fprintf(&in, "\t\tiifname != \"docker0\" iifname != \"br-*\" tcp dport %d ct state new %s\n", p, logDrop("db"+strconv.Itoa(p)))
		fmt.Fprintf(&in, "\t\tiifname != \"docker0\" iifname != \"br-*\" tcp dport %d ct state new drop comment \"database guard\"\n", p)
	}
	return in.String(), fw.String()
}

func (sv *Service) guarded(ctx context.Context) []int {
	if sv.Guarded == nil {
		return nil
	}
	return sv.Guarded(ctx)
}

// ApplyQuiet installs the ruleset without arming the revert timer: for
// changes the panel makes itself that cannot lock anyone out (accept rules,
// guards).
func (sv *Service) ApplyQuiet(ctx context.Context) error {
	c, err := sv.Config(ctx)
	if err != nil {
		return err
	}
	sv.listsChanged()
	if !sv.Available() || !c.Enabled {
		return nil
	}
	return sv.install(ctx, c)
}

// covers reports whether a rule source list ("" = everyone) includes every
// address in want (nil = everyone).
func sourcesCover(have string, want []string) bool {
	if strings.TrimSpace(have) == "" {
		return true
	}
	if len(want) == 0 {
		return false
	}
	var hp []netip.Prefix
	for _, s := range strings.Split(have, ",") {
		if p, _, err := NormIP(s); err == nil {
			hp = append(hp, p)
		}
	}
	for _, w := range want {
		wp, _, err := NormIP(w)
		if err != nil {
			return false
		}
		ok := false
		for _, p := range hp {
			if p.Bits() <= wp.Bits() && p.Contains(wp.Addr()) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// EnsureRule makes sure an enabled TCP accept rule lets sources (nil =
// everyone) reach port. An existing rule that covers them is reused
// (created = false); otherwise one is added with remark. Applied quietly.
func (sv *Service) EnsureRule(ctx context.Context, port int, sources []string, remark string) (id int64, created bool, err error) {
	rules, err := sv.Rules(ctx)
	if err != nil {
		return 0, false, err
	}
	for _, r := range rules {
		if r.tcpAccept(port) && sourcesCover(r.Source, sources) {
			return r.ID, false, sv.ApplyQuiet(ctx)
		}
	}
	r := Rule{Action: "accept", Proto: "tcp", Ports: strconv.Itoa(port), Source: strings.Join(sources, ","), Remark: remark, Enabled: true}
	if err := r.validate(); err != nil {
		return 0, false, err
	}
	res, err := sv.st.DB.ExecContext(ctx, `INSERT INTO fw_rules(action, proto, ports, source, remark, enabled, created_at) VALUES(?,?,?,?,?,?,?)`,
		r.Action, r.Proto, r.Ports, r.Source, r.Remark, r.Enabled, time.Now().Unix())
	if err != nil {
		return 0, false, err
	}
	id, _ = res.LastInsertId()
	return id, true, sv.ApplyQuiet(ctx)
}

// RemoveRuleQuiet deletes a rule the panel added (gone already = fine).
func (sv *Service) RemoveRuleQuiet(ctx context.Context, id int64) error {
	if id == 0 {
		return nil
	}
	if _, err := sv.st.DB.ExecContext(ctx, `DELETE FROM fw_rules WHERE id = ? AND system = ''`, id); err != nil {
		return err
	}
	return sv.ApplyQuiet(ctx)
}
