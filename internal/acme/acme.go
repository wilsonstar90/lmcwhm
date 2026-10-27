// Package acme issues certificates from Let's Encrypt, LiteSSL or any ACME CA
// (with optional External Account Binding) using HTTP-01 through the edge's
// port 80, or DNS-01 through Cloudflare (required for wildcards).
package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/registration"

	"github.com/wilsonstar90/lmcwhm/internal/webcfg"
)

type Challenge string

const (
	HTTP01 Challenge = "http"
	DNS01  Challenge = "dns"
)

var (
	ErrNoEmail     = errors.New("ssl.no_email")
	ErrNoDirectory = errors.New("ssl.no_directory")
	ErrNeedCF      = errors.New("ssl.dns_needs_cloudflare")
)

// ---- HTTP-01 responder (reached via HAProxy :80 -> 127.0.0.1:ACMEPort) ----

type HTTPResponder struct {
	mu     sync.RWMutex
	tokens map[string]string // token -> keyAuth
}

func NewHTTPResponder() *HTTPResponder { return &HTTPResponder{tokens: map[string]string{}} }

func (h *HTTPResponder) Present(_, token, keyAuth string) error {
	h.mu.Lock()
	h.tokens[token] = keyAuth
	h.mu.Unlock()
	return nil
}

func (h *HTTPResponder) CleanUp(_, token, _ string) error {
	h.mu.Lock()
	delete(h.tokens, token)
	h.mu.Unlock()
	return nil
}

func (h *HTTPResponder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, http01.ChallengePath(""))
	h.mu.RLock()
	ka, ok := h.tokens[token]
	h.mu.RUnlock()
	if !ok || token == r.URL.Path {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(ka))
}

// Listen serves the responder on loopback until ctx ends.
func (h *HTTPResponder) Listen(ctx context.Context, port int) error {
	srv := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), Handler: h, ReadHeaderTimeout: 5 * time.Second}
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	err = srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// ---- account ----

type account struct {
	Email     string                 `json:"email"`
	Directory string                 `json:"directory"`
	KeyPEM    string                 `json:"key"`
	Reg       *registration.Resource `json:"registration"`
	key       crypto.PrivateKey
}

func (a *account) GetEmail() string                        { return a.Email }
func (a *account) GetRegistration() *registration.Resource { return a.Reg }
func (a *account) GetPrivateKey() crypto.PrivateKey        { return a.key }

// ---- issuer ----

type Issuer struct {
	Cfg  *webcfg.Store
	HTTP *HTTPResponder
}

type Request struct {
	Domains   []string
	Challenge Challenge
}

type Result struct {
	CertPEM  []byte // leaf + chain
	KeyPEM   []byte
	NotAfter time.Time
	Issuer   string
}

// Obtain registers the ACME account on first use (persisted in webcfg) and
// orders a certificate covering req.Domains.
func (i *Issuer) Obtain(ctx context.Context, req Request, log func(string, ...any)) (Result, error) {
	cfg, err := i.Cfg.Get(ctx)
	if err != nil {
		return Result{}, err
	}
	if cfg.ACMEEmail == "" {
		return Result{}, ErrNoEmail
	}
	dir := cfg.Directory()
	if dir == "" {
		return Result{}, ErrNoDirectory
	}

	acc, err := i.loadAccount(cfg, dir)
	if err != nil {
		return Result{}, err
	}
	lc := lego.NewConfig(acc)
	lc.CADirURL = dir
	lc.Certificate.KeyType = certcrypto.EC256
	lc.UserAgent = "lmcwhm"
	client, err := lego.NewClient(lc)
	if err != nil {
		return Result{}, fmt.Errorf("acme client (%s): %w", dir, err)
	}

	switch req.Challenge {
	case DNS01:
		if cfg.CFToken == "" {
			return Result{}, ErrNeedCF
		}
		pc := cloudflare.NewDefaultConfig()
		pc.AuthToken = cfg.CFToken
		pc.PropagationTimeout = 3 * time.Minute
		p, err := cloudflare.NewDNSProviderConfig(pc)
		if err != nil {
			return Result{}, err
		}
		if err := client.Challenge.SetDNS01Provider(p); err != nil {
			return Result{}, err
		}
	default:
		if err := client.Challenge.SetHTTP01Provider(i.HTTP); err != nil {
			return Result{}, err
		}
	}

	if acc.Reg == nil {
		log("registering ACME account %s at %s", acc.Email, dir)
		var reg *registration.Resource
		if cfg.EABKid != "" {
			reg, err = client.Registration.RegisterWithExternalAccountBinding(registration.RegisterEABOptions{
				TermsOfServiceAgreed: true, Kid: cfg.EABKid, HmacEncoded: cfg.EABHmac,
			})
		} else {
			reg, err = client.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		}
		if err != nil {
			return Result{}, fmt.Errorf("acme registration: %w", err)
		}
		acc.Reg = reg
		raw, _ := json.Marshal(acc)
		if _, err := i.Cfg.Update(ctx, func(c *webcfg.Config) { c.ACMEAccount = string(raw) }); err != nil {
			return Result{}, err
		}
	}

	log("requesting certificate for %s (%s-01)", strings.Join(req.Domains, ", "), req.Challenge)
	res, err := client.Certificate.Obtain(certificate.ObtainRequest{Domains: req.Domains, Bundle: true})
	if err != nil {
		return Result{}, err
	}
	leaf, err := parseLeaf(res.Certificate)
	if err != nil {
		return Result{}, err
	}
	log("issued by %s, valid until %s", leaf.Issuer.CommonName, leaf.NotAfter.Format(time.DateOnly))
	return Result{CertPEM: res.Certificate, KeyPEM: res.PrivateKey, NotAfter: leaf.NotAfter, Issuer: leaf.Issuer.CommonName}, nil
}

// loadAccount reuses the stored account when it belongs to the same CA and
// email; otherwise a fresh key is generated (and registered by Obtain).
func (i *Issuer) loadAccount(cfg webcfg.Config, dir string) (*account, error) {
	var acc account
	if cfg.ACMEAccount != "" && json.Unmarshal([]byte(cfg.ACMEAccount), &acc) == nil &&
		acc.Directory == dir && acc.Email == cfg.ACMEEmail && acc.KeyPEM != "" {
		block, _ := pem.Decode([]byte(acc.KeyPEM))
		if block != nil {
			if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
				acc.key = k
				return &acc, nil
			}
		}
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, _ := x509.MarshalECPrivateKey(k)
	return &account{
		Email: cfg.ACMEEmail, Directory: dir, key: k,
		KeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})),
	}, nil
}

func parseLeaf(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, errors.New("ssl.invalid_pair")
	}
	return x509.ParseCertificate(block.Bytes)
}

// ParsePair validates an uploaded cert/key and returns its leaf certificate.
func ParsePair(certPEM, keyPEM []byte) (*x509.Certificate, error) {
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		return nil, errors.New("ssl.invalid_pair")
	}
	return parseLeaf(certPEM)
}
