package backup

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	encryptionFormat = "aes-256-gcm-chunked-v1"
	encryptionChunk  = 1 << 20
)

var encryptionMagic = []byte("LMCWHMENC1")

var errEncryption = errors.New("backup.invalid_encryption")

func (sv *Service) EncryptionStatus() map[string]any {
	enabled := false
	fingerprint := ""
	if key, err := readRecoveryKey(sv.EncryptionKeyPath); err == nil {
		sum := sha256Sum(key)
		fingerprint = sum[:16]
		_, err := os.Stat(sv.EncryptionKeyPath + ".disabled")
		enabled = errors.Is(err, os.ErrNotExist)
	}
	return map[string]any{"enabled": enabled, "key_available": fingerprint != "", "key_fingerprint": fingerprint}
}

func (sv *Service) EnableEncryption() (string, error) {
	key, err := readRecoveryKey(sv.EncryptionKeyPath)
	if err != nil {
		return "", err
	}
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(sv.EncryptionKeyPath), 0o700); err != nil {
			return "", err
		}
		tmp := sv.EncryptionKeyPath + ".tmp"
		if err := os.WriteFile(tmp, key, 0o600); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, sv.EncryptionKeyPath); err != nil {
			return "", err
		}
	}
	if err := os.Remove(sv.EncryptionKeyPath + ".disabled"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

func (sv *Service) DisableEncryption() error {
	return os.WriteFile(sv.EncryptionKeyPath+".disabled", nil, 0o600)
}

func (sv *Service) encryptionKey() ([]byte, error) {
	if sv.EncryptionKeyPath == "" {
		return nil, nil
	}
	key, err := readRecoveryKey(sv.EncryptionKeyPath)
	if err != nil || key == nil {
		return nil, err
	}
	if _, err := os.Stat(sv.EncryptionKeyPath + ".disabled"); err == nil {
		return nil, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return key, nil
}

func readRecoveryKey(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	key, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errEncryption
	}
	return key, nil
}

func sha256Sum(data []byte) string {
	return fmt.Sprintf("%x", sha256Bytes(data))
}

func sha256Bytes(data []byte) []byte {
	h := sha256.New()
	h.Write(data)
	return h.Sum(nil)
}

func cipherAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errEncryption
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func encryptionAAD(prefix []byte, counter uint64, last bool) []byte {
	out := append([]byte{}, encryptionMagic...)
	out = append(out, prefix...)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], counter)
	out = append(out, n[:]...)
	if last {
		out = append(out, 1)
	} else {
		out = append(out, 0)
	}
	return out
}

func nonceFor(prefix []byte, counter uint64) []byte {
	nonce := append([]byte{}, prefix...)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], counter)
	return append(nonce, n[:]...)
}

func encryptStream(dst io.Writer, src io.Reader, key []byte) error {
	aead, err := cipherAEAD(key)
	if err != nil {
		return err
	}
	prefix := make([]byte, 4)
	if _, err := rand.Read(prefix); err != nil {
		return err
	}
	if _, err := dst.Write(append(encryptionMagic, prefix...)); err != nil {
		return err
	}
	plain := make([]byte, encryptionChunk)
	for counter := uint64(0); ; counter++ {
		n, readErr := io.ReadFull(src, plain)
		last := errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF)
		if readErr != nil && !last {
			return readErr
		}
		sealed := aead.Seal(nil, nonceFor(prefix, counter), plain[:n], encryptionAAD(prefix, counter, last))
		if _, err := dst.Write(sealed); err != nil {
			return err
		}
		if last {
			return nil
		}
	}
}

func decryptStream(dst io.Writer, src io.Reader, key []byte) error {
	aead, err := cipherAEAD(key)
	if err != nil {
		return err
	}
	header := make([]byte, len(encryptionMagic)+4)
	if _, err := io.ReadFull(src, header); err != nil {
		return errEncryption
	}
	if string(header[:len(encryptionMagic)]) != string(encryptionMagic) {
		return errEncryption
	}
	prefix := header[len(encryptionMagic):]
	ciphertext := make([]byte, encryptionChunk+aead.Overhead())
	for counter := uint64(0); ; counter++ {
		n, readErr := io.ReadFull(src, ciphertext)
		last := errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF)
		if readErr != nil && !last {
			return readErr
		}
		plain, err := aead.Open(nil, nonceFor(prefix, counter), ciphertext[:n], encryptionAAD(prefix, counter, last))
		if err != nil {
			return errEncryption
		}
		if _, err := dst.Write(plain); err != nil {
			return err
		}
		if last {
			var extra [1]byte
			if n, _ := src.Read(extra[:]); n != 0 {
				return errEncryption
			}
			return nil
		}
	}
}

type decryptedReadCloser struct {
	io.Reader
	parent io.Closer
	close  func()
	once   sync.Once
}

func (r *decryptedReadCloser) Close() error {
	var err error
	r.once.Do(func() {
		err = r.parent.Close()
		if r.close != nil {
			r.close()
		}
	})
	return err
}

func decryptReader(src io.ReadCloser, key []byte, closeParent func()) (io.ReadCloser, error) {
	if _, err := cipherAEAD(key); err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	reader := &decryptedReadCloser{Reader: pr, parent: src, close: closeParent}
	go func() {
		err := decryptStream(pw, src, key)
		pw.CloseWithError(err)
	}()
	return reader, nil
}

func DecryptFileTo(source, destination, keyPath string) (err error) {
	key, err := readRecoveryKey(keyPath)
	if err != nil || len(key) != 32 {
		return errEncryption
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.Remove(destination)
		}
	}()
	if err = decryptStream(out, in, key); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func IsEncryptedArchive(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	header := make([]byte, len(encryptionMagic))
	_, err = io.ReadFull(f, header)
	return err == nil && bytes.Equal(header, encryptionMagic), nil
}
