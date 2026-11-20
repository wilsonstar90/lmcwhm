package privileged

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"

	"github.com/wilsonstar90/lmcwhm/internal/dockerx"
)

var (
	ErrAction = errors.New("privileged.invalid_action")
	ErrName   = errors.New("privileged.invalid_name")
)

type Request struct {
	Action string `json:"action"`
	Name   string `json:"name,omitempty"`
}

type Response struct {
	Error string `json:"error,omitempty"`
}

type Client struct {
	Socket string
}

func (c Client) Call(ctx context.Context, action, name string) error {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	if err := json.NewEncoder(conn).Encode(Request{Action: action, Name: name}); err != nil {
		return err
	}
	var response Response
	if err := json.NewDecoder(io.LimitReader(conn, 16384)).Decode(&response); err != nil {
		return err
	}
	if response.Error != "" {
		return errors.New(response.Error)
	}
	return nil
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func execute(ctx context.Context, request Request) error {
	switch request.Action {
	case "docker.restart", "docker.start", "docker.stop":
		if !namePattern.MatchString(request.Name) {
			return ErrName
		}
		args := []string{request.Action[len("docker."):], request.Name}
		if request.Action == "docker.restart" {
			args = []string{"restart", "-t", "30", request.Name}
		}
		_, err := dockerx.Run(ctx, args...)
		return err
	case "nginx.test":
		_, err := dockerx.Run(ctx, "exec", "lmcwhm-nginx", "nginx", "-t", "-c", "/etc/nginx/lmcwhm/nginx.conf")
		return err
	case "haproxy.test":
		_, err := dockerx.Run(ctx, "exec", "lmcwhm-haproxy", "haproxy", "-c", "-q", "-f", "/opt/lmcwhm/edge/haproxy.cfg")
		return err
	default:
		return ErrAction
	}
}

func handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	var request Request
	response := Response{}
	if err := json.NewDecoder(io.LimitReader(conn, 16384)).Decode(&request); err != nil {
		response.Error = err.Error()
	} else if err := execute(ctx, request); err != nil {
		response.Error = err.Error()
	}
	_ = json.NewEncoder(conn).Encode(response)
}

func Serve(ctx context.Context, socket string) error {
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return err
	}
	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		return err
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		listener.Close()
		return err
	}
	go func() {
		<-ctx.Done()
		listener.Close()
		os.Remove(socket)
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go handle(ctx, conn)
	}
}
