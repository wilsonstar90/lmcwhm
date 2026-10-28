package firewall

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wilsonstar90/lmcwhm/internal/cfdns"
	"github.com/wilsonstar90/lmcwhm/internal/linode"
)

// SyncReport is the result of pushing the manual lists outwards.
type SyncReport struct {
	At         int64    `json:"at"`
	Cloudflare []string `json:"cloudflare,omitempty"` // per-zone results
	Linode     string   `json:"linode,omitempty"`
	Errors     []string `json:"errors,omitempty"`
}

var (
	reportMu   sync.Mutex
	lastReport SyncReport
)

func LastSync() SyncReport {
	reportMu.Lock()
	defer reportMu.Unlock()
	return lastReport
}

// SyncRemote pushes the manually added, permanent block and allow entries to
// every Cloudflare zone the token can see (when CFSync) and to the chosen
// Linode Cloud Firewall (when LinodeSync / LinodePorts). Bans from CrowdSec,
// MCP and the public lists stay on the server: they churn too fast for the
// APIs' rate limits.
func (sv *Service) SyncRemote(ctx context.Context, cfToken string) SyncReport {
	rep := SyncReport{At: time.Now().Unix()}
	defer func() {
		reportMu.Lock()
		lastReport = rep
		reportMu.Unlock()
	}()
	c, err := sv.Config(ctx)
	if err != nil {
		rep.Errors = append(rep.Errors, err.Error())
		return rep
	}
	blocks, err := sv.ManualIPs(ctx, "block")
	if err != nil {
		rep.Errors = append(rep.Errors, err.Error())
		return rep
	}
	allows, err := sv.ManualIPs(ctx, "allow")
	if err != nil {
		rep.Errors = append(rep.Errors, err.Error())
		return rep
	}
	if c.CFSync && cfToken != "" {
		cf := cfdns.New(cfToken)
		zones, err := cf.Zones(ctx)
		if err != nil {
			rep.Errors = append(rep.Errors, "cloudflare: "+err.Error())
		}
		for _, z := range zones {
			add, del, skipped, err := cf.SyncAccess(ctx, z.ID, blocks, allows)
			if err != nil {
				rep.Errors = append(rep.Errors, z.Name+": "+err.Error())
				continue
			}
			line := z.Name + ": +" + strconv.Itoa(add) + " -" + strconv.Itoa(del)
			if len(skipped) > 0 {
				line += " (not supported by Cloudflare: " + strings.Join(skipped, ", ") + ")"
			}
			rep.Cloudflare = append(rep.Cloudflare, line)
		}
	}
	if (c.LinodeSync || c.LinodePorts) && c.LinodeToken != "" && c.LinodeFirewall > 0 {
		cl := linode.New(c.LinodeToken)
		rules, err := cl.Rules(ctx, c.LinodeFirewall)
		if err != nil {
			rep.Errors = append(rep.Errors, err.Error())
			return rep
		}
		var lb, la []string
		if c.LinodeSync {
			lb, la = blocks, allows
		}
		ports := ""
		if c.LinodePorts {
			ports = strings.ReplaceAll(joinInts(sv.ProtectedPorts()), " ", "")
		}
		next, overflow := linode.ApplyManaged(rules, lb, la, ports)
		if err := cl.PutRules(ctx, c.LinodeFirewall, next); err != nil {
			rep.Errors = append(rep.Errors, err.Error())
			return rep
		}
		rep.Linode = "blocked " + strconv.Itoa(min(len(lb), linode.MaxAddresses)) + ", allowed " + strconv.Itoa(min(len(la), linode.MaxAddresses))
		if len(overflow) > 0 {
			rep.Linode += ", " + strconv.Itoa(len(overflow)) + " over Linode's per-rule limit (kept on the server only)"
		}
	}
	return rep
}
