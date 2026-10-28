package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Remote storage (Google Drive / OneDrive) is authorised with the OAuth 2.0
// device authorization grant: the panel shows a code, the admin enters it on
// the provider's page, and no redirect URI back to the panel is needed. The
// admin registers their own OAuth client (Google: "TVs and Limited Input
// devices"; Microsoft: an app registration with public client flows enabled).

var (
	ErrRemoteKind    = errors.New("backup.invalid_remote")
	ErrRemoteAuth    = errors.New("backup.remote_unauthorized")
	ErrAuthPending   = errors.New("backup.auth_pending")
	ErrAuthExpired   = errors.New("backup.auth_expired")
	ErrRemoteMissing = errors.New("backup.remote_not_found")
)

// Endpoints are variables so tests can point them at a fake server.
var (
	googleDeviceURL = "https://oauth2.googleapis.com/device/code"
	googleTokenURL  = "https://oauth2.googleapis.com/token"
	googleAPI       = "https://www.googleapis.com"
	msLogin         = "https://login.microsoftonline.com"
	msGraph         = "https://graph.microsoft.com/v1.0"
	httpc           = &http.Client{Timeout: 10 * time.Minute}
	gdriveChunk     = int64(8 << 20)        // multiple of 256 KiB
	onedriveChunk   = int64(32 * 320 << 10) // 10 MiB, multiple of 320 KiB
)

const (
	gdriveScope   = "https://www.googleapis.com/auth/drive.file"
	onedriveScope = "Files.ReadWrite.AppFolder offline_access User.Read"
	folderName    = "lmcwhm-backup"
)

// RemoteConfig is stored sealed in backup_remotes.
type RemoteConfig struct {
	ClientID     string  `json:"client_id"`
	ClientSecret string  `json:"client_secret,omitempty"` // Google only
	Tenant       string  `json:"tenant,omitempty"`        // Microsoft: common | consumers | organizations | <id>
	Token        *Token  `json:"token,omitempty"`
	FolderID     string  `json:"folder_id,omitempty"` // Google Drive folder
	Device       *Device `json:"device,omitempty"`    // pending authorisation
	Account      string  `json:"account,omitempty"`
}

type Token struct {
	Access  string `json:"access"`
	Refresh string `json:"refresh"`
	Expiry  int64  `json:"expiry"`
}

// Device is a pending device authorisation as shown to the admin.
type Device struct {
	DeviceCode string `json:"device_code"`
	UserCode   string `json:"user_code"`
	URL        string `json:"url"`
	Interval   int    `json:"interval"`
	ExpiresAt  int64  `json:"expires_at"`
}

// Remote is one cloud destination.
type Remote interface {
	Upload(ctx context.Context, name string, r io.Reader, size int64) (string, error)
	Download(ctx context.Context, id string, w io.Writer) error
	Delete(ctx context.Context, id string) error
}

type oauthResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
	// device code response (Google uses verification_url, Microsoft verification_uri)
	DeviceCode string `json:"device_code"`
	UserCode   string `json:"user_code"`
	VerifyURL  string `json:"verification_url"`
	VerifyURI  string `json:"verification_uri"`
	Interval   int    `json:"interval"`
}

func postForm(ctx context.Context, u string, v url.Values) (oauthResp, error) {
	var out oauthResp
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(v.Encode()))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := httpc.Do(req)
	if err != nil {
		return out, err
	}
	defer res.Body.Close()
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out); err != nil {
		return out, fmt.Errorf("HTTP %d: unreadable response", res.StatusCode)
	}
	return out, nil
}

func (c *RemoteConfig) deviceURL(kind string) string {
	if kind == KindGDrive {
		return googleDeviceURL
	}
	return msLogin + "/" + c.tenant() + "/oauth2/v2.0/devicecode"
}

func (c *RemoteConfig) tokenURL(kind string) string {
	if kind == KindGDrive {
		return googleTokenURL
	}
	return msLogin + "/" + c.tenant() + "/oauth2/v2.0/token"
}

func (c *RemoteConfig) tenant() string {
	if c.Tenant == "" {
		return "common"
	}
	return c.Tenant
}

// StartDevice begins a device authorisation.
func (c *RemoteConfig) StartDevice(ctx context.Context, kind string) error {
	v := url.Values{"client_id": {c.ClientID}}
	if kind == KindGDrive {
		v.Set("scope", gdriveScope)
	} else {
		v.Set("scope", onedriveScope)
	}
	r, err := postForm(ctx, c.deviceURL(kind), v)
	if err != nil {
		return err
	}
	if r.Error != "" || r.DeviceCode == "" {
		return fmt.Errorf("%s: %s", r.Error, r.Description)
	}
	link := r.VerifyURL
	if link == "" {
		link = r.VerifyURI
	}
	if r.Interval <= 0 {
		r.Interval = 5
	}
	c.Device = &Device{DeviceCode: r.DeviceCode, UserCode: r.UserCode, URL: link, Interval: r.Interval,
		ExpiresAt: time.Now().Add(15 * time.Minute).Unix()}
	return nil
}

// PollDevice checks once whether the admin has approved the device code.
func (c *RemoteConfig) PollDevice(ctx context.Context, kind string) error {
	if c.Device == nil || time.Now().Unix() > c.Device.ExpiresAt {
		c.Device = nil
		return ErrAuthExpired
	}
	v := url.Values{"client_id": {c.ClientID}, "device_code": {c.Device.DeviceCode},
		"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}}
	if c.ClientSecret != "" {
		v.Set("client_secret", c.ClientSecret)
	}
	r, err := postForm(ctx, c.tokenURL(kind), v)
	if err != nil {
		return err
	}
	switch r.Error {
	case "":
	case "authorization_pending", "slow_down":
		return ErrAuthPending
	case "expired_token", "access_denied":
		c.Device = nil
		return ErrAuthExpired
	default:
		return fmt.Errorf("%s: %s", r.Error, r.Description)
	}
	c.Device = nil
	c.Token = &Token{Access: r.AccessToken, Refresh: r.RefreshToken, Expiry: time.Now().Unix() + r.ExpiresIn}
	return nil
}

// tokenSource refreshes the access token and reports changes for persistence.
type tokenSource struct {
	mu    sync.Mutex
	kind  string
	cfg   *RemoteConfig
	saved func(*RemoteConfig)
}

func (t *tokenSource) access(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tok := t.cfg.Token
	if tok == nil || tok.Refresh == "" && tok.Access == "" {
		return "", ErrRemoteAuth
	}
	if tok.Access != "" && time.Now().Unix() < tok.Expiry-60 {
		return tok.Access, nil
	}
	v := url.Values{"client_id": {t.cfg.ClientID}, "refresh_token": {tok.Refresh}, "grant_type": {"refresh_token"}}
	if t.cfg.ClientSecret != "" {
		v.Set("client_secret", t.cfg.ClientSecret)
	}
	if t.kind == KindOneDrive {
		v.Set("scope", onedriveScope)
	}
	r, err := postForm(ctx, t.cfg.tokenURL(t.kind), v)
	if err != nil {
		return "", err
	}
	if r.Error != "" || r.AccessToken == "" {
		return "", fmt.Errorf("%w: %s %s", ErrRemoteAuth, r.Error, r.Description)
	}
	tok.Access, tok.Expiry = r.AccessToken, time.Now().Unix()+r.ExpiresIn
	if r.RefreshToken != "" { // Microsoft rotates refresh tokens
		tok.Refresh = r.RefreshToken
	}
	if t.saved != nil {
		t.saved(t.cfg)
	}
	return tok.Access, nil
}

// apiCall performs an authorised JSON request; out may be nil.
func apiCall(ctx context.Context, ts *tokenSource, method, u string, body any, out any) (*http.Response, error) {
	tok, err := ts.access(ctx)
	if err != nil {
		return nil, err
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := httpc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		if res.StatusCode == http.StatusNotFound {
			return res, fmt.Errorf("%w: %s", ErrRemoteMissing, strings.TrimSpace(string(msg)))
		}
		return res, fmt.Errorf("%s %s: HTTP %d: %s", method, u, res.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			return res, err
		}
	}
	return res, nil
}

// chunkedUpload sends r in chunks to a resumable session URL. auth adds the
// bearer token (Google needs it; OneDrive upload URLs are pre-authorised).
// On a failed chunk it asks the session how much arrived and resumes there.
func chunkedUpload(ctx context.Context, session string, r io.Reader, size, chunk int64, auth func(*http.Request) error,
	resume func(ctx context.Context) (int64, error)) ([]byte, error) {
	buf := make([]byte, chunk)
	var off int64
	pending := []byte{} // bytes read from r but not yet acknowledged
	for {
		// fill pending up to one chunk
		for int64(len(pending)) < chunk && off+int64(len(pending)) < size {
			n, err := r.Read(buf[:min(chunk-int64(len(pending)), size-off-int64(len(pending)))])
			pending = append(pending, buf[:n]...)
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
		}
		if size == 0 {
			pending = pending[:0]
		}
		end := off + int64(len(pending)) - 1
		var lastErr error
	attempts:
		for attempt := 0; attempt < 4; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(attempt*attempt) * time.Second):
				}
				if got, err := resume(ctx); err == nil && got > off {
					// The server already has some of this chunk.
					skip := min(got-off, int64(len(pending)))
					pending, off = pending[skip:], off+skip
					end = off + int64(len(pending)) - 1
				}
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodPut, session, bytes.NewReader(pending))
			if err != nil {
				return nil, err
			}
			req.ContentLength = int64(len(pending))
			if size == 0 {
				req.Header.Set("Content-Range", "bytes */0")
			} else {
				req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", off, end, size))
			}
			if auth != nil {
				if err := auth(req); err != nil {
					return nil, err
				}
			}
			res, err := httpc.Do(req)
			if err != nil {
				lastErr = err
				continue
			}
			body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
			res.Body.Close()
			switch {
			case res.StatusCode == http.StatusOK || res.StatusCode == http.StatusCreated:
				return body, nil
			case res.StatusCode == 308 || res.StatusCode == http.StatusAccepted:
				next := end + 1
				if h := res.Header.Get("Range"); h != "" { // Google may keep only part of a chunk
					next = min(rangeEnd(h), next)
				}
				pending, off = pending[next-off:], next
				lastErr = nil
				break attempts
			case res.StatusCode >= 500 || res.StatusCode == http.StatusTooManyRequests:
				lastErr = fmt.Errorf("upload: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
			default:
				return nil, fmt.Errorf("upload: HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
			}
		}
		if lastErr != nil {
			return nil, lastErr
		}
		if off >= size {
			return nil, errors.New("upload: session ended without a final response")
		}
	}
}

// ---- Google Drive ----

type gdrive struct{ ts *tokenSource }

func (g *gdrive) folder(ctx context.Context) (string, error) {
	if g.ts.cfg.FolderID != "" {
		return g.ts.cfg.FolderID, nil
	}
	var f struct {
		ID string `json:"id"`
	}
	if _, err := apiCall(ctx, g.ts, http.MethodPost, googleAPI+"/drive/v3/files?fields=id", map[string]any{
		"name": folderName, "mimeType": "application/vnd.google-apps.folder",
	}, &f); err != nil {
		return "", err
	}
	g.ts.mu.Lock()
	g.ts.cfg.FolderID = f.ID
	g.ts.mu.Unlock()
	if g.ts.saved != nil {
		g.ts.saved(g.ts.cfg)
	}
	return f.ID, nil
}

func (g *gdrive) Upload(ctx context.Context, name string, r io.Reader, size int64) (string, error) {
	folder, err := g.folder(ctx)
	if err != nil {
		return "", err
	}
	tok, err := g.ts.access(ctx)
	if err != nil {
		return "", err
	}
	meta, _ := json.Marshal(map[string]any{"name": name, "parents": []string{folder}})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, googleAPI+"/upload/drive/v3/files?uploadType=resumable&fields=id", bytes.NewReader(meta))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("X-Upload-Content-Type", "application/octet-stream")
	req.Header.Set("X-Upload-Content-Length", strconv.FormatInt(size, 10))
	res, err := httpc.Do(req)
	if err != nil {
		return "", err
	}
	res.Body.Close()
	session := res.Header.Get("Location")
	if res.StatusCode != http.StatusOK || session == "" {
		if res.StatusCode == http.StatusNotFound { // folder deleted by the user
			g.ts.cfg.FolderID = ""
		}
		return "", fmt.Errorf("drive: start upload: HTTP %d", res.StatusCode)
	}
	auth := func(req *http.Request) error {
		tok, err := g.ts.access(ctx)
		req.Header.Set("Authorization", "Bearer "+tok)
		return err
	}
	resume := func(ctx context.Context) (int64, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPut, session, nil)
		req.Header.Set("Content-Range", fmt.Sprintf("bytes */%d", size))
		if err := auth(req); err != nil {
			return 0, err
		}
		res, err := httpc.Do(req)
		if err != nil {
			return 0, err
		}
		res.Body.Close()
		return rangeEnd(res.Header.Get("Range")), nil
	}
	body, err := chunkedUpload(ctx, session, r, size, gdriveChunk, auth, resume)
	if err != nil {
		return "", err
	}
	var f struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &f); err != nil || f.ID == "" {
		return "", fmt.Errorf("drive: unexpected upload response")
	}
	return f.ID, nil
}

// rangeEnd parses "bytes=0-1234" into the next offset (1235); 0 when absent.
func rangeEnd(h string) int64 {
	_, after, ok := strings.Cut(h, "-")
	if !ok {
		return 0
	}
	n, err := strconv.ParseInt(after, 10, 64)
	if err != nil {
		return 0
	}
	return n + 1
}

func (g *gdrive) Download(ctx context.Context, id string, w io.Writer) error {
	return download(ctx, g.ts, googleAPI+"/drive/v3/files/"+url.PathEscape(id)+"?alt=media", w)
}

func (g *gdrive) Delete(ctx context.Context, id string) error {
	_, err := apiCall(ctx, g.ts, http.MethodDelete, googleAPI+"/drive/v3/files/"+url.PathEscape(id), nil, nil)
	return err
}

func (g *gdrive) account(ctx context.Context) string {
	var a struct {
		User struct {
			Email string `json:"emailAddress"`
		} `json:"user"`
	}
	apiCall(ctx, g.ts, http.MethodGet, googleAPI+"/drive/v3/about?fields=user(emailAddress)", nil, &a)
	return a.User.Email
}

func download(ctx context.Context, ts *tokenSource, u string, w io.Writer) error {
	tok, err := ts.access(ctx)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, err := httpc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return ErrRemoteMissing
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %d", res.StatusCode)
	}
	_, err = io.Copy(w, res.Body)
	return err
}

// ---- OneDrive (app folder: /Apps/<app name>) ----

type onedrive struct{ ts *tokenSource }

func (o *onedrive) Upload(ctx context.Context, name string, r io.Reader, size int64) (string, error) {
	var sess struct {
		URL string `json:"uploadUrl"`
	}
	_, err := apiCall(ctx, o.ts, http.MethodPost, msGraph+"/me/drive/special/approot:/"+url.PathEscape(name)+":/createUploadSession",
		map[string]any{"item": map[string]any{"@microsoft.graph.conflictBehavior": "rename"}}, &sess)
	if err != nil {
		return "", err
	}
	resume := func(ctx context.Context) (int64, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, sess.URL, nil)
		res, err := httpc.Do(req)
		if err != nil {
			return 0, err
		}
		defer res.Body.Close()
		var st struct {
			Next []string `json:"nextExpectedRanges"`
		}
		json.NewDecoder(res.Body).Decode(&st)
		if len(st.Next) == 0 {
			return 0, errors.New("no range")
		}
		start, _, _ := strings.Cut(st.Next[0], "-")
		return strconv.ParseInt(start, 10, 64)
	}
	body, err := chunkedUpload(ctx, sess.URL, r, size, onedriveChunk, nil, resume)
	if err != nil {
		return "", err
	}
	var item struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &item); err != nil || item.ID == "" {
		return "", fmt.Errorf("onedrive: unexpected upload response")
	}
	return item.ID, nil
}

func (o *onedrive) Download(ctx context.Context, id string, w io.Writer) error {
	return download(ctx, o.ts, msGraph+"/me/drive/items/"+url.PathEscape(id)+"/content", w)
}

func (o *onedrive) Delete(ctx context.Context, id string) error {
	_, err := apiCall(ctx, o.ts, http.MethodDelete, msGraph+"/me/drive/items/"+url.PathEscape(id), nil, nil)
	return err
}

func (o *onedrive) account(ctx context.Context) string {
	var me struct {
		UPN  string `json:"userPrincipalName"`
		Mail string `json:"mail"`
	}
	apiCall(ctx, o.ts, http.MethodGet, msGraph+"/me", nil, &me)
	if me.Mail != "" {
		return me.Mail
	}
	return me.UPN
}
