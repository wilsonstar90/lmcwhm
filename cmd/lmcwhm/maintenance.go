package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wilsonstar90/lmcwhm/internal/config"
	"github.com/wilsonstar90/lmcwhm/internal/settings"
	"github.com/wilsonstar90/lmcwhm/internal/tlsutil"
)

const installerURL = "https://raw.githubusercontent.com/wilsonstar90/lmcwhm/main/deploy/install.sh"

// maintenanceCommands are update / repair (both re-run the installer in
// upgrade mode, which keeps sites, firewall rules and panel settings) and
// reset-panel (lock-out recovery).
func maintenanceCommands() []*cobra.Command {
	var version, channel string
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
			if channel != "" {
				args = append(args, "--channel", channel)
			}
			if updateReset {
				args = append(args, "--reset-panel")
			}
			return runInstaller(cmd.Context(), args...)
		},
	}
	update.Flags().StringVar(&version, "version", "", "release tag to install, e.g. v0.1.1 (default latest)")
	update.Flags().BoolVar(&updateReset, "reset-panel", false, "also reset panel access settings (see reset-panel)")
	update.Flags().StringVar(&channel, "channel", "", "stable (default) or beta (includes pre-releases)")

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

	var withData bool
	rollback := &cobra.Command{
		Use:   "rollback",
		Short: "Go back to the version installed before the last update (--with-data also restores the data backed up then)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return doRollback(withData)
		},
	}
	rollback.Flags().BoolVar(&withData, "with-data", false, "also restore panel data from the backup taken before that update")

	return []*cobra.Command{update, repair, reset, rollback}
}

// doRollback swaps bin/lmcwhm.prev back in and restarts the service. Schema
// migrations only add tables and columns, so an older version runs on newer
// data; --with-data restores the exact pre-update data instead.
func doRollback(withData bool) error {
	if os.Geteuid() != 0 {
		return errors.New("please run as root (sudo lmcwhm ...)")
	}
	p := config.Resolve()
	bin := filepath.Join(p.Home, "bin", "lmcwhm")
	prev := bin + ".prev"
	out, err := exec.Command(prev, "version").Output()
	if err != nil {
		return errors.New("no previous version to roll back to (it is kept after an update)")
	}
	fmt.Printf("rolling back %s -> %s\n", config.Version, strings.TrimSpace(strings.TrimPrefix(string(out), "lmcwhm")))
	exec.Command("systemctl", "stop", "lmcwhm").Run()
	if withData {
		b, err := os.ReadFile(filepath.Join(p.Home, "bin", ".prev-backup"))
		backupFile := strings.TrimSpace(string(b))
		if err != nil || backupFile == "" {
			return errors.New("no data backup recorded for the previous update")
		}
		stamp := time.Now().Format("20060102-150405")
		if err := os.Rename(p.DataDir, p.DataDir+".rollback-"+stamp); err != nil {
			return err
		}
		if out, err := exec.Command("tar", "-xzf", backupFile, "-C", p.Home).CombinedOutput(); err != nil {
			os.Rename(p.DataDir+".rollback-"+stamp, p.DataDir)
			return fmt.Errorf("restore %s: %v %s", backupFile, err, out)
		}
		fmt.Printf("data restored from %s (the current data is kept in %s.rollback-%s)\n", backupFile, p.DataDir, stamp)
	}
	// Swap: the current binary becomes .prev, so a rollback can be undone.
	cur := bin + ".rollback-tmp"
	if err := copyExec(bin, cur); err != nil {
		return err
	}
	if err := os.Rename(prev, bin); err != nil {
		return err
	}
	if err := os.Rename(cur, prev); err != nil {
		return err
	}
	if out, err := exec.Command("systemctl", "start", "lmcwhm").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl start lmcwhm: %v %s", err, out)
	}
	fmt.Println("done; run `lmcwhm rollback` again to undo")
	return nil
}

func copyExec(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o755)
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
