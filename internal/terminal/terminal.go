// Package terminal bridges a browser xterm.js over WebSocket to a PTY running
// a host shell or `docker exec` into a container.
package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"github.com/wilsonstar90/lmcwhm/internal/settings"
)

var (
	ErrBadTarget = errors.New("terminal.invalid_target")
	ErrTicket    = errors.New("terminal.invalid_ticket")
)

var containerRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.\-]{0,127}$`)

// Ticket authorises one terminal connection shortly after the password was re-entered.
type Ticket struct {
	User      string
	Container string // "" = host shell
	expires   time.Time
}

type Service struct {
	mu      sync.Mutex
	tickets map[string]Ticket
}

func New() *Service { return &Service{tickets: map[string]Ticket{}} }

// Issue returns a single-use ticket valid for 30 seconds.
func (s *Service) Issue(user, container string) (string, error) {
	if container != "" && !containerRe.MatchString(container) {
		return "", ErrBadTarget
	}
	id := settings.RandomString(32, settings.Alnum)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, t := range s.tickets {
		if now.After(t.expires) {
			delete(s.tickets, k)
		}
	}
	s.tickets[id] = Ticket{User: user, Container: container, expires: now.Add(30 * time.Second)}
	return id, nil
}

// Redeem consumes a ticket issued to user.
func (s *Service) Redeem(id, user string) (Ticket, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tickets[id]
	delete(s.tickets, id)
	if !ok || t.User != user || time.Now().After(t.expires) {
		return Ticket{}, ErrTicket
	}
	return t, nil
}

func command(t Ticket) *exec.Cmd {
	if t.Container != "" {
		return exec.Command("docker", "exec", "-it", "-e", "TERM=xterm-256color", t.Container,
			"sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi")
	}
	shell := "/bin/bash"
	if _, err := os.Stat(shell); err != nil {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-l")
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/"
	}
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "HOME="+home)
	return cmd
}

// message is a client → server frame: input data or a resize.
type message struct {
	Type string `json:"t"` // "i" input | "r" resize
	Data string `json:"d,omitempty"`
	Cols uint16 `json:"c,omitempty"`
	Rows uint16 `json:"r,omitempty"`
}

// Serve runs the session until either side closes. Output is sent as binary frames.
func Serve(ctx context.Context, c *websocket.Conn, t Ticket) error {
	cmd := command(t)
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 120, Rows: 32})
	if err != nil {
		c.Close(websocket.StatusInternalError, "cannot start shell")
		return err
	}
	defer func() {
		f.Close()
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		cmd.Wait()
	}()
	c.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		defer cancel()
		buf := make([]byte, 32<<10)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				if werr := c.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed) {
					c.Close(websocket.StatusNormalClosure, "exited")
				}
				return
			}
		}
	}()

	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return nil
		}
		var m message
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Type {
		case "i":
			if _, err := f.Write([]byte(m.Data)); err != nil {
				return nil
			}
		case "r":
			if m.Cols > 0 && m.Rows > 0 {
				pty.Setsize(f, &pty.Winsize{Cols: m.Cols, Rows: m.Rows})
			}
		}
	}
}
