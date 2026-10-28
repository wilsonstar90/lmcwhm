package webenv

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strings"
	"time"
)

// A minimal FastCGI client, enough to read PHP-FPM's status page.

const (
	fcgiBeginRequest = 1
	fcgiEndRequest   = 3
	fcgiParams       = 4
	fcgiStdin        = 5
	fcgiStdout       = 6
	fcgiStderr       = 7
)

func fcgiRecord(w io.Writer, typ uint8, content []byte) error {
	h := [8]byte{1, typ, 0, 1}
	binary.BigEndian.PutUint16(h[4:], uint16(len(content)))
	if _, err := w.Write(h[:]); err != nil {
		return err
	}
	_, err := w.Write(content)
	return err
}

func fcgiPair(b *bytes.Buffer, k, v string) {
	for _, n := range []int{len(k), len(v)} {
		if n < 128 {
			b.WriteByte(byte(n))
		} else {
			binary.Write(b, binary.BigEndian, uint32(n)|1<<31)
		}
	}
	b.WriteString(k)
	b.WriteString(v)
}

// FastCGIGet performs a GET of script on a FastCGI server and returns the body.
func FastCGIGet(ctx context.Context, addr, script, query string) ([]byte, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	w := bufio.NewWriter(c)
	fcgiRecord(w, fcgiBeginRequest, []byte{0, 1, 0, 0, 0, 0, 0, 0}) // responder, no keep-conn
	var p bytes.Buffer
	for k, v := range map[string]string{
		"SCRIPT_NAME": script, "SCRIPT_FILENAME": script, "REQUEST_URI": script + "?" + query,
		"QUERY_STRING": query, "REQUEST_METHOD": "GET", "SERVER_PROTOCOL": "HTTP/1.1", "GATEWAY_INTERFACE": "CGI/1.1",
		"REMOTE_ADDR": "127.0.0.1", "SERVER_NAME": "localhost",
	} {
		fcgiPair(&p, k, v)
	}
	fcgiRecord(w, fcgiParams, p.Bytes())
	fcgiRecord(w, fcgiParams, nil)
	fcgiRecord(w, fcgiStdin, nil)
	if err := w.Flush(); err != nil {
		return nil, err
	}
	r := bufio.NewReader(c)
	var out, stderr bytes.Buffer
	for {
		var h [8]byte
		if _, err := io.ReadFull(r, h[:]); err != nil {
			return nil, err
		}
		n, pad := binary.BigEndian.Uint16(h[4:]), h[6]
		body := make([]byte, int(n)+int(pad))
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, err
		}
		switch h[1] {
		case fcgiStdout:
			out.Write(body[:n])
		case fcgiStderr:
			stderr.Write(body[:n])
		case fcgiEndRequest:
			resp := textproto.NewReader(bufio.NewReader(&out))
			hdr, err := resp.ReadMIMEHeader()
			if err != nil && !errors.Is(err, io.EOF) {
				return nil, err
			}
			if st := hdr.Get("Status"); st != "" && !strings.HasPrefix(st, "200") {
				return nil, fmt.Errorf("status %s %s", st, strings.TrimSpace(stderr.String()))
			}
			return io.ReadAll(resp.R)
		}
	}
}

// FPMStatus is PHP-FPM's status page (pm.status_path) of one pool.
type FPMStatus struct {
	Pool               string `json:"pool"`
	ProcessManager     string `json:"process manager"`
	StartTime          int64  `json:"start time"`
	StartSince         int64  `json:"start since"`
	AcceptedConn       int64  `json:"accepted conn"`
	ListenQueue        int64  `json:"listen queue"`
	MaxListenQueue     int64  `json:"max listen queue"`
	ListenQueueLen     int64  `json:"listen queue len"`
	IdleProcesses      int64  `json:"idle processes"`
	ActiveProcesses    int64  `json:"active processes"`
	TotalProcesses     int64  `json:"total processes"`
	MaxActiveProcesses int64  `json:"max active processes"`
	MaxChildrenReached int64  `json:"max children reached"`
	SlowRequests       int64  `json:"slow requests"`
}

// PHPStatus reads the FPM status of a running version.
func PHPStatus(ctx context.Context, v string) (FPMStatus, error) {
	var st FPMStatus
	body, err := FastCGIGet(ctx, fmt.Sprintf("127.0.0.1:%d", PHPPort(v)), "/fpm-status", "json")
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(body, &st)
	return st, err
}
