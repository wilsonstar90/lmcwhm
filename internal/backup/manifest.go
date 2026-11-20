package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/wilsonstar90/lmcwhm/internal/config"
)

var ErrChecksum = errors.New("backup.checksum_mismatch")

type Manifest struct {
	Format       int    `json:"format"`
	PanelVersion string `json:"panel_version"`
	Kind         string `json:"kind"`
	Target       string `json:"target"`
	TargetID     int64  `json:"target_id,omitempty"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	SHA256       string `json:"sha256"`
	Encryption   string `json:"encryption,omitempty"`
	PlainSHA256  string `json:"plain_sha256,omitempty"`
	PlainSize    int64  `json:"plain_size,omitempty"`
	CreatedAt    int64  `json:"created_at"`
}

func manifestFor(u unit, name string, size int64, createdAt int64, sum string) Manifest {
	return Manifest{Format: 1, PanelVersion: config.Version, Kind: u.kind, Target: u.target,
		TargetID: u.targetID, Name: name, Size: size, SHA256: sum, CreatedAt: createdAt}
}

func manifestJSON(m Manifest) ([]byte, error) {
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func writeLocalManifest(path string, body []byte) error {
	tmp := path + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func VerifyArchive(path string) error {
	manifestPath := path + ".manifest.json"
	fi, err := os.Stat(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Size() > 1<<20 {
		return ErrBadArchive
	}
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil || manifest.Format != 1 || manifest.Name != filepath.Base(path) {
		return ErrBadArchive
	}
	archiveInfo, err := os.Stat(path)
	if err != nil {
		return err
	}
	if archiveInfo.Size() != manifest.Size {
		return fmt.Errorf("%w: archive size", ErrChecksum)
	}
	return verifyLocalCopy(path, manifest.SHA256)
}

func verifyLocalCopy(path, expected string) error {
	got, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if got != expected {
		return fmt.Errorf("%w: local copy", ErrChecksum)
	}
	return nil
}

func verifyRemoteCopy(ctx context.Context, remote Remote, location, expected string) error {
	h := sha256.New()
	if err := remote.Download(ctx, location, h); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != expected {
		return fmt.Errorf("%w: remote readback", ErrChecksum)
	}
	return nil
}

type verifiedReadCloser struct {
	*os.File
	path string
}

func (r *verifiedReadCloser) Close() error {
	err := r.File.Close()
	if removeErr := os.Remove(r.path); err == nil {
		err = removeErr
	}
	return err
}

func (sv *Service) openVerified(ctx context.Context, rec Record) (io.ReadCloser, error) {
	raw, err := sv.Open(ctx, rec)
	if err != nil || rec.SHA256 == "" {
		return raw, err
	}
	defer raw.Close()
	dir := filepath.Join(sv.LocalDir(), ".tmp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, "verify-*")
	if err != nil {
		return nil, err
	}
	path := f.Name()
	keep := false
	defer func() {
		if !keep {
			f.Close()
			os.Remove(path)
		}
	}()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), raw)
	if err == nil {
		err = f.Sync()
	}
	if err != nil {
		return nil, err
	}
	if n != rec.Size || hex.EncodeToString(h.Sum(nil)) != rec.SHA256 {
		return nil, ErrChecksum
	}
	if rec.Encrypted {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		key, err := readRecoveryKey(sv.EncryptionKeyPath)
		if err != nil || len(key) != 32 {
			return nil, errEncryption
		}
		plain, err := os.CreateTemp(dir, "decrypt-*")
		if err != nil {
			return nil, err
		}
		plainPath := plain.Name()
		if err := decryptStream(plain, f, key); err != nil {
			plain.Close()
			os.Remove(plainPath)
			return nil, err
		}
		f.Close()
		os.Remove(path)
		if _, err := plain.Seek(0, io.SeekStart); err != nil {
			plain.Close()
			os.Remove(plainPath)
			return nil, err
		}
		f, path = plain, plainPath
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	keep = true
	return &verifiedReadCloser{File: f, path: path}, nil
}

func OpenArchive(path, keyPath string) (io.ReadCloser, error) {
	if err := VerifyArchive(path); err != nil {
		return nil, err
	}
	manifestPath := path + ".manifest.json"
	body, err := os.ReadFile(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		return os.Open(path)
	}
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, ErrBadArchive
	}
	archive, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if manifest.Encryption == "" {
		return archive, nil
	}
	if manifest.Encryption != encryptionFormat {
		archive.Close()
		return nil, errEncryption
	}
	key, err := readRecoveryKey(keyPath)
	if err != nil || len(key) != 32 {
		archive.Close()
		return nil, errEncryption
	}
	plain, err := os.CreateTemp("", "lmcwhm-panel-restore-*")
	if err != nil {
		archive.Close()
		return nil, err
	}
	plainPath := plain.Name()
	if err := decryptStream(plain, archive, key); err != nil {
		plain.Close()
		os.Remove(plainPath)
		archive.Close()
		return nil, err
	}
	if err := archive.Close(); err != nil {
		plain.Close()
		os.Remove(plainPath)
		return nil, err
	}
	if _, err := plain.Seek(0, io.SeekStart); err != nil {
		plain.Close()
		os.Remove(plainPath)
		return nil, err
	}
	return &verifiedReadCloser{File: plain, path: plainPath}, nil
}
