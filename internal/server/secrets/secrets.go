// Package secrets provides symmetric encryption of stored secrets and the
// server's command-signing key.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Box encrypts small secrets with AES-256-GCM.
type Box struct{ aead cipher.AEAD }

func NewBox(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("master key must be 32 bytes")
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

// Seal returns "v1:" + base64(nonce || ciphertext).
func (b *Box) Seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return "v1:" + base64.StdEncoding.EncodeToString(ct), nil
}

func (b *Box) Open(sealed string) (string, error) {
	if sealed == "" {
		return "", nil
	}
	rest, ok := strings.CutPrefix(sealed, "v1:")
	if !ok {
		return "", errors.New("unknown secret format")
	}
	raw, err := base64.StdEncoding.DecodeString(rest)
	if err != nil {
		return "", err
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("secret too short")
	}
	pt, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", errors.New("cannot decrypt secret (wrong master key?)")
	}
	return string(pt), nil
}

// LoadSigningKey reads or creates <dataDir>/signing.key, the Ed25519 key
// whose public half every agent pins at enrollment. Rotating it requires
// re-enrolling all agents, so it must be backed up with the master key.
func LoadSigningKey(dataDir string) (ed25519.PrivateKey, error) {
	path := filepath.Join(dataDir, "signing.key")
	if b, err := os.ReadFile(path); err == nil {
		seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(seed) != ed25519.SeedSize {
			return nil, fmt.Errorf("%s is corrupt", path)
		}
		return ed25519.NewKeyFromSeed(seed), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
		return nil, err
	}
	return priv, nil
}

// RandomToken returns a URL-safe random token with the given prefix.
func RandomToken(prefix string, nbytes int) string {
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// HashToken returns the hex SHA-256 of a token, used for at-rest storage of
// bearer secrets (sessions, enrollment tokens).
func HashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}
