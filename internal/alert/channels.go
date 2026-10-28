package alert

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Channel kinds.
const (
	Email    = "email"
	Telegram = "telegram"
	Webhook  = "webhook"
	WeCom    = "wecom"
	DingTalk = "dingtalk"
	Slack    = "slack"
	Discord  = "discord"
)

var ErrChannel = errors.New("alert.invalid_channel")

// ChannelConfig holds every kind's settings; only the kind's fields are used.
type ChannelConfig struct {
	// email
	Host     string   `json:"host,omitempty"`
	Port     int      `json:"port,omitempty"`
	Security string   `json:"security,omitempty"` // starttls | tls | none
	Username string   `json:"username,omitempty"`
	Password string   `json:"password,omitempty"`
	From     string   `json:"from,omitempty"`
	To       []string `json:"to,omitempty"`
	// telegram
	BotToken string `json:"bot_token,omitempty"`
	ChatID   string `json:"chat_id,omitempty"`
	// webhook / wecom / dingtalk / slack / discord
	URL    string `json:"url,omitempty"`
	Secret string `json:"secret,omitempty"` // dingtalk signing secret
}

// secretFields are blanked when a channel is shown and kept when left empty on save.
func (c *ChannelConfig) redact() {
	if c.Password != "" {
		c.Password = "********"
	}
	if c.BotToken != "" {
		c.BotToken = "********"
	}
	if c.Secret != "" {
		c.Secret = "********"
	}
}

func (c *ChannelConfig) keepSecrets(old ChannelConfig) {
	if c.Password == "" || c.Password == "********" {
		c.Password = old.Password
	}
	if c.BotToken == "" || c.BotToken == "********" {
		c.BotToken = old.BotToken
	}
	if c.Secret == "" || c.Secret == "********" {
		c.Secret = old.Secret
	}
}

func validate(kind string, c *ChannelConfig) error {
	httpsURL := func(u string) bool {
		p, err := url.Parse(u)
		return err == nil && (p.Scheme == "https" || p.Scheme == "http") && p.Host != ""
	}
	switch kind {
	case Email:
		if c.Host == "" || c.From == "" || len(c.To) == 0 {
			return ErrChannel
		}
		if c.Port == 0 {
			c.Port = 587
		}
		if c.Security == "" {
			c.Security = "starttls"
		}
		if c.Security != "starttls" && c.Security != "tls" && c.Security != "none" {
			return ErrChannel
		}
	case Telegram:
		if c.BotToken == "" || c.ChatID == "" {
			return ErrChannel
		}
	case Webhook, WeCom, DingTalk, Slack, Discord:
		if !httpsURL(c.URL) {
			return ErrChannel
		}
	default:
		return ErrChannel
	}
	return nil
}

// Message is what a channel delivers.
type Message struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Severity string `json:"severity"` // info | warning | critical | resolved
	Kind     string `json:"kind"`
	Host     string `json:"host"`
	Time     int64  `json:"time"`
}

func (m Message) text() string {
	icon := map[string]string{"critical": "🔴", "warning": "🟠", "resolved": "🟢", "info": "🔵"}[m.Severity]
	return fmt.Sprintf("%s [%s] %s\n%s\n— %s", icon, m.Host, m.Title, m.Body, time.Unix(m.Time, 0).Format("2006-01-02 15:04:05"))
}

var httpc = &http.Client{Timeout: 15 * time.Second}

// telegramAPI is a variable so tests can use a fake server.
var telegramAPI = "https://api.telegram.org"

func postJSON(ctx context.Context, u string, v any) error {
	b, _ := json.Marshal(v)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "lmcwhm-alert")
	res, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	if res.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	// WeCom and DingTalk answer 200 with an error code in the body.
	var r struct {
		ErrCode *int   `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		OK      *bool  `json:"ok"`
		Desc    string `json:"description"`
	}
	if json.Unmarshal(body, &r) == nil {
		if r.ErrCode != nil && *r.ErrCode != 0 {
			return fmt.Errorf("errcode %d: %s", *r.ErrCode, r.ErrMsg)
		}
		if r.OK != nil && !*r.OK {
			return errors.New(r.Desc)
		}
	}
	return nil
}

// send delivers m through one channel.
func send(ctx context.Context, kind string, c ChannelConfig, m Message) error {
	switch kind {
	case Email:
		return sendMail(c, m)
	case Telegram:
		return postJSON(ctx, telegramAPI+"/bot"+c.BotToken+"/sendMessage", map[string]any{"chat_id": c.ChatID, "text": m.text(), "disable_web_page_preview": true})
	case Webhook:
		return postJSON(ctx, c.URL, m)
	case WeCom:
		return postJSON(ctx, c.URL, map[string]any{"msgtype": "markdown", "markdown": map[string]string{"content": "**" + m.Title + "**\n" + m.text()}})
	case DingTalk:
		u := c.URL
		if c.Secret != "" {
			ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
			h := hmac.New(sha256.New, []byte(c.Secret))
			h.Write([]byte(ts + "\n" + c.Secret))
			sep := "&"
			if !strings.Contains(u, "?") {
				sep = "?"
			}
			u += sep + "timestamp=" + ts + "&sign=" + url.QueryEscape(base64.StdEncoding.EncodeToString(h.Sum(nil)))
		}
		return postJSON(ctx, u, map[string]any{"msgtype": "markdown", "markdown": map[string]string{"title": m.Title, "text": "### " + m.Title + "\n\n" + strings.ReplaceAll(m.text(), "\n", "\n\n")}})
	case Slack:
		return postJSON(ctx, c.URL, map[string]string{"text": m.text()})
	case Discord:
		return postJSON(ctx, c.URL, map[string]string{"content": m.text()})
	}
	return ErrChannel
}

func sendMail(c ChannelConfig, m Message) error {
	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	var conn net.Conn
	var err error
	d := &net.Dialer{Timeout: 15 * time.Second}
	if c.Security == "tls" {
		conn, err = tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: c.Host})
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	cl, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer cl.Close()
	if c.Security == "starttls" {
		if err := cl.StartTLS(&tls.Config{ServerName: c.Host}); err != nil {
			return err
		}
	}
	if c.Username != "" {
		if err := cl.Auth(smtp.PlainAuth("", c.Username, c.Password, c.Host)); err != nil {
			return err
		}
	}
	if err := cl.Mail(c.From); err != nil {
		return err
	}
	for _, to := range c.To {
		if err := cl.Rcpt(to); err != nil {
			return err
		}
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	subject := mime.QEncoding.Encode("utf-8", "["+m.Host+"] "+m.Title)
	fmt.Fprintf(w, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n",
		c.From, strings.Join(c.To, ", "), subject, time.Now().Format(time.RFC1123Z))
	enc := base64.StdEncoding.EncodeToString([]byte(m.text()))
	for len(enc) > 76 {
		fmt.Fprintf(w, "%s\r\n", enc[:76])
		enc = enc[76:]
	}
	fmt.Fprintf(w, "%s\r\n", enc)
	if err := w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}
