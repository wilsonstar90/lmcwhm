// Package config resolves the on-disk layout of an lmcwhm installation.
package config

import (
	"os"
	"path/filepath"
)

// Version is overridden at build time via -ldflags.
var Version = "0.1.0-dev"

// Paths describes where lmcwhm keeps its state. Everything lives under Home
// (default /opt/lmcwhm, override with LMCWHM_HOME for development).
type Paths struct {
	Home    string
	DataDir string
	DBFile  string
	CertDir string
	LogDir  string
	PIDFile string
}

func Resolve() Paths {
	home := os.Getenv("LMCWHM_HOME")
	if home == "" {
		home = "/opt/lmcwhm"
	}
	home, _ = filepath.Abs(home)
	data := filepath.Join(home, "data")
	return Paths{
		Home:    home,
		DataDir: data,
		DBFile:  filepath.Join(data, "panel.db"),
		CertDir: filepath.Join(data, "ssl"),
		LogDir:  filepath.Join(home, "logs"),
		PIDFile: filepath.Join(data, "lmcwhm.pid"),
	}
}

// Ensure creates the directory tree with restrictive permissions.
func (p Paths) Ensure() error {
	for _, d := range []string{p.Home, p.DataDir, p.CertDir, p.LogDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}
