package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"

	"github.com/wilsonstar90/lmcwhm/internal/settings"
	"github.com/wilsonstar90/lmcwhm/internal/tlsutil"
)

const installerURL = "https://raw.githubusercontent.com/wilsonstar90/lmcwhm/main/deploy/install.sh"

// maintenanceCommands are update / repair (both re-run the installer in
// upgrade mode, which keeps sites, firewall rules and panel settings) and
// reset-panel (lock-out recovery).
func maintenanceCommands() []*cobra.Command {
	var version string
	var updateReset bool
	update := &cobra.Command{
		Use:   "update",
		Short: "Update lmcwhm to the latest (or --version) release; sites, firewall and settings are kept",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			args := []string{}
			if version != "" {
				args = append(args, "--version", version)
			}
			if updateReset {
				args = append(args, "--reset-panel")
			}
			return runInstaller(cmd.Context(), args...)
		},
	}
	update.Flags().StringVar(&version, "version", "", "release tag to install, e.g. v0.1.1 (default latest)")
	update.Flags().BoolVar(&updateReset, "reset-panel", false, "also reset panel access settings (see reset-panel)")

	var repairReset bool
	repair := &cobra.Command{
		Use:   "repair",
		Short: "Reinstall the current version (binary, service); sites, firewall and settings are kept",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			args := []string{"--repair"}
			if repairReset {
				args = append(args, "--reset-panel")
			}
			return runInstaller(cmd.Context(), args...)
		},
	}
	repair.Flags().BoolVar(&repairReset, "reset-panel", false, "also reset panel access settings (see reset-panel)")

	var rPort int
	var rEntry, rPass string
	reset := &cobra.Command{
		Use:   "reset-panel",
		Short: "Lock-out recovery: new security entry and admin password, self-signed SSL, 2FA/IP/domain/BasicAuth off",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withApp(cmd.Context(), func(a *app) error {
				return doResetPanel(cmd.Context(), a, rPort, rEntry, rPass)
			})
		},
	}
	reset.Flags().IntVar(&rPort, "port", 0, "also change the panel port (default: keep current)")
	reset.Flags().StringVar(&rEntry, "entry", "", "security entry to use (default random)")
	reset.Flags().StringVar(&rPass, "password", "", "admin password to set (default random)")

	return []*cobra.Command{update, repair, reset}
}

// doResetPanel restores panel access without touching websites, databases or
// firewall rules. The port is kept unless given, because the firewall already
// allows it and moving it could lock the admin out further.
func doResetPanel(ctx context.Context, a *app, port int, entry, password string) error {
	if entry == "" {
		entry = settings.RandomEntry()
	}
	if password == "" {
		password = settings.RandomString(12, settings.Alnum)
	}
	if _, err := a.settings.Update(ctx, func(s *settings.Settings) {
		s.SecurityEntry = entry
		s.SSLMode = settings.SSLSelfSigned
		s.AllowedIPs = nil
		s.BindDomain = ""
		s.BasicAuthUser, s.BasicAuthHash = "", ""
		if port > 0 {
			s.PanelPort = port
		}
	}); err != nil {
		return err
	}
	if err := tlsutil.GenerateSelfSigned(a.paths.CertDir, nil); err != nil {
		return err
	}
	u, err := a.auth.PrimaryAdmin(ctx)
	if err != nil {
		return errors.New(a.tr.T("err.not_init"))
	}
	if err := a.auth.DisableTOTP(ctx, u.ID); err != nil {
		return err
	}
	if err := a.auth.SetPassword(ctx, u.ID, password); err != nil {
		return err
	}
	if err := a.auth.ClearLoginLocks(ctx); err != nil {
		return err
	}
	if err := a.auth.RevokeAllSessions(ctx); err != nil {
		return err
	}
	a.audit.Record(ctx, "cli", "127.0.0.1", "panel", "cli.reset_panel", "")
	fmt.Println(a.tr.T("ok.reset_panel"))
	a.signalReload()
	return printInfo(ctx, a, password)
}

// runInstaller downloads the current install.sh and runs it with args.
func runInstaller(ctx context.Context, args ...string) error {
	if os.Geteuid() != 0 {
		return errors.New("please run as root (sudo lmcwhm ...)")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, installerURL, nil)
	resp, err := http.DefaultClient.Do(req)
	cancel()
	if err != nil {
		return fmt.Errorf("download installer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download installer: HTTP %d", resp.StatusCode)
	}
	f, err := os.CreateTemp("", "lmcwhm-install-*.sh")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 1<<20)); err != nil {
		f.Close()
		return err
	}
	f.Close()

	c := exec.Command("bash", append([]string{f.Name()}, args...)...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}
