package main

import (
	"errors"

	"github.com/spf13/cobra"
	"github.com/wilsonstar90/lmcwhm/internal/backup"
	"github.com/wilsonstar90/lmcwhm/internal/privileged"
)

func workerCmd() *cobra.Command {
	var verifyArchive string
	cmd := &cobra.Command{
		Use:    "worker",
		Short:  "Run a fixed worker action",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if verifyArchive == "" {
				return errors.New("no worker action requested")
			}
			return backup.VerifyArchive(verifyArchive)
		},
	}
	cmd.Flags().StringVar(&verifyArchive, "verify-archive", "", "verify an archive using its SHA256 manifest")
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
