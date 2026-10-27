// Package secret encrypts small blobs (channel tokens, monitor headers) at rest
// with AES-256-GCM.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Box struct{ aead cipher.AEAD }

// Load builds a Box from a base64 key, or from <dataDir>/secret.key, creating
// that file with a fresh random key on first run.
func Load(b64, dataDir string) (*Box, error) {
	if b64 == "" {
		path := filepath.Join(dataDir, "secret.key")
		raw, err := os.ReadFile(path)
		switch {
		case err == nil:
			b64 = strings.TrimSpace(string(raw))
		case errors.Is(err, os.ErrNotExist):
			key := make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return nil, err
			}
			b64 = base64.StdEncoding.EncodeToString(key)
			if err := os.WriteFile(path, []byte(b64+"\n"), 0o600); err != nil {
				return nil, fmt.Errorf("write secret.key: %w", err)
			}
		default:
			return nil, err
		}
	}
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(key) != 32 {
		return nil, errors.New("secret key must be 32 bytes, base64 encoded")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

func (b *Box) Seal(plain []byte) []byte {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return b.aead.Seal(nonce, nonce, plain, nil)
}

func (b *Box) Open(sealed []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("ciphertext too short")
	}
	return b.aead.Open(nil, sealed[:n], sealed[n:], nil)
}
