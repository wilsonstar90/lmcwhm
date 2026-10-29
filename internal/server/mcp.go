package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/wilsonstar90/lmcwhm/internal/ai"
	"github.com/wilsonstar90/lmcwhm/internal/apitoken"
	"github.com/wilsonstar90/lmcwhm/internal/backup"
	"github.com/wilsonstar90/lmcwhm/internal/config"
	"github.com/wilsonstar90/lmcwhm/internal/logcenter"
	"github.com/wilsonstar90/lmcwhm/internal/mcp"
	"github.com/wilsonstar90/lmcwhm/internal/sysinfo"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
)

const mcpInstructions = `lmcwhm manages this Linux server: websites behind an HAProxy SNI edge and nginx (+ ModSecurity WAF), PHP-FPM, runtime projects, Docker containers, App Store databases, an nftables firewall synced to Linode / Cloudflare, CrowdSec, cron jobs and backups.
Start with server_overview or list_sites. Read tools are safe; operate tools change state (restart, ban, run jobs); admin tools can run shell commands. Prefer the narrowest tool. Prompts (skills) describe common investigations.`

// MCPSocket is the root-only unix socket that "lmcwhm mcp --stdio" bridges to.
func MCPSocket(p config.Paths) string { return filepath.Join(p.DataDir, "mcp.sock") }

type mcpCallerKey struct{}

// buildMCP registers every tool over the panel's services.
func (s *Server) buildMCP() *mcp.Server {
	m := mcp.NewServer("lmcwhm", config.Version, mcpInstructions)
	m.OnCall = func(ctx context.Context, c mcp.Caller, tool string, args json.RawMessage, err error) {
		detail := tool + " " + string(args)
		if len(detail) > 500 {
			detail = detail[:500]
		}
		if err != nil {
			detail += " → error: " + err.Error()
		}
		s.Audit.Record(ctx, c.Name, c.IP, "mcp", "mcp.tool", detail)
	}
	read, operate, admin := mcp.ScopeRead, mcp.ScopeOperate, mcp.ScopeAdmin
	O, S, I, B := mcp.Object, mcp.Str, mcp.Int, mcp.Bool

	add := func(scope, name, title, desc string, schema map[string]any, destructive bool, h func(context.Context, json.RawMessage) (any, error)) {
		m.AddTool(mcp.Tool{Name: name, Title: title, Description: desc, Schema: schema, Scope: scope, Destructive: destructive, Handler: h})
	}
	noArgs := func(f func(ctx context.Context) (any, error)) func(context.Context, json.RawMessage) (any, error) {
		return func(ctx context.Context, raw json.RawMessage) (any, error) {
			var v struct{}
			if err := mcp.Args(raw, &v); err != nil {
				return nil, err
			}
			return f(ctx)
		}
	}

	add(read, "server_overview", "Server overview", "Host, OS, CPU/memory/swap/disk usage, load and uptime.", nil, false,
		noArgs(func(ctx context.Context) (any, error) { return sysinfo.Collect(ctx), nil }))
	if s.Monitor != nil {
		add(read, "metrics", "Resource history", "CPU %, memory %, load, disk and network rates over a time range (averaged points).",
			O(map[string]any{"range": S("time range", "15m", "1h", "6h", "24h", "7d", "30d")}, "range"), false,
			func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct{ Range string }
				if err := mcp.Args(raw, &a); err != nil {
					return nil, err
				}
				d, ok := ranges[a.Range]
				if !ok {
					return nil, mcp.ErrArgs
				}
				pts, err := s.Monitor.Series(ctx, d)
				if err != nil {
					return nil, err
				}
				// Thin long series: the model needs the shape, not every sample.
				if step := len(pts) / 120; step > 1 {
					var thin = pts[:0:0]
					for i := 0; i < len(pts); i += step {
						thin = append(thin, pts[i])
					}
					pts = thin
				}
				return map[string]any{"points": pts}, nil
			})
	}
	if s.Sites != nil {
		add(read, "list_sites", "List websites", "Websites with type, domains, SSL state and expiry, WAF state and project settings.", nil, false,
			noArgs(func(ctx context.Context) (any, error) { return s.Sites.List(ctx) }))
		add(read, "site_logs", "Website log lines", "Last lines of a website's nginx access or error log, optionally filtered by a keyword or IP.",
			O(map[string]any{"domain": S("site domain"), "kind": S("which log", "access", "error"), "lines": I("number of lines", 1, 2000), "filter": S("keyword or IP")}, "domain", "kind"), false,
			func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Domain, Kind, Filter string
					Lines                int
				}
				if err := mcp.Args(raw, &a); err != nil {
					return nil, err
				}
				p, err := s.siteLogPath(ctx, a.Domain, a.Kind)
				if err != nil {
					return nil, err
				}
				lines, err := logcenter.Tail(p, clampInt(a.Lines, 200, 2000), a.Filter)
				if err != nil {
					return nil, err
				}
				return strings.Join(lines, "\n"), nil
			})
		add(read, "analyze_access_log", "Access log statistics", "Summarises recent requests of a site: status codes, top IPs (with country), paths, user agents, 4xx/5xx paths and suspicious requests (scanners, SQL injection, path traversal).",
			O(map[string]any{"domain": S("site domain"), "lines": I("recent lines to analyse", 100, 50000)}, "domain"), false,
			func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Domain string
					Lines  int
				}
				if err := mcp.Args(raw, &a); err != nil {
					return nil, err
				}
				p, err := s.siteLogPath(ctx, a.Domain, "access")
				if err != nil {
					return nil, err
				}
				lines, err := logcenter.Tail(p, clampInt(a.Lines, 5000, 50000), "")
				if err != nil {
					return nil, err
				}
				return s.accessStats(lines), nil
			})
		add(operate, "restart_project", "Restart a runtime project", "Restarts the container of a Node / Python / Java project site.",
			O(map[string]any{"domain": S("site domain")}, "domain"), false,
			func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct{ Domain string }
				if err := mcp.Args(raw, &a); err != nil {
					return nil, err
				}
				id, err := s.siteID(ctx, a.Domain)
				if err != nil {
					return nil, err
				}
				return "restarted", s.Sites.RestartProject(ctx, id)
			})
		add(operate, "issue_certificate", "Issue / renew SSL", "Requests a Let's Encrypt (or configured CA) certificate for a site; runs in the background.",
			O(map[string]any{"domain": S("site domain"), "challenge": S("validation", "http", "dns"), "wildcard": B("also cover *.domain (needs dns)")}, "domain"), false,
			func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Domain, Challenge string
					Wildcard          bool
				}
				if err := mcp.Args(raw, &a); err != nil {
					return nil, err
				}
				if a.Challenge == "" {
					a.Challenge = "http"
				}
				id, err := s.siteID(ctx, a.Domain)
				if err != nil {
					return nil, err
				}
				task, err := s.Sites.IssueCert(ctx, id, a.Challenge, a.Wildcard)
				return map[string]string{"task_id": task, "note": "check progress with get_task"}, err
			})
	}
	add(read, "get_task", "Background task status", "Status and log of a background task started by another tool.",
		O(map[string]any{"task_id": S("task id")}, "task_id"), false,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a struct {
				TaskID string `json:"task_id"`
			}
			if err := mcp.Args(raw, &a); err != nil {
				return nil, err
			}
			t, ok := s.Tasks.Get(a.TaskID)
			if !ok {
				return nil, errors.New("no such task")
			}
			return t, nil
		})
	if s.Docker != nil {
		add(read, "list_containers", "List containers", "All Docker containers with state, image, ports and live CPU / memory.", nil, false,
			noArgs(func(ctx context.Context) (any, error) { return s.Docker.Containers(ctx, true) }))
		add(read, "container_logs", "Container logs", "Last lines of a container's stdout/stderr.",
			O(map[string]any{"name": S("container name or id"), "lines": I("number of lines", 1, 2000)}, "name"), false,
			func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Name  string
					Lines int
				}
				if err := mcp.Args(raw, &a); err != nil {
					return nil, err
				}
				return s.Docker.Logs(ctx, a.Name, clampInt(a.Lines, 200, 2000))
			})
		add(operate, "container_action", "Start / stop / restart a container", "Starts, stops or restarts a container.",
			O(map[string]any{"name": S("container name or id"), "action": S("action", "start", "stop", "restart")}, "name", "action"), true,
			func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct{ Name, Action string }
				if err := mcp.Args(raw, &a); err != nil {
					return nil, err
				}
				if a.Action != "start" && a.Action != "stop" && a.Action != "restart" {
					return nil, mcp.ErrArgs
				}
				return a.Action + " done", s.Docker.Action(ctx, a.Name, a.Action)
			})
	}
	if s.DBs != nil {
		add(read, "list_databases", "List databases", "Databases created in the panel with engine, version, user and port (no passwords).", nil, false,
			noArgs(func(ctx context.Context) (any, error) {
				dbs, err := s.DBs.List(ctx)
				for i := range dbs {
					dbs[i].Password = ""
				}
				return dbs, err
			}))
	}
	if s.Apps != nil {
		add(read, "list_apps", "Installed App Store apps", "Installed runtimes, database engines and tools with version, port and container state.", nil, false,
			noArgs(func(ctx context.Context) (any, error) { return s.Apps.List(ctx) }))
		add(read, "app_updates", "Software updates", "Exact versions of installed apps and PHP versions and whether a patch update of the same version line is available (from the last daily check).", nil, false,
			noArgs(func(ctx context.Context) (any, error) { return s.Apps.Updates(ctx), nil }))
	}
	if s.Firewall != nil {
		add(read, "firewall_status", "Firewall status", "Firewall on/off, default policy, port rules and the number of blocked / allowed IPs.", nil, false,
			noArgs(func(ctx context.Context) (any, error) {
				c, err := s.Firewall.Config(ctx)
				if err != nil {
					return nil, err
				}
				rules, _ := s.Firewall.Rules(ctx)
				blocks, _ := s.Firewall.IPs(ctx, "block")
				allows, _ := s.Firewall.IPs(ctx, "allow")
				return map[string]any{"enabled": c.Enabled, "policy": c.Policy, "ping": c.Ping, "protected_ports": s.Firewall.ProtectedPorts(),
					"rules": rules, "blocked_ips": len(blocks), "allowed_ips": len(allows), "linode_sync": c.LinodeSync, "cloudflare_sync": c.CFSync}, nil
			}))
		add(read, "list_ip_rules", "Blocked / allowed IPs", "IP block or allow list entries with source (manual, crowdsec, mcp) and expiry.",
			O(map[string]any{"list": S("which list", "block", "allow")}, "list"), false,
			func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct{ List string }
				if err := mcp.Args(raw, &a); err != nil {
					return nil, err
				}
				ips, err := s.Firewall.IPs(ctx, a.List)
				if len(ips) > 500 {
					ips = ips[:500]
				}
				return ips, err
			})
		add(operate, "ban_ip", "Block an IP", "Adds an IP or CIDR to the firewall block list (also synced to Linode / Cloudflare when enabled).",
			O(map[string]any{"ip": S("IP address or CIDR"), "minutes": I("block duration; 0 = permanent", 0, 525600), "reason": S("why")}, "ip"), true,
			func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					IP, Reason string
					Minutes    int
				}
				if err := mcp.Args(raw, &a); err != nil {
					return nil, err
				}
				return s.Firewall.AddIP(ctx, a.IP, "block", time.Duration(a.Minutes)*time.Minute, "mcp", a.Reason)
			})
		add(operate, "unban_ip", "Unblock an IP", "Removes an IP or CIDR from the block list.",
			O(map[string]any{"ip": S("IP address or CIDR")}, "ip"), false,
			func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct{ IP string }
				if err := mcp.Args(raw, &a); err != nil {
					return nil, err
				}
				return "removed", s.Firewall.RemoveIP(ctx, a.IP, "block")
			})
	}
	if s.WAF != nil {
		add(read, "waf_events", "WAF events", "Recent ModSecurity detections and blocks: IP, host, request and matched CRS rules.",
			O(map[string]any{"lines": I("audit log entries to read", 1, 2000), "filter": S("keyword, IP or host")}), false,
			func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Lines  int
					Filter string
				}
				if err := mcp.Args(raw, &a); err != nil {
					return nil, err
				}
				return s.WAF.Events(clampInt(a.Lines, 200, 2000), a.Filter)
			})
	}
	if s.CrowdSec != nil {
		add(read, "crowdsec_alerts", "CrowdSec alerts", "Recent CrowdSec alerts (scenario, source IP, decisions).",
			O(map[string]any{"limit": I("number of alerts", 1, 200)}), false,
			func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct{ Limit int }
				if err := mcp.Args(raw, &a); err != nil {
					return nil, err
				}
				res, err := s.CrowdSec.Alerts(ctx, clampInt(a.Limit, 50, 200))
				return json.RawMessage(res), err
			})
	}
	add(read, "ssh_logins", "SSH logins", "Recent SSH logins: accepted / failed counts, top failing IPs and events.",
		O(map[string]any{"lines": I("auth log lines to read", 1, 20000), "filter": S("user or IP")}), false,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a struct {
				Lines  int
				Filter string
			}
			if err := mcp.Args(raw, &a); err != nil {
				return nil, err
			}
			sum := logcenter.SSH(ctx, clampInt(a.Lines, 2000, 20000), a.Filter)
			if len(sum.Items) > 100 {
				sum.Items = sum.Items[len(sum.Items)-100:]
			}
			return sum, nil
		})
	add(read, "panel_logins", "Panel logins", "Recent panel login attempts (success, IP, reason).",
		O(map[string]any{"limit": I("entries", 1, 500)}), false,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a struct{ Limit int }
			if err := mcp.Args(raw, &a); err != nil {
				return nil, err
			}
			items, _, err := s.Audit.ListLogins(ctx, clampInt(a.Limit, 50, 500), 0)
			return items, err
		})
	add(read, "system_journal", "System journal", "Last lines of the systemd journal, optionally of one unit (e.g. lmcwhm, docker, ssh).",
		O(map[string]any{"unit": S("systemd unit; empty = all"), "lines": I("lines", 1, 2000), "filter": S("keyword")}), false,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a struct {
				Unit, Filter string
				Lines        int
			}
			if err := mcp.Args(raw, &a); err != nil {
				return nil, err
			}
			lines, err := logcenter.Journal(ctx, a.Unit, clampInt(a.Lines, 200, 2000), a.Filter)
			return strings.Join(lines, "\n"), err
		})
	if s.Cron != nil {
		add(read, "list_cron_jobs", "Cron jobs", "Scheduled jobs with schedule, next run and the last run's status.", nil, false,
			noArgs(func(ctx context.Context) (any, error) { return s.Cron.List(ctx) }))
		add(read, "cron_run_output", "Cron run output", "Output of one cron run (run ids come from list_cron_jobs' last run).",
			O(map[string]any{"run_id": I("run id", 1, 1<<31-1)}, "run_id"), false,
			func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					RunID int64 `json:"run_id"`
				}
				if err := mcp.Args(raw, &a); err != nil {
					return nil, err
				}
				return s.Cron.Run(ctx, a.RunID)
			})
		add(operate, "run_cron_job", "Run a cron job now", "Starts a cron job immediately in the background.",
			O(map[string]any{"id": I("job id", 1, 1<<31-1)}, "id"), false,
			func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct{ ID int64 }
				if err := mcp.Args(raw, &a); err != nil {
					return nil, err
				}
				return "started", s.Cron.RunNow(ctx, a.ID)
			})
	}
	if s.Backup != nil {
		add(read, "list_backups", "Backups", "Stored backups (kind, target, size, destination, time).",
			O(map[string]any{"kind": S("filter by kind", "site", "database", "panel", "volume", "path")}), false,
			func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct{ Kind string }
				if err := mcp.Args(raw, &a); err != nil {
					return nil, err
				}
				recs, err := s.Backup.List(ctx, a.Kind)
				if len(recs) > 200 {
					recs = recs[:200]
				}
				return recs, err
			})
		add(operate, "run_backup", "Back up now", "Backs up a site (domain), database (id from list_databases), the panel, a Docker volume or a directory; runs in the background.",
			O(map[string]any{"kind": S("what", "site", "database", "panel", "volume", "path"), "target": S("domain / database id / volume / absolute path; '*' = all sites or databases"),
				"dests": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "\"local\" and/or \"remote:<id>\"; default local"}}, "kind"), false,
			func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Kind, Target string
					Dests        []string
				}
				if err := mcp.Args(raw, &a); err != nil {
					return nil, err
				}
				if len(a.Dests) == 0 {
					a.Dests = []string{backup.DestLocal}
				}
				b, _ := json.Marshal(backup.Plan{Kind: a.Kind, Target: a.Target, Dests: a.Dests})
				plan, err := s.Backup.ParsePlan(ctx, string(b))
				if err != nil {
					return nil, err
				}
				id := s.Tasks.StartLong("backup "+plan.Kind+" "+plan.Target, backupTimeout, func(ctx context.Context, log tasks.Logf) error {
					return s.Backup.Run(ctx, plan, 0, logWriter(log))
				})
				return map[string]string{"task_id": id}, nil
			})
	}
	add(admin, "run_shell", "Run a shell command", "Runs a bash command as root on the host and returns its output (exit code included). Use only when no other tool fits.",
		O(map[string]any{"command": S("bash command"), "timeout": I("seconds", 1, 600)}, "command"), true,
		func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a struct {
				Command string
				Timeout int
			}
			if err := mcp.Args(raw, &a); err != nil {
				return nil, err
			}
			return runShell(ctx, a.Command, time.Duration(clampInt(a.Timeout, 60, 600))*time.Second), nil
		})

	if s.Probes != nil {
		add(read, "list_probes", "Uptime probes", "Uptime checks with state (up / down), latency, last error, certificate days left and 24h / 7d uptime.", nil, false,
			noArgs(func(ctx context.Context) (any, error) { return s.Probes.List(ctx) }))
	}
	if s.Stats != nil {
		add(read, "site_report", "Website traffic report", "PV, UV, bandwidth, status codes, spiders, top URLs / IPs / referrers and countries of a site over the last days.",
			O(map[string]any{"domain": S("site domain"), "days": I("days (1 = today)", 1, 30)}, "domain"), false,
			func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct {
					Domain string
					Days   int
				}
				if err := mcp.Args(raw, &a); err != nil {
					return nil, err
				}
				var country func(string) string
				if s.GeoIP != nil {
					country = func(ip string) string { return s.GeoIP.Lookup(ip).Country }
				}
				rep, err := s.Stats.Report(ctx, a.Domain, clampInt(a.Days, 1, 30), country)
				rep.Series = nil // the totals and tops are what matter here
				return rep, err
			})
	}
	if s.Alerts != nil {
		add(read, "alert_events", "Alert history", "Recent alerts sent by the alert center.", O(map[string]any{"limit": I("entries", 1, 200)}), false,
			func(ctx context.Context, raw json.RawMessage) (any, error) {
				var a struct{ Limit int }
				if err := mcp.Args(raw, &a); err != nil {
					return nil, err
				}
				list, _, err := s.Alerts.Events(ctx, clampInt(a.Limit, 50, 200), 0)
				return list, err
			})
	}
	addPrompts(m)
	return m
}

func clampInt(v, def, max int) int {
	if v <= 0 {
		return def
	}
	return min(v, max)
}

func (s *Server) siteID(ctx context.Context, domain string) (int64, error) {
	list, err := s.Sites.List(ctx)
	if err != nil {
		return 0, err
	}
	for _, x := range list {
		if x.Domain == domain {
			return x.ID, nil
		}
	}
	return 0, fmt.Errorf("no site %q", domain)
}

// runShell runs a command in its own process group so a timeout kills it all.
func runShell(ctx context.Context, command string, timeout time.Duration) string {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.Command("/bin/bash", "-c", command)
	cmd.Dir = "/"
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return "error: " + err.Error()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		err = fmt.Errorf("timed out after %s", timeout)
	}
	text := out.String()
	if len(text) > 100<<10 {
		text = "[… truncated]\n" + text[len(text)-100<<10:]
	}
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		return text + "\n[" + err.Error() + "]"
	}
	return fmt.Sprintf("%s\n[exit %d]", text, code)
}

// ---- access log statistics ----

var suspicious = []string{"union select", "union%20select", "../", "..%2f", "/etc/passwd", "<script", "%3cscript", "sleep(", "benchmark(",
	"information_schema", "wp-login.php", "xmlrpc.php", "/.env", "/.git", "phpmyadmin", "/cgi-bin/", "eval(", "base64_decode", "cmd=", "/actuator"}

type kv struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

func top(m map[string]int, n int) []kv {
	out := make([]kv, 0, len(m))
	for k, v := range m {
		out = append(out, kv{k, v})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Count > out[j].Count || out[i].Count == out[j].Count && out[i].Key < out[j].Key
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func (s *Server) accessStats(lines []string) map[string]any {
	status, ips, paths, uas, errPaths := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	var first, last int64
	var bytesSent int64
	var sus []string
	for _, l := range lines {
		a := logcenter.ParseAccess(l)
		if a.Raw != "" {
			continue
		}
		if first == 0 || a.Time < first {
			first = a.Time
		}
		last = max(last, a.Time)
		bytesSent += a.Bytes
		status[strconv.Itoa(a.Status)]++
		ips[a.IP]++
		p, _, _ := strings.Cut(a.Path, "?")
		paths[p]++
		uas[a.UA]++
		if a.Status >= 400 {
			errPaths[strconv.Itoa(a.Status)+" "+p]++
		}
		low := strings.ToLower(a.Path)
		for _, pat := range suspicious {
			if strings.Contains(low, pat) {
				if len(sus) < 50 {
					sus = append(sus, fmt.Sprintf("%s %d %s %s", a.IP, a.Status, a.Method, a.Path))
				}
				break
			}
		}
	}
	topIPs := []map[string]any{}
	for _, x := range top(ips, 15) {
		e := map[string]any{"ip": x.Key, "requests": x.Count}
		if s.GeoIP != nil {
			info := s.GeoIP.Lookup(x.Key)
			e["country"], e["org"] = info.Country, info.Org
		}
		topIPs = append(topIPs, e)
	}
	return map[string]any{
		"requests": len(lines), "from": time.Unix(first, 0).Format(time.RFC3339), "to": time.Unix(last, 0).Format(time.RFC3339),
		"bytes_sent": bytesSent, "unique_ips": len(ips), "status": top(status, 20), "top_ips": topIPs,
		"top_paths": top(paths, 15), "top_user_agents": top(uas, 10), "error_paths": top(errPaths, 15), "suspicious": sus,
	}
}

// ---- HTTP transport ----

// handleMCP serves Streamable HTTP with JSON responses. The caller comes
// from the bearer token (or the root-only unix socket).
func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	c, ok := r.Context().Value(mcpCallerKey{}).(mcp.Caller)
	if !ok {
		notFound(w)
		return
	}
	switch r.Method {
	case http.MethodPost:
	case http.MethodGet, http.MethodDelete:
		// No server-initiated stream and no sessions.
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if o := r.Header.Get("Origin"); o != "" && !sameOrigin(o, r.Host) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil {
		http.Error(w, "too large", http.StatusRequestEntityTooLarge)
		return
	}
	out := s.MCP.Handle(r.Context(), c, body)
	if out == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}

func sameOrigin(origin, host string) bool {
	_, after, ok := strings.Cut(origin, "://")
	return ok && strings.EqualFold(after, host)
}

// mcpAuth resolves the bearer token for /mcp; failures look like any unknown path.
func (s *Server) mcpAuth(w http.ResponseWriter, r *http.Request) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || s.Tokens == nil {
		notFound(w)
		return
	}
	t, err := s.Tokens.Verify(r.Context(), strings.TrimSpace(tok), clientIP(r))
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="lmcwhm"`)
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	c := mcp.Caller{Name: "token:" + t.Name, Scope: t.Scope, IP: clientIP(r)}
	s.handleMCP(w, r.WithContext(context.WithValue(r.Context(), mcpCallerKey{}, c)))
}

// ServeMCPSocket serves /mcp with admin scope on a root-only unix socket for
// "lmcwhm mcp --stdio" (reached over SSH, so the OS account is the credential).
func (s *Server) ServeMCPSocket(ctx context.Context, path string) error {
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	os.Chmod(path, 0o600)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := mcp.Caller{Name: "cli:stdio", Scope: mcp.ScopeAdmin, IP: "local"}
		r.Header.Del("Origin")
		s.handleMCP(w, r.WithContext(context.WithValue(r.Context(), mcpCallerKey{}, c)))
	}), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Close()
		os.Remove(path)
	}()
	err = srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// ---- panel API: tokens, MCP info, AI assistant ----

func (s *Server) m7Routes(r chi.Router) {
	r.Get("/mcp/info", s.handleMCPInfo)
	r.Get("/mcp/skill", s.handleSkill)
	if s.Tokens != nil {
		r.Get("/tokens", s.handleTokens)
		r.Post("/tokens", s.handleTokenCreate)
		r.Post("/tokens/{id}/delete", s.handleTokenDelete)
	}
	if s.AI != nil {
		r.Get("/ai/config", s.handleAIConfig)
		r.Put("/ai/config", s.handleAISave)
		r.Post("/ai/ask", s.handleAIAsk)
	}
}

func (s *Server) handleMCPInfo(w http.ResponseWriter, r *http.Request) {
	type tool struct {
		Name        string `json:"name"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Scope       string `json:"scope"`
		Destructive bool   `json:"destructive"`
	}
	tools := []tool{}
	for _, t := range s.MCP.Tools(mcp.ScopeAdmin) {
		tools = append(tools, tool{t.Name, t.Title, t.Description, t.Scope, t.Destructive})
	}
	prompts := []map[string]any{}
	for _, p := range s.MCP.Prompts() {
		prompts = append(prompts, map[string]any{"name": p.Name, "title": p.Title, "description": p.Description, "args": p.Args})
	}
	cfg := s.Settings.Get()
	writeJSON(w, http.StatusOK, map[string]any{"tools": tools, "prompts": prompts, "port": cfg.PanelPort, "tls": cfg.SSLMode != "off"})
}

func (s *Server) handleTokens(w http.ResponseWriter, r *http.Request) {
	list, err := s.Tokens.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
}

func (s *Server) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	var in apitoken.Input
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	t, secret, err := s.Tokens.Create(r.Context(), in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "mcp", "token.create", t.Name+" ("+t.Scope+")")
	writeJSON(w, http.StatusOK, map[string]any{"token": t, "secret": secret})
}

func (s *Server) handleTokenDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.Tokens.Delete(r.Context(), idParam(r)); err != nil {
		writeCodedErr(w, err, apitoken.ErrNotFound)
		return
	}
	s.auditCat(r, "mcp", "token.delete", chi.URLParam(r, "id"))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAIConfig(w http.ResponseWriter, r *http.Request) {
	c, err := s.AI.Config(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"model": c.Model, "effort": c.Effort, "base_url": c.BaseURL, "has_key": c.APIKey != "", "default_model": ai.DefaultModel})
}

func (s *Server) handleAISave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ai.Config
		ClearKey bool `json:"clear_key"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.AI.Save(r.Context(), in.Config, in.ClearKey); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.auditCat(r, "mcp", "ai.config", in.Model)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAIAsk(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Turns []ai.Turn `json:"turns"`
	}
	if err := decode(r, &in); err != nil || len(in.Turns) == 0 || len(in.Turns) > 40 {
		writeErr(w, http.StatusBadRequest, errBadRequest)
		return
	}
	c := mcp.Caller{Name: "ai:" + sessionFrom(r).User.Username, IP: clientIP(r)}
	ans, err := s.AI.Ask(r.Context(), in.Turns, s.Settings.Get().Language, c)
	if err != nil {
		if errors.Is(err, ai.ErrNoKey) || errors.Is(err, ai.ErrRefused) || errors.Is(err, ai.ErrConfig) {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusBadGateway, apiError{Code: "ai.failed", Params: map[string]any{"value": err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, ans)
}
