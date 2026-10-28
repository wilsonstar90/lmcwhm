package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/wilsonstar90/lmcwhm/internal/backup"
	"github.com/wilsonstar90/lmcwhm/internal/config"
	"github.com/wilsonstar90/lmcwhm/internal/secrets"
	"github.com/wilsonstar90/lmcwhm/internal/server"
)

func backupCommand() *cobra.Command {
	root := &cobra.Command{Use: "backup", Short: "Panel backups (list, back up the panel, restore it)"}
	open := func(a *app) (*backup.Service, error) {
		box, err := secrets.Open(a.paths.DataDir)
		if err != nil {
			return nil, err
		}
		return backup.New(a.st, box, a.paths.Home), nil
	}
	root.AddCommand(&cobra.Command{
		Use: "list", Short: "List stored backups", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withApp(cmd.Context(), func(a *app) error {
				bk, err := open(a)
				if err != nil {
					return err
				}
				recs, err := bk.List(cmd.Context(), "")
				if err != nil {
					return err
				}
				tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
				fmt.Fprintln(tw, "ID\tKIND\tTARGET\tFILE\tSIZE\tWHERE\tTIME")
				for _, r := range recs {
					fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d\t%s\t%s\n", r.ID, r.Kind, r.Target, r.Name, r.Size, r.DestName,
						time.Unix(r.CreatedAt, 0).Format("2006-01-02 15:04"))
				}
				return tw.Flush()
			})
		},
	})
	root.AddCommand(&cobra.Command{
		Use: "panel", Short: "Back up the panel settings, certificates and web configuration now", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withApp(cmd.Context(), func(a *app) error {
				bk, err := open(a)
				if err != nil {
					return err
				}
				return bk.Run(cmd.Context(), backup.Plan{Kind: backup.KindPanel, Dests: []string{backup.DestLocal}}, 0, os.Stdout)
			})
		},
	})
	var force bool
	restore := &cobra.Command{
		Use: "restore-panel <file.tar.gz>", Short: "Restore a panel backup (stop the panel first)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := config.Resolve()
			if panelRunning(p) && !force {
				return errors.New("the panel is running: systemctl stop lmcwhm, then run this again")
			}
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			if err := backup.RestorePanel(p.Home, f); err != nil {
				return err
			}
			// Recreate the web stack from the restored configuration on the next start.
			os.WriteFile(server.RebuildMarker(p), nil, 0o600)
			fmt.Println("panel restored; start it with: systemctl start lmcwhm")
			return nil
		},
	}
	restore.Flags().BoolVar(&force, "force", false, "restore even though the panel seems to be running")
	root.AddCommand(restore)
	return root
}

func panelRunning(p config.Paths) bool {
	raw, err := os.ReadFile(p.PIDFile)
	if err != nil {
		return false
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return pid > 0 && syscall.Kill(pid, 0) == nil
}
