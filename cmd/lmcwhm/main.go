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
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/wilsonstar90/lmcwhm/internal/acme"
	"github.com/wilsonstar90/lmcwhm/internal/appstore"
	"github.com/wilsonstar90/lmcwhm/internal/audit"
	"github.com/wilsonstar90/lmcwhm/internal/auth"
	"github.com/wilsonstar90/lmcwhm/internal/config"
	"github.com/wilsonstar90/lmcwhm/internal/cron"
	"github.com/wilsonstar90/lmcwhm/internal/database"
	"github.com/wilsonstar90/lmcwhm/internal/dockermgr"
	"github.com/wilsonstar90/lmcwhm/internal/files"
	"github.com/wilsonstar90/lmcwhm/internal/geoip"
	"github.com/wilsonstar90/lmcwhm/internal/i18n"
	"github.com/wilsonstar90/lmcwhm/internal/monitor"
	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/server"
	"github.com/wilsonstar90/lmcwhm/internal/settings"
	"github.com/wilsonstar90/lmcwhm/internal/site"
	"github.com/wilsonstar90/lmcwhm/internal/store"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
	"github.com/wilsonstar90/lmcwhm/internal/terminal"
	"github.com/wilsonstar90/lmcwhm/internal/webcfg"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
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
			apps := appstore.New(a.st, box, tm, a.paths.Home)
			apps.InUse = sites.UsesRuntime
			sites.Runtimes = apps
			go func() {
				if err := apps.Boot(ctx); err != nil {
					log.Error("app store boot", "err", err)
				}
				if err := sites.Boot(ctx); err != nil {
					log.Error("web stack boot", "err", err)
				}
			}()
			go sites.RenewLoop(ctx, log.Info)

			mon := monitor.New(a.st)
			go mon.Run(ctx, log.Error)
			loc, err := time.LoadLocation(a.settings.Get().Timezone)
			if err != nil {
				loc = time.Local
			}
			jobs := cron.New(a.st, env.WWWLogs, loc)
			if err := jobs.Start(ctx); err != nil {
				log.Error("cron", "err", err)
			}
			geo := geoip.New(filepath.Join(a.paths.Home, "geoip"))
			go geo.RefreshLoop(ctx, log.Error)
			srv, err := server.New(server.Deps{
				Paths: a.paths, Settings: a.settings, Auth: a.auth, Audit: a.audit,
				WebFS: web.FS(), Log: log, Sites: sites, Web: webCfg, Tasks: tm,
				Apps: apps, DBs: database.New(a.st, box, apps, tm), Docker: dockermgr.New(tm, a.paths.Home),
				Files: files.New(os.Getenv("LMCWHM_FILES_ROOT")), Term: terminal.New(),
				Monitor: mon, Cron: jobs, GeoIP: geo,
			})
			if err != nil {
				return err
			}
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
