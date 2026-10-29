// Package server runs the panel's HTTP(S) listener and API.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

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
	"github.com/wilsonstar90/lmcwhm/internal/mail"
	"github.com/wilsonstar90/lmcwhm/internal/malware"
	"github.com/wilsonstar90/lmcwhm/internal/mcp"
	"github.com/wilsonstar90/lmcwhm/internal/monitor"
	"github.com/wilsonstar90/lmcwhm/internal/probe"
	"github.com/wilsonstar90/lmcwhm/internal/protect"
	"github.com/wilsonstar90/lmcwhm/internal/secscan"
	"github.com/wilsonstar90/lmcwhm/internal/security"
	"github.com/wilsonstar90/lmcwhm/internal/settings"
	"github.com/wilsonstar90/lmcwhm/internal/site"
	"github.com/wilsonstar90/lmcwhm/internal/sitestats"
	"github.com/wilsonstar90/lmcwhm/internal/store"
	"github.com/wilsonstar90/lmcwhm/internal/supervisor"
	"github.com/wilsonstar90/lmcwhm/internal/tamper"
	"github.com/wilsonstar90/lmcwhm/internal/tasks"
	"github.com/wilsonstar90/lmcwhm/internal/terminal"
	"github.com/wilsonstar90/lmcwhm/internal/tlsutil"
	"github.com/wilsonstar90/lmcwhm/internal/toolbox"
	"github.com/wilsonstar90/lmcwhm/internal/webcfg"
	"github.com/wilsonstar90/lmcwhm/internal/wordpress"
)

type Deps struct {
	Paths    config.Paths
	Settings *settings.Manager
	Store    *store.Store // panel database (built-in task bookkeeping)
	Auth     *auth.Service
	Audit    *audit.Logger
	WebFS    fs.FS // built Vue SPA (may be empty in dev)
	Log      *slog.Logger
	Sites    *site.Service
	Web      *webcfg.Store
	Tasks    *tasks.Manager
	Apps     *appstore.Service
	DBs      *database.Service
	Docker   *dockermgr.Manager
	Files    *files.FS
	Term     *terminal.Service
	Monitor  *monitor.Monitor
	Cron     *cron.Service
	GeoIP    *geoip.DB
	Firewall *firewall.Service
	Protect  *protect.Service
	WAF      *security.WAF
	CrowdSec *security.CrowdSec
	Backup   *backup.Service
	Tokens   *apitoken.Service
	AI       *ai.Service
	Alerts   *alert.Service
	Probes   *probe.Service
	Stats    *sitestats.Service
	Procs    *supervisor.Service
	WP       *wordpress.Service
	FTP      *ftp.Service
	Toolbox  *toolbox.Service
	SecScan  *secscan.Service
	Malware  *malware.Service
	Tamper   *tamper.Service
	Shares   *fileshare.Service
	Mail     *mail.Service
}

type Server struct {
	pmaLinks pmaLinks // one-time phpMyAdmin sign-on links
	Deps
	secret  string
	restart chan struct{}
	MCP     *mcp.Server
	api     chi.Router // /api/v1, for the OpenAPI description
}

func New(d Deps) (*Server, error) {
	secret, err := d.Settings.InstanceSecret(context.Background())
	if err != nil {
		return nil, err
	}
	s := &Server{Deps: d, secret: secret, restart: make(chan struct{}, 1)}
	s.MCP = s.buildMCP()
	if s.AI != nil {
		s.AI.MCP = s.MCP
	}
	return s, nil
}

// RequestRestart rebuilds the listener (port / SSL / IPv6 changed). It is
// non-blocking and coalesces repeated requests.
func (s *Server) RequestRestart() {
	select {
	case s.restart <- struct{}{}:
	default:
	}
}

// Run serves until ctx is cancelled, rebuilding the listener whenever a
// restart is requested (settings change or SIGHUP from the CLI).
func (s *Server) Run(ctx context.Context) error {
	for {
		cfg := s.Settings.Get()
		srv, ln, err := s.listen(cfg)
		if err != nil {
			return err
		}
		scheme := "http"
		if cfg.SSLMode != settings.SSLOff {
			scheme = "https"
		}
		s.Log.Info("panel listening", "addr", ln.Addr().String(), "scheme", scheme, "entry", cfg.SecurityEntry)

		errc := make(chan error, 1)
		go func() { errc <- srv.Serve(ln) }()

		var stop bool
		select {
		case <-ctx.Done():
			stop = true
		case <-s.restart:
			s.Log.Info("restarting panel listener")
		case err := <-errc:
			if !errors.Is(err, http.ErrServerClosed) {
				return err
			}
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		srv.Shutdown(shutdownCtx)
		cancel()
		if stop {
			return nil
		}
	}
}

func (s *Server) listen(cfg settings.Settings) (*http.Server, net.Listener, error) {
	network, host := "tcp4", "0.0.0.0"
	if cfg.ListenIPv6 {
		network, host = "tcp", "::" // dual-stack socket accepts v4-mapped too
	}
	addr := net.JoinHostPort(host, strconv.Itoa(cfg.PanelPort))
	ln, err := net.Listen(network, addr)
	if err != nil {
		return nil, nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	srv := &http.Server{
		// Remember when each connection was accepted (firewall confirmation).
		ConnContext: func(ctx context.Context, _ net.Conn) context.Context {
			return context.WithValue(ctx, connStartKey{}, time.Now())
		},
		Handler:           s.handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          slog.NewLogLogger(s.Log.Handler(), slog.LevelDebug),
	}
	if cfg.SSLMode == settings.SSLOff {
		return srv, ln, nil
	}
	if cfg.SSLMode == settings.SSLSelfSigned {
		if err := tlsutil.EnsureSelfSigned(s.Paths.CertDir, nil); err != nil {
			ln.Close()
			return nil, nil, err
		}
	}
	cert, err := tlsutil.Load(s.Paths.CertDir)
	if err != nil {
		// A broken custom cert must not lock the admin out: fall back to self-signed.
		s.Log.Error("panel certificate unusable, regenerating self-signed", "err", err)
		if err := tlsutil.GenerateSelfSigned(s.Paths.CertDir, nil); err != nil {
			ln.Close()
			return nil, nil, err
		}
		if cert, err = tlsutil.Load(s.Paths.CertDir); err != nil {
			ln.Close()
			return nil, nil, err
		}
	}
	srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	return srv, tls.NewListener(ln, srv.TLSConfig), nil
}

type connStartKey struct{}

// connStart is when the request's connection was accepted.
func connStart(r *http.Request) time.Time {
	t, _ := r.Context().Value(connStartKey{}).(time.Time)
	return t
}

// WritePID records the running process so the CLI can signal a reload.
func WritePID(path string) error {
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o600)
}
