// Command lmcwhm is the panel server and its aaPanel "bt"-style admin CLI.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/wilsonstar90/lmcwhm/internal/acme"
	"github.com/wilsonstar90/lmcwhm/internal/ai"
	"github.com/wilsonstar90/lmcwhm/internal/alert"
	"github.com/wilsonstar90/lmcwhm/internal/apitoken"
	"github.com/wilsonstar90/lmcwhm/internal/appstore"
	"github.com/wilsonstar90/lmcwhm/internal/audit"
	"github.com/wilsonstar90/lmcwhm/internal/auth"
	"github.com/wilsonstar90/lmcwhm/internal/backup"
	"github.com/wilsonstar90/lmcwhm/internal/config"
	"github.com/wilsonstar90/lmcwhm/internal/cron"
	"github.com/wilsonstar90/lmcwhm/internal/database"
	"github.com/wilsonstar90/lmcwhm/internal/dockermgr"
	"github.com/wilsonstar90/lmcwhm/internal/files"
	"github.com/wilsonstar90/lmcwhm/internal/fileshare"
	"github.com/wilsonstar90/lmcwhm/internal/firewall"
	"github.com/wilsonstar90/lmcwhm/internal/ftp"
	"github.com/wilsonstar90/lmcwhm/internal/geoip"
	"github.com/wilsonstar90/lmcwhm/internal/i18n"
	"github.com/wilsonstar90/lmcwhm/internal/mail"
	"github.com/wilsonstar90/lmcwhm/internal/malware"
	"github.com/wilsonstar90/lmcwhm/internal/monitor"
	"github.com/wilsonstar90/lmcwhm/internal/netinfo"
	"github.com/wilsonstar90/lmcwhm/internal/probe"
	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/secscan"
	"github.com/wilsonstar90/lmcwhm/internal/security"
	"github.com/wilsonstar90/lmcwhm/internal/server"
	"github.com/wilsonstar90/lmcwhm/internal/settings"
	"github.com/wilsonstar90/lmcwhm/internal/site"
	"github.com/wilsonstar90/lmcwhm/internal/sitestats"
	"github.com/wilsonstar90/lmcwhm/internal/store"
	"github.com/wilsonstar90/lmcwhm/internal/supervisor"
	"github.com/wilsonstar90/lmcwhm/internal/tamper"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
	"github.com/wilsonstar90/lmcwhm/internal/terminal"
	"github.com/wilsonstar90/lmcwhm/internal/toolbox"
	"github.com/wilsonstar90/lmcwhm/internal/webcfg"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
	"github.com/wilsonstar90/lmcwhm/internal/wordpress"
	"github.com/wilsonstar90/lmcwhm/web"
)

// app bundles what every subcommand needs once the database is open.
type app struct {
	paths    config.Paths
	st       *store.Store
	settings *settings.Manager
	auth     *auth.Service
	audit    *audit.Logger
	tr       i18n.Translator
}

func openApp(ctx context.Context) (*app, error) {
	p := config.Resolve()
	if err := p.Ensure(); err != nil {
		return nil, err
	}
	st, err := store.Open(p.DBFile)
	if err != nil {
		return nil, err
	}
	sm, err := settings.NewManager(ctx, st)
	if err != nil {
		st.Close()
		return nil, err
	}
	return &app{
		paths: p, st: st, settings: sm,
		auth: auth.New(st), audit: audit.New(st),
		tr: i18n.Translator(sm.Get().Language),
	}, nil
}

func main() {
	root := &cobra.Command{
		Use:           "lmcwhm",
		Short:         "lmcwhm server control panel",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMenu(cmd.Context())
		},
	}
	root.AddCommand(serveCmd(), versionCmd())
	root.AddCommand(cliCommands()...)
	root.AddCommand(maintenanceCommands()...)
	root.AddCommand(backupCommand(), mcpCommand(), snapshotCommand())

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run:   func(*cobra.Command, []string) { fmt.Println("lmcwhm", config.Version) },
	}
}

func serveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the panel server (used by systemd)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			a, err := openApp(ctx)
			if err != nil {
				return err
			}
			defer a.st.Close()

			logFile, err := os.OpenFile(filepath.Join(a.paths.LogDir, "panel.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			defer logFile.Close()
			log := slog.New(slog.NewTextHandler(io.MultiWriter(os.Stderr, logFile), nil))

			if ok, err := a.auth.HasUsers(ctx); err != nil {
				return err
			} else if !ok {
				return fmt.Errorf("%s", a.tr.T("err.not_init"))
			}

			box, err := secrets.Open(a.paths.DataDir)
			if err != nil {
				return err
			}
			webCfg := webcfg.New(a.st, box)
			env := webenv.New(a.paths.Home)
			env.Timezone = a.settings.Get().Timezone
			responder := acme.NewHTTPResponder()
			go func() {
				if err := responder.Listen(ctx, webenv.ACMEPort); err != nil {
					log.Error("acme http-01 responder", "err", err)
				}
			}()
			tm := tasks.NewManager()
			sites := site.NewService(a.st, env, webCfg, &acme.Issuer{Cfg: webCfg, HTTP: responder}, tm)
			sites.RebuildMarker = server.RebuildMarker(a.paths)
			waf := security.NewWAF(a.st, env) // before the stack boots: it picks the nginx image
			fw := firewall.New(a.st, box, log.Error)
			fw.PanelPort = func() int { return a.settings.Get().PanelPort }
			env.CDN = func() webenv.CDN {
				c := fw.CDN(ctx)
				return webenv.CDN{Cloudflare: fw.CloudflareTrust(ctx), Ranges: c.Ranges, Header: c.Header}
			}
			// The edge refuses blocked visitors by their real address (behind a CDN).
			fw.OnListsChanged = func() {
				block, allow, err := fw.EdgeLists(ctx)
				if err == nil {
					err = env.SetLists(ctx, block, allow)
				}
				if err != nil {
					log.Error("edge ip lists", "err", err)
				}
			}
			fw.Boot(ctx)
			crowdsec := security.NewCrowdSec(a.st, box, env, tm, fw, a.paths.Home)
			apps := appstore.New(a.st, box, tm, a.paths.Home)
			apps.InUse = sites.UsesRuntime
			sites.Runtimes = apps
			go func() {
				if err := apps.Boot(ctx); err != nil {
					log.Error("app store boot", "err", err)
				}
				fw.OnListsChanged() // the lists the edge starts with
				if err := sites.Boot(ctx); err != nil {
					log.Error("web stack boot", "err", err)
				}
				crowdsec.Boot(ctx)
			}()

			mon := monitor.New(a.st)
			go mon.Run(ctx, log.Error)
			loc, err := time.LoadLocation(a.settings.Get().Timezone)
			if err != nil {
				loc = time.Local
			}
			jobs := cron.New(a.st, env.WWWLogs, loc)
			dbs := database.New(a.st, box, apps, tm)
			bk := backup.New(a.st, box, a.paths.Home)
			bk.WWWRoot, bk.Sites, bk.DBs, bk.Apps = env.WWWRoot, sites, dbs, apps
			jobs.Backups = bk
			apps.RuntimeUpdated = sites.RecreateProjects
			apps.Backup = func(ctx context.Context, i appstore.Installed, log tasks.Logf) error {
				return bk.BackupEngine(ctx, i.ID, logWriter(log))
			}
			jobs.Feeds = fw.RunFeeds
			alerts := alert.New(a.st, box)
			jobs.OnResult = func(j cron.Job, r cron.Run) {
				if r.Status == "failed" || r.Status == "timeout" {
					alerts.Emit(context.Background(), alert.KindCronFailed, fmt.Sprint(j.ID), "warning",
						fmt.Sprintf("Cron job %q %s", j.Name, r.Status), tailText(r.Output, 800), false)
				}
			}
			probes := probe.New(a.st)
			probes.Notify = func(p probe.Probe, down bool, detail string) {
				if down {
					alerts.Emit(context.Background(), alert.KindProbeDown, fmt.Sprint(p.ID), "critical", "DOWN: "+p.Name+" ("+p.Target+")", detail, false)
				} else {
					alerts.Emit(context.Background(), alert.KindProbeDown, fmt.Sprint(p.ID), "resolved", "UP: "+p.Name+" ("+p.Target+")", detail, true)
				}
			}
			go probes.Run(ctx)
			stats := sitestats.New(a.st, env.WWWLogs, loc)
			stats.Sites = func(ctx context.Context) []string {
				var out []string
				if list, err := sites.List(ctx); err == nil {
					for _, s := range list {
						if !s.Passthrough {
							out = append(out, s.Domain)
						}
					}
				}
				return out
			}
			go stats.Run(ctx, log.Error)
			ftpSvc := ftp.New(a.st, box, a.paths.Home, env.WWWRoot)
			ftpSvc.PublicIP = func(ctx context.Context) string {
				if c, err := webCfg.Get(ctx); err == nil && c.ServerIPv4 != "" {
					return c.ServerIPv4
				}
				return netinfo.PublicIPv4(ctx)
			}
			go ftpSvc.Boot(ctx, func(f string, a ...any) { log.Info(fmt.Sprintf(f, a...)) })
			procs := supervisor.New(a.st, a.paths.Home)
			go procs.Boot(ctx)
			if err := jobs.Start(ctx); err != nil {
				log.Error("cron", "err", err)
			}
			geo := geoip.New(filepath.Join(a.paths.Home, "geoip"))
			go geo.RefreshLoop(ctx, log.Error)
			tb := toolbox.New()
			tb.SSHChanged = func(ctx context.Context) error {
				// Open the new SSH port before sshd moves to it.
				if c, err := fw.Config(ctx); err == nil && c.Enabled {
					if _, err := fw.Apply(ctx); err != nil {
						return err
					}
				}
				return nil
			}
			tp := tamper.New(a.st, env.WWWRoot, a.paths.Home)
			mailSvc := mail.New(a.st, a.paths.Home)
			go mailSvc.Boot(ctx)
			mailSvc.Cert = func(host string) string {
				if _, err := os.Stat(env.CertPath(host)); err == nil {
					return env.CertPath(host)
				}
				return ""
			}
			go func() { // pick up renewed certificates
				t := time.NewTicker(6 * time.Hour)
				defer t.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-t.C:
						mailSvc.SyncCert(ctx)
					}
				}
			}()
			defer tp.Stop()
			srv, err := server.New(server.Deps{
				Paths: a.paths, Settings: a.settings, Auth: a.auth, Audit: a.audit,
				WebFS: web.FS(), Log: log, Sites: sites, Web: webCfg, Tasks: tm,
				Apps: apps, DBs: dbs, Docker: dockermgr.New(tm, a.paths.Home),
				Files: files.New(os.Getenv("LMCWHM_FILES_ROOT")), Term: terminal.New(),
				Store: a.st, Monitor: mon, Cron: jobs, GeoIP: geo, Firewall: fw, WAF: waf, CrowdSec: crowdsec, Backup: bk,
				Tokens: apitoken.New(a.st), AI: ai.New(a.st, box, nil),
				Alerts: alerts, Probes: probes, Stats: stats, Procs: procs, WP: wordpress.New(sites, dbs, env, tm), FTP: ftpSvc,
				Toolbox: tb, SecScan: secscan.New(a.st), Malware: malware.New(a.st, env.WWWRoot, a.paths.Home), Tamper: tp,
				Shares: fileshare.New(a.st), Mail: mailSvc,
			})
			if err != nil {
				return err
			}
			fw.OnBlocksChanged = srv.SyncFirewall
			tp.OnEvent = srv.TamperAlerts()
			go tp.Boot(ctx)
			alerts.Src = srv.AlertSources()
			go alerts.Run(ctx)
			go func() {
				if err := srv.ServeMCPSocket(ctx, server.MCPSocket(a.paths)); err != nil {
					log.Error("mcp socket", "err", err)
				}
			}()
			go crowdsec.Run(ctx, log.Error)
			jobs.Tasks = srv
			srv.EnsureFeedJobs(ctx)
			srv.EnsureSystemJobs(ctx)
			if err := server.WritePID(a.paths.PIDFile); err != nil {
				return err
			}
			defer os.Remove(a.paths.PIDFile)

			// SIGHUP = the CLI changed settings on disk: reload and rebuild the listener.
			hup := make(chan os.Signal, 1)
			signal.Notify(hup, syscall.SIGHUP)
			go func() {
				for range hup {
					if err := a.settings.Reload(ctx); err != nil {
						log.Error("reload settings", "err", err)
						continue
					}
					srv.RequestRestart()
				}
			}()
			go purgeLoop(ctx, a.auth)
			return srv.Run(ctx)
		},
	}
}

// tailText keeps the end of s (where errors are).
func tailText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

// logWriter turns task log lines into an io.Writer.
type logWriter tasks.Logf

func (l logWriter) Write(p []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if line != "" {
			l("%s", line)
		}
	}
	return len(p), nil
}
