package main

import (
	"errors"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/wilsonstar90/lmcwhm/internal/backup"
	"github.com/wilsonstar90/lmcwhm/internal/config"
	"github.com/wilsonstar90/lmcwhm/internal/privileged"
)

func workerCmd() *cobra.Command {
	var verifyArchive, decryptArchive, output string
	cmd := &cobra.Command{
		Use:    "worker",
		Short:  "Run a fixed worker action",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if decryptArchive != "" {
				if output == "" {
					return errors.New("decrypt output is required")
				}
				keyPath := filepath.Join(filepath.Dir(config.Resolve().Home), "lmcwhm-backup-recovery.key")
				return backup.DecryptFileTo(decryptArchive, output, keyPath)
			}
			if verifyArchive == "" {
				return errors.New("no worker action requested")
			}
			return backup.VerifyArchive(verifyArchive)
		},
	}
	cmd.Flags().StringVar(&verifyArchive, "verify-archive", "", "verify an archive using its SHA256 manifest")
	cmd.Flags().StringVar(&decryptArchive, "decrypt-archive", "", "decrypt an archive using the recovery key")
	cmd.Flags().StringVar(&output, "output", "", "decrypted archive path")
	return cmd
}

func helperCmd() *cobra.Command {
	var socket string
	cmd := &cobra.Command{
		Use:    "helper",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if socket == "" {
				return errors.New("helper socket is required")
			}
			return privileged.Serve(cmd.Context(), socket)
		},
	}
	cmd.Flags().StringVar(&socket, "socket", "", "root-only Unix socket")
	return cmd
}
