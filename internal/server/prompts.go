package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/wilsonstar90/lmcwhm/internal/mcp"
)

// Skills: reusable investigations, exposed as MCP prompts and used by the
// panel's one-click AI analysis buttons.
var skills = []mcp.Prompt{
	{
		Name: "diagnose_site", Title: "Diagnose a website",
		Description: "Find why a website is down, slow or erroring (5xx, 404, SSL, PHP / project crashes).",
		Args:        []mcp.PromptArg{{Name: "domain", Description: "site domain", Required: true}},
		Render: func(a map[string]string) string {
			return fmt.Sprintf(`Diagnose the website %[1]s on this lmcwhm server.
1. list_sites: find %[1]s — its type (php, static, proxy, node/python/java project), whether it is stopped, SSL state and certificate expiry.
2. analyze_access_log for %[1]s: status code mix, error paths, request rate, top IPs.
3. site_logs kind=error for %[1]s (last 200 lines): nginx / PHP errors, upstream failures.
4. For a project site: container_logs of lmcwhm-proj-%[1]s; for proxy sites check the upstream in the error log; for PHP check the lmcwhm-php* container logs.
5. server_overview: rule out disk full, memory pressure or load.
Report: the most likely cause with evidence (quote log lines), then concrete fixes in order. Say what you could not verify.`, a["domain"])
		},
	},
	{
		Name: "security_triage", Title: "Security triage",
		Description: "Review recent attacks and exposure: WAF, CrowdSec, SSH and panel logins, firewall — and what to block.",
		Render: func(map[string]string) string {
			return `Do a security triage of this lmcwhm server.
1. firewall_status: is the firewall on, which ports are open, is Linode / Cloudflare sync on?
2. ssh_logins: failed logins, top attacking IPs, any accepted logins from unusual IPs.
3. panel_logins: failed panel logins and their IPs.
4. waf_events (if the WAF is on) and crowdsec_alerts (if installed): attack types, sources, targeted sites.
5. For the busiest site(s) from list_sites, analyze_access_log and look at the "suspicious" requests.
Report: a short risk summary (critical / warning / ok), the IPs or networks worth blocking with reasons (do not block them yourself unless asked), and hardening steps (e.g. enable firewall drop policy, WAF, 2FA, security entry, CrowdSec).`
		},
	},
	{
		Name: "tune_waf", Title: "Tune WAF false positives",
		Description: "Separate real attacks from false positives in ModSecurity events and propose per-site rule exclusions.",
		Args:        []mcp.PromptArg{{Name: "domain", Description: "site domain (optional)"}},
		Render: func(a map[string]string) string {
			scope := "all sites"
			if d := strings.TrimSpace(a["domain"]); d != "" {
				scope = "the site " + d + " (filter waf_events by it)"
			}
			return `Tune the OWASP CRS WAF for ` + scope + `.
1. waf_events (500 entries): group by rule id, host and path.
2. For each frequent rule decide: real attack (scanner IPs, injection payloads, many paths) or false positive (logged-in users, normal app endpoints, legitimate payloads such as rich-text editors or JSON APIs).
3. For false positives propose the narrowest fix: excluding that rule id for the site (Security → WAF → excluded rules), or turning the WAF off only for an API-only site. Never propose lowering protection globally first.
Report a table: rule id, what it matched, verdict, recommended action.`
		},
	},
	{
		Name: "high_load", Title: "Investigate high load",
		Description: "Explain high CPU, memory, disk or network usage and what is causing it.",
		Render: func(map[string]string) string {
			return `Investigate resource usage on this lmcwhm server.
1. server_overview and metrics range=24h: when did usage rise, which resource?
2. list_containers: CPU / memory per container.
3. If a website is the source, analyze_access_log for it: crawler or attack traffic (top IPs and user agents)?
4. list_cron_jobs: jobs running at the times of the spikes.
5. If still unclear and you have the admin scope, run_shell with "ps aux --sort=-%cpu | head -20" or "df -h".
Report the cause, the evidence and the fix (block abusive IPs, limit a container, move a cron job, add swap, upgrade the plan).`
		},
	},
	{
		Name: "backup_review", Title: "Review backups",
		Description: "Check that every site and database has recent backups, off-site copies and working schedules.",
		Render: func(map[string]string) string {
			return `Review the backup coverage of this lmcwhm server.
1. list_sites and list_databases: what exists.
2. list_backups: the latest backup per site / database, its age, size and destination (local vs remote).
3. list_cron_jobs: backup jobs (type "backup"), their schedule and last run status; read failing runs with cron_run_output.
Report: items with no backup or none in the last 2 days, items with no off-site (remote) copy, failing jobs and why, and a recommended schedule.`
		},
	},
}

func addPrompts(m *mcp.Server) {
	for _, p := range skills {
		m.AddPrompt(p)
	}
}

// handleSkill returns a Claude Code skill (SKILL.md) that teaches an agent
// how to operate this server through the lmcwhm MCP tools.
func (s *Server) handleSkill(w http.ResponseWriter, r *http.Request) {
	var b strings.Builder
	b.WriteString(`---
name: lmcwhm-ops
description: Operate and troubleshoot a server managed by the lmcwhm panel through its MCP tools (websites, SSL, Docker, databases, firewall, WAF, CrowdSec, cron, backups). Use when asked about this server's sites, errors, attacks, load or backups.
---

# lmcwhm server operations

The lmcwhm MCP server exposes this host. Its tools are grouped by scope: read tools are always safe; operate tools change state (restart, block IPs, run jobs, start backups, issue certificates); admin tools run shell commands as root.

## Ground rules
- Investigate with read tools first and quote evidence (log lines, counts, IPs).
- Before an operate or admin tool, say what you will change and why; prefer the narrowest action (unban_ip exists, blocks can expire with minutes).
- Never block the admin's own IP, the panel port, SSH or 80/443 for everyone.
- Long actions (issue_certificate, run_backup) return a task id: follow it with get_task.

## Layout
- Sites live in /www/wwwroot/<domain>; nginx logs in /www/wwwlogs/<domain>.log and <domain>.error.log.
- Containers: lmcwhm-haproxy (edge, ports 80/443), lmcwhm-nginx, lmcwhm-php<ver>, lmcwhm-proj-<domain> (runtime projects), App Store engines.
- Panel home /opt/lmcwhm; CLI: lmcwhm (status, restart, firewall-off, backup list).

## Playbooks
`)
	for _, p := range skills {
		args := map[string]string{"domain": "<domain>"}
		fmt.Fprintf(&b, "\n### %s (`%s`)\n%s\n\n%s\n", p.Title, p.Name, p.Description, p.Render(args))
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="SKILL.md"`)
	w.Write([]byte(b.String()))
}
