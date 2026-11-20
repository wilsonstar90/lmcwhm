package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncryptionRoundTripAndAuthentication(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	for _, size := range []int{0, 1, encryptionChunk, encryptionChunk + 17, 2*encryptionChunk + 31} {
		plain := make([]byte, size)
		for i := range plain {
			plain[i] = byte(i * 31)
		}
		var encrypted bytes.Buffer
		if err := encryptStream(&encrypted, bytes.NewReader(plain), key); err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		var decrypted bytes.Buffer
		if err := decryptStream(&decrypted, bytes.NewReader(encrypted.Bytes()), key); err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if !bytes.Equal(decrypted.Bytes(), plain) {
			t.Fatalf("size %d differs after decryption", size)
		}
		if size > 0 {
			tampered := append([]byte{}, encrypted.Bytes()...)
			tampered[len(tampered)-1] ^= 1
			if err := decryptStream(io.Discard, bytes.NewReader(tampered), key); !errors.Is(err, errEncryption) {
				t.Fatalf("size %d accepted tampering: %v", size, err)
			}
		}
	}
}

func TestEncryptedBackupRestoreAndDisable(t *testing.T) {
	sv := newSvc(t)
	ctx := context.Background()
	encodedKey, err := sv.EnableEncryption()
	if err != nil || encodedKey == "" {
		t.Fatalf("enable: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "data")
	write(t, filepath.Join(dir, "a.txt"), "before")
	if err := sv.Run(ctx, Plan{Kind: KindPath, Target: dir, Dests: []string{DestLocal}}, 0, io.Discard); err != nil {
		t.Fatal(err)
	}
	recs, err := sv.List(ctx, KindPath)
	if err != nil || len(recs) != 1 || !recs[0].Encrypted {
		t.Fatalf("encrypted record: %+v %v", recs, err)
	}
	body, err := os.ReadFile(recs[0].ManifestLocation)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil || manifest.Encryption != encryptionFormat || manifest.PlainSHA256 == "" || manifest.SHA256 == manifest.PlainSHA256 {
		t.Fatalf("encrypted manifest: %+v %v", manifest, err)
	}
	archive, err := os.ReadFile(recs[0].Location)
	if err != nil || !bytes.HasPrefix(archive, encryptionMagic) {
		t.Fatalf("archive is not encrypted: %v", err)
	}
	if err := sv.DisableEncryption(); err != nil {
		t.Fatal(err)
	}
	if status := sv.EncryptionStatus()["enabled"]; status != false {
		t.Fatalf("encryption still enabled: %v", status)
	}
	write(t, filepath.Join(dir, "a.txt"), "after")
	if err := sv.Restore(ctx, recs[0].ID, 0, t.Logf); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(body) != "before" {
		t.Fatalf("encrypted restore: %q", body)
	}
	opened, err := OpenArchive(recs[0].Location, sv.EncryptionKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if body, err := io.ReadAll(opened); err != nil || !bytes.HasPrefix(body, []byte{0x1f, 0x8b}) {
		t.Fatalf("open encrypted archive: %v", err)
	}
	plainCopy := filepath.Join(t.TempDir(), "copy-without-manifest.enc")
	if err := os.WriteFile(plainCopy, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	openedCopy, err := OpenArchive(plainCopy, sv.EncryptionKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if body, err := io.ReadAll(openedCopy); err != nil || !bytes.HasPrefix(body, []byte{0x1f, 0x8b}) {
		t.Fatalf("open archive without manifest: %v", err)
	}
	openedCopy.Close()
	if err := sv.Run(ctx, Plan{Kind: KindPath, Target: dir, Dests: []string{DestLocal}}, 0, io.Discard); err != nil {
		t.Fatal(err)
	}
	recs, _ = sv.List(ctx, KindPath)
	if len(recs) != 2 || recs[0].Encrypted {
		t.Fatalf("disabled encryption did not create a plain archive: %+v", recs)
	}
	if !strings.HasSuffix(recs[0].Name, ".tar.gz") {
		t.Fatalf("unexpected plain archive name: %s", recs[0].Name)
	}
}
