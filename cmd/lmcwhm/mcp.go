package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/wilsonstar90/lmcwhm/internal/config"
	"github.com/wilsonstar90/lmcwhm/internal/server"
)

// mcpCommand bridges an MCP client over stdio to the running panel's
// root-only socket, e.g.  claude mcp add lmcwhm -- ssh root@host lmcwhm mcp --stdio
func mcpCommand() *cobra.Command {
	var stdio bool
	c := &cobra.Command{
		Use: "mcp", Short: "MCP server over stdio (for AI clients via SSH)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !stdio {
				return errors.New("use: lmcwhm mcp --stdio")
			}
			return bridgeStdio(cmd.Context(), server.MCPSocket(config.Resolve()), os.Stdin, os.Stdout)
		},
	}
	c.Flags().BoolVar(&stdio, "stdio", false, "speak MCP (newline-delimited JSON-RPC) on stdin/stdout")
	return c
}

func bridgeStdio(ctx context.Context, sock string, in io.Reader, out io.Writer) error {
	if _, err := os.Stat(sock); err != nil {
		return fmt.Errorf("the panel is not running (no %s)", sock)
	}
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}, Timeout: 15 * time.Minute}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 8<<20)
	w := bufio.NewWriter(out)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://lmcwhm/mcp", bytes.NewReader(line))
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			return err
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode == http.StatusAccepted || len(bytes.TrimSpace(body)) == 0 {
			continue // notification
		}
		w.Write(bytes.TrimSpace(body))
		w.WriteByte('\n')
		if err := w.Flush(); err != nil {
			return err
		}
	}
	return sc.Err()
}
