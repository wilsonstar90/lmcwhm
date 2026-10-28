package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/wilsonstar90/lmcwhm/internal/backup"
	"github.com/wilsonstar90/lmcwhm/internal/config"
	"github.com/wilsonstar90/lmcwhm/internal/server"
	"github.com/wilsonstar90/lmcwhm/internal/webenv"
)

// snapshotCommand restores a panel snapshot (made under Backups → Snapshot)
// on this server: the way to move everything to a new machine.
func snapshotCommand() *cobra.Command {
	root := &cobra.Command{Use: "snapshot", Short: "Panel snapshots: move the whole server to a new machine"}
	var noStart bool
	restore := &cobra.Command{
		Use:   "restore <file.tar.gz | https://share-link>",
		Short: "Restore a snapshot: panel settings, App Store data and websites (stops the panel while restoring)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Geteuid() != 0 {
				return errors.New("please run as root (sudo lmcwhm ...)")
			}
			p := config.Resolve()
			var src io.ReadCloser
			if strings.HasPrefix(args[0], "http://") || strings.HasPrefix(args[0], "https://") {
				// A share link answers GET with a page and POST with the file.
				resp, err := http.Post(args[0], "application/x-www-form-urlencoded", strings.NewReader(""))
				if err != nil {
					return err
				}
				if resp.StatusCode != http.StatusOK {
					resp.Body.Close()
					return fmt.Errorf("download: HTTP %d (a password-protected link cannot be used here)", resp.StatusCode)
				}
				src = resp.Body
			} else {
				f, err := os.Open(args[0])
				if err != nil {
					return err
				}
				src = f
			}
			defer src.Close()
			if panelRunning(p) {
				fmt.Println("stopping lmcwhm")
				if out, err := exec.Command("systemctl", "stop", "lmcwhm").CombinedOutput(); err != nil {
					return fmt.Errorf("systemctl stop lmcwhm: %v %s", err, out)
				}
				time.Sleep(time.Second)
			}
			env := webenv.New(p.Home)
			m, err := backup.RestoreSnapshot(p.Home, env.WWWRoot, src, func(f string, a ...any) { fmt.Printf(f+"\n", a...) })
			if err != nil {
				return err
			}
			// Recreate the edge, nginx and PHP containers from the restored configuration.
			os.WriteFile(server.RebuildMarker(p), nil, 0o600)
			fmt.Printf("snapshot of %s restored.\n", m.Hostname)
			fmt.Println("Websites keep their DNS records: point them at this server's IP (Website → DNS, or Cloudflare).")
			if noStart {
				fmt.Println("start the panel with: systemctl start lmcwhm")
				return nil
			}
			if out, err := exec.Command("systemctl", "start", "lmcwhm").CombinedOutput(); err != nil {
				return fmt.Errorf("systemctl start lmcwhm: %v %s", err, out)
			}
			fmt.Println("panel started; it is recreating containers in the background (first start pulls images).")
			return withApp(cmd.Context(), func(a *app) error { return printInfo(cmd.Context(), a, "") })
		},
	}
	restore.Flags().BoolVar(&noStart, "no-start", false, "do not start the panel afterwards")
	root.AddCommand(restore)
	return root
}
