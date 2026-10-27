// Package tlsutil manages the panel's own certificate (self-signed or custom).
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	CertFile = "panel.crt"
	KeyFile  = "panel.key"
)

// EnsureSelfSigned creates a 10-year self-signed ECDSA certificate in dir if
// none exists yet, covering localhost and every local interface address.
func EnsureSelfSigned(dir string, hosts []string) error {
	crt, key := filepath.Join(dir, CertFile), filepath.Join(dir, KeyFile)
	if _, err := os.Stat(crt); err == nil {
		if _, err := os.Stat(key); err == nil {
			return nil
		}
	}
	return GenerateSelfSigned(dir, hosts)
}

func GenerateSelfSigned(dir string, hosts []string) error {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "lmcwhm panel", Organization: []string{"lmcwhm"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	for _, h := range append(hosts, LocalIPs()...) {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else if h != "" {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return err
	}
	return writePair(dir,
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
}

// InstallCustom validates a user-supplied PEM pair and stores it as the panel cert.
func InstallCustom(dir, certPEM, keyPEM string) error {
	if _, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); err != nil {
		return errors.New("ssl.invalid_pair")
	}
	return writePair(dir, []byte(certPEM), []byte(keyPEM))
}

func writePair(dir string, certPEM, keyPEM []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, KeyFile), keyPEM, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, CertFile), certPEM, 0o644)
}

func Load(dir string) (tls.Certificate, error) {
	return tls.LoadX509KeyPair(filepath.Join(dir, CertFile), filepath.Join(dir, KeyFile))
}

type CertInfo struct {
	Subject  string    `json:"subject"`
	Issuer   string    `json:"issuer"`
	DNSNames []string  `json:"dns_names"`
	NotAfter time.Time `json:"not_after"`
}

func Info(dir string) (CertInfo, error) {
	c, err := Load(dir)
	if err != nil {
		return CertInfo{}, err
	}
	x, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		return CertInfo{}, err
	}
	names := x.DNSNames
	for _, ip := range x.IPAddresses {
		names = append(names, ip.String())
	}
	return CertInfo{Subject: x.Subject.CommonName, Issuer: x.Issuer.CommonName, DNSNames: names, NotAfter: x.NotAfter}, nil
}

// LocalIPs lists non-loopback unicast addresses of this host.
func LocalIPs() []string {
	var out []string
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			out = append(out, n.IP.String())
		}
	}
	return out
}
