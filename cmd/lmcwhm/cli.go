package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/wilsonstar90/lmcwhm/internal/auth"
	"github.com/wilsonstar90/lmcwhm/internal/settings"
	"github.com/wilsonstar90/lmcwhm/internal/tlsutil"
)

const serviceName = "lmcwhm"

// withApp opens the database for a CLI action and closes it afterwards.
func withApp(ctx context.Context, fn func(*app) error) error {
	a, err := openApp(ctx)
	if err != nil {
		return err
	}
	defer a.st.Close()
	return fn(a)
}

// changeSettings saves a settings mutation and tells a running panel to reload.
func (a *app) changeSettings(ctx context.Context, fn func(*settings.Settings), okMsg string) error {
	if _, err := a.settings.Update(ctx, fn); err != nil {
		return err
	}
	a.audit.Record(ctx, "cli", "127.0.0.1", "panel", "cli.settings", okMsg)
	fmt.Println(okMsg)
	a.signalReload()
	return nil
}

func (a *app) signalReload() {
	raw, err := os.ReadFile(a.paths.PIDFile)
	if err != nil {
		return // panel not running: nothing to notify
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if pid <= 0 {
		return
	}
	if err := syscall.Kill(pid, syscall.SIGHUP); err != nil && !errors.Is(err, syscall.ESRCH) {
		fmt.Println(a.tr.T("warn.reload_failed", err))
	}
}

func systemctl(action string) error {
	c := exec.Command("systemctl", action, serviceName)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	return c.Run()
}

func cliCommands() []*cobra.Command {
	simple := func(use, short string, fn func(context.Context, *app) error) *cobra.Command {
		return &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return withApp(cmd.Context(), func(a *app) error { return fn(cmd.Context(), a) })
			}}
	}
	svc := func(action string) *cobra.Command {
		return &cobra.Command{Use: action, Short: action + " the panel service", Args: cobra.NoArgs,
			RunE: func(*cobra.Command, []string) error { return systemctl(action) }}
	}

	var initUser, initPass, initEntry string
	var initPort int
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Initialise the panel (first install): admin user, port, security entry",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withApp(cmd.Context(), func(a *app) error {
				return doInit(cmd.Context(), a, initUser, initPass, initPort, initEntry)
			})
		},
	}
	initCmd.Flags().StringVar(&initUser, "username", "", "admin username (default random)")
	initCmd.Flags().StringVar(&initPass, "password", "", "admin password (default random)")
	initCmd.Flags().IntVar(&initPort, "port", 0, "panel port (default random 10000-60000)")
	initCmd.Flags().StringVar(&initEntry, "entry", "", "security entry, e.g. /console (default random)")

	reload := &cobra.Command{Use: "reload", Short: "Reload panel settings without restarting", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withApp(cmd.Context(), func(a *app) error { a.signalReload(); return nil })
		}}

	passwd := &cobra.Command{Use: "passwd [new-password]", Short: "Change the admin password", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd.Context(), func(a *app) error { return doPasswd(cmd.Context(), a, firstArg(args)) })
		}}
	username := &cobra.Command{Use: "username [new-username]", Short: "Change the admin username", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd.Context(), func(a *app) error { return doUsername(cmd.Context(), a, firstArg(args)) })
		}}
	port := &cobra.Command{Use: "port [new-port]", Short: "Print or change the panel port", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd.Context(), func(a *app) error {
				if len(args) == 0 {
					fmt.Println(a.settings.Get().PanelPort)
					return nil
				}
				return doPort(cmd.Context(), a, args[0])
			})
		}}

	var entryOff bool
	entry := &cobra.Command{Use: "entry [/path]", Short: "Set (or --disable) the security entry", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withApp(cmd.Context(), func(a *app) error {
				if entryOff {
					return a.changeSettings(cmd.Context(), func(s *settings.Settings) { s.SecurityEntry = "" }, a.tr.T("ok.entry_off"))
				}
				return doEntry(cmd.Context(), a, firstArg(args))
			})
		}}
	entry.Flags().BoolVar(&entryOff, "disable", false, "disable the security entry")

	return []*cobra.Command{
		initCmd,
		svc("start"), svc("stop"), svc("restart"), svc("status"), reload,
		simple("default", "Show panel address, entry and username", showDefault),
		passwd, username, port, entry,
		simple("ssl-off", "Disable panel SSL (serve plain http)", func(ctx context.Context, a *app) error {
			return a.changeSettings(ctx, func(s *settings.Settings) { s.SSLMode = settings.SSLOff }, a.tr.T("ok.ssl_off"))
		}),
		simple("2fa-off", "Disable two-factor authentication for the admin", func(ctx context.Context, a *app) error {
			u, err := a.auth.PrimaryAdmin(ctx)
			if err != nil {
				return err
			}
			if err := a.auth.DisableTOTP(ctx, u.ID); err != nil {
				return err
			}
			fmt.Println(a.tr.T("ok.2fa_off"))
			return nil
		}),
		simple("domain-unbind", "Remove the panel domain binding", func(ctx context.Context, a *app) error {
			return a.changeSettings(ctx, func(s *settings.Settings) { s.BindDomain = "" }, a.tr.T("ok.domain_off"))
		}),
		simple("ip-clear", "Remove the authorized IP restriction", func(ctx context.Context, a *app) error {
			return a.changeSettings(ctx, func(s *settings.Settings) { s.AllowedIPs = nil }, a.tr.T("ok.ip_off"))
		}),
		simple("basicauth-off", "Disable BasicAuth in front of the panel", func(ctx context.Context, a *app) error {
			return a.changeSettings(ctx, func(s *settings.Settings) { s.BasicAuthUser, s.BasicAuthHash = "", "" }, a.tr.T("ok.basicauth_off"))
		}),
		simple("unlock", "Clear login lockouts", func(ctx context.Context, a *app) error {
			if err := a.auth.ClearLoginLocks(ctx); err != nil {
				return err
			}
			fmt.Println(a.tr.T("ok.unlock"))
			return nil
		}),
		simple("log", "Show the last 100 lines of the panel log", showLog),
	}
}

func firstArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

func doInit(ctx context.Context, a *app, user, pass string, port int, entry string) error {
	if ok, err := a.auth.HasUsers(ctx); err != nil {
		return err
	} else if ok {
		fmt.Println("already initialised")
		return showDefault(ctx, a)
	}
	if user == "" {
		user = strings.ToLower(settings.RandomString(8, settings.Alnum))
	}
	if pass == "" {
		pass = settings.RandomString(12, settings.Alnum)
	}
	if port == 0 {
		port = settings.RandomPort()
	}
	if entry == "" {
		entry = settings.RandomEntry()
	}
	if _, err := a.settings.Update(ctx, func(s *settings.Settings) {
		s.PanelPort, s.SecurityEntry, s.SSLMode = port, entry, settings.SSLSelfSigned
	}); err != nil {
		return err
	}
	if err := tlsutil.EnsureSelfSigned(a.paths.CertDir, nil); err != nil {
		return err
	}
	if err := a.auth.CreateUser(ctx, user, pass, true); err != nil {
		return err
	}
	return printInfo(ctx, a, pass)
}

func doPasswd(ctx context.Context, a *app, pw string) error {
	if pw == "" {
		pw = prompt(a.tr.T("ask.password"))
	}
	if pw == "" {
		pw = settings.RandomString(12, settings.Alnum)
	}
	u, err := a.auth.PrimaryAdmin(ctx)
	if err != nil {
		return err
	}
	if err := a.auth.SetPassword(ctx, u.ID, pw); err != nil {
		return err
	}
	a.audit.Record(ctx, "cli", "127.0.0.1", "panel", "user.password", "")
	fmt.Println(a.tr.T("ok.password", pw))
	return nil
}

func doUsername(ctx context.Context, a *app, name string) error {
	if name == "" {
		name = prompt(a.tr.T("ask.username"))
	}
	if name == "" {
		name = strings.ToLower(settings.RandomString(8, settings.Alnum))
	}
	u, err := a.auth.PrimaryAdmin(ctx)
	if err != nil {
		return err
	}
	if err := a.auth.SetUsername(ctx, u.ID, name); err != nil {
		return err
	}
	fmt.Println(a.tr.T("ok.username", name))
	return nil
}

func doPort(ctx context.Context, a *app, v string) error {
	if v == "" {
		v = prompt(a.tr.T("ask.port"))
	}
	p, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return settings.ErrInvalidPort
	}
	return a.changeSettings(ctx, func(s *settings.Settings) { s.PanelPort = p }, a.tr.T("ok.port", p))
}

func doEntry(ctx context.Context, a *app, v string) error {
	if v == "" {
		v = prompt(a.tr.T("ask.entry"))
	}
	if v == "" {
		v = settings.RandomEntry()
	}
	if !strings.HasPrefix(v, "/") {
		v = "/" + v
	}
	return a.changeSettings(ctx, func(s *settings.Settings) { s.SecurityEntry = v }, a.tr.T("ok.entry", v))
}

func showDefault(ctx context.Context, a *app) error { return printInfo(ctx, a, "") }

// printInfo prints the bt-style "default" block. password is only known right
// after it was generated; otherwise a hint is shown.
func printInfo(ctx context.Context, a *app, password string) error {
	cfg := a.settings.Get()
	u, err := a.auth.PrimaryAdmin(ctx)
	if err != nil {
		return errors.New(a.tr.T("err.not_init"))
	}
	scheme := "https"
	if cfg.SSLMode == settings.SSLOff {
		scheme = "http"
	}
	url := func(host string) string {
		return fmt.Sprintf("%s://%s%s", scheme, net.JoinHostPort(host, strconv.Itoa(cfg.PanelPort)), cfg.SecurityEntry)
	}
	line := strings.Repeat("=", 66)
	fmt.Println(line)
	fmt.Println(" " + a.tr.T("info.title"))
	fmt.Println(line)
	v4, v6 := publicIP("https://api.ipify.org"), publicIP("https://api6.ipify.org")
	if v4 != "" {
		fmt.Printf(" %s: %s\n", a.tr.T("info.outer4"), url(v4))
	}
	if v6 != "" && cfg.ListenIPv6 {
		fmt.Printf(" %s: %s\n", a.tr.T("info.outer6"), url(v6))
	}
	for _, ip := range tlsutil.LocalIPs() {
		if p := net.ParseIP(ip); p != nil && p.IsPrivate() {
			fmt.Printf(" %s: %s\n", a.tr.T("info.inner"), url(ip))
		}
	}
	if cfg.BindDomain != "" {
		fmt.Printf(" Domain: %s\n", url(cfg.BindDomain))
	}
	fmt.Printf(" %s: %s\n", a.tr.T("info.username"), u.Username)
	if password != "" {
		fmt.Printf(" %s: %s\n", a.tr.T("info.password"), password)
	} else {
		fmt.Printf(" %s: %s\n", a.tr.T("info.password"), a.tr.T("info.password_hidden"))
	}
	fmt.Println(line)
	fmt.Println(" " + a.tr.T("info.warn_port", cfg.PanelPort))
	if cfg.SSLMode == settings.SSLSelfSigned {
		fmt.Println(" " + a.tr.T("info.warn_selfsigned"))
	}
	fmt.Println(line)
	return nil
}

func publicIP(endpoint string) string {
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(endpoint)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	if ip := net.ParseIP(strings.TrimSpace(string(b))); ip != nil {
		return ip.String()
	}
	return ""
}

func showLog(_ context.Context, a *app) error {
	raw, err := os.ReadFile(filepath.Join(a.paths.LogDir, "panel.log"))
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) > 100 {
		lines = lines[len(lines)-100:]
	}
	fmt.Println(strings.Join(lines, "\n"))
	return nil
}

var stdin = bufio.NewReader(os.Stdin)

func prompt(label string) string {
	fmt.Print(label)
	s, _ := stdin.ReadString('\n')
	return strings.TrimSpace(s)
}

// runMenu is the numbered menu shown by a bare `lmcwhm`, like aaPanel's `bt`.
func runMenu(ctx context.Context) error {
	return withApp(ctx, func(a *app) error {
		t := a.tr.T
		line := strings.Repeat("=", 66)
		fmt.Println(line)
		fmt.Println(" " + t("menu.title"))
		fmt.Println(line)
		for i := 1; i <= 17; i += 2 {
			left := fmt.Sprintf("(%d) %s", i, t(fmt.Sprintf("menu.%d", i)))
			right := ""
			if i+1 <= 17 {
				right = fmt.Sprintf("(%d) %s", i+1, t(fmt.Sprintf("menu.%d", i+1)))
			}
			fmt.Printf(" %-34s %s\n", left, right)
		}
		fmt.Printf(" (0) %s\n", t("menu.0"))
		fmt.Println(line)

		choice := prompt(t("menu.prompt"))
		u, _ := a.auth.PrimaryAdmin(ctx)
		switch choice {
		case "0", "":
			return nil
		case "1":
			return systemctl("restart")
		case "2":
			return systemctl("stop")
		case "3":
			return systemctl("start")
		case "4":
			a.signalReload()
			return nil
		case "5":
			return doPasswd(ctx, a, "")
		case "6":
			return doUsername(ctx, a, "")
		case "7":
			return doPort(ctx, a, "")
		case "8":
			return doEntry(ctx, a, "")
		case "9":
			return a.changeSettings(ctx, func(s *settings.Settings) { s.SecurityEntry = "" }, t("ok.entry_off"))
		case "10":
			return a.changeSettings(ctx, func(s *settings.Settings) { s.SSLMode = settings.SSLOff }, t("ok.ssl_off"))
		case "11":
			if err := a.auth.DisableTOTP(ctx, u.ID); err != nil {
				return err
			}
			fmt.Println(t("ok.2fa_off"))
			return nil
		case "12":
			return a.changeSettings(ctx, func(s *settings.Settings) { s.BindDomain = "" }, t("ok.domain_off"))
		case "13":
			return a.changeSettings(ctx, func(s *settings.Settings) { s.AllowedIPs = nil }, t("ok.ip_off"))
		case "14":
			if err := a.auth.ClearLoginLocks(ctx); err != nil {
				return err
			}
			fmt.Println(t("ok.unlock"))
			return nil
		case "15":
			return a.changeSettings(ctx, func(s *settings.Settings) { s.BasicAuthUser, s.BasicAuthHash = "", "" }, t("ok.basicauth_off"))
		case "16":
			return showDefault(ctx, a)
		case "17":
			return showLog(ctx, a)
		default:
			fmt.Println(t("menu.invalid"))
			return nil
		}
	})
}

func purgeLoop(ctx context.Context, s *auth.Service) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.PurgeExpired(ctx)
		}
	}
}
