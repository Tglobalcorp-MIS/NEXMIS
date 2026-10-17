// Package config persists the agent's identity and settings under the
// agent data directory. The private key is protected with DPAPI (machine
// scope) on Windows and the directory is restricted to SYSTEM and
// Administrators.
package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
	"github.com/tglobalcorp-mis/nexmis/internal/agent/sys"
)

const fileName = "agent.json"

// Config is the persisted agent configuration.
type Config struct {
	ServerURL   string        `json:"server_url"`
	TLS         helperipc.TLS `json:"tls"`
	EnrollToken string        `json:"enroll_token,omitempty"` // cleared after enrollment

	AgentID       string `json:"agent_id,omitempty"`
	DeviceID      string `json:"device_id,omitempty"`
	PrivateKey    []byte `json:"private_key,omitempty"` // DPAPI-protected Ed25519 seed
	ServerSignKey []byte `json:"server_sign_key,omitempty"`

	mu   sync.Mutex `json:"-"`
	path string     `json:"-"`
	key  ed25519.PrivateKey
}

// Path returns the configuration file path in dir.
func Path(dir string) string { return filepath.Join(dir, fileName) }

// Load reads the configuration from dir. A missing file yields an empty
// configuration (not an error) so the installer can create it.
func Load(dir string) (*Config, error) {
	c := &Config{path: Path(dir)}
	b, err := os.ReadFile(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", c.path, err)
	}
	if len(c.PrivateKey) > 0 {
		seed, err := sys.Unprotect(c.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("unprotect agent key: %w", err)
		}
		if len(seed) != ed25519.SeedSize {
			return nil, errors.New("agent key is corrupt")
		}
		c.key = ed25519.NewKeyFromSeed(seed)
	}
	return c, nil
}

// Save atomically writes the configuration.
func (c *Config) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := sys.SecureDir(filepath.Dir(c.path)); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// SetPath is used by the installer to write to a specific directory.
func (c *Config) SetPath(dir string) { c.path = Path(dir) }

// Enrolled reports whether the agent has an identity.
func (c *Config) Enrolled() bool {
	return c.AgentID != "" && c.key != nil && len(c.ServerSignKey) == ed25519.PublicKeySize
}

// Key returns the agent's private key.
func (c *Config) Key() ed25519.PrivateKey { return c.key }

// NewKey generates a fresh key pair (stored on Save).
func (c *Config) NewKey() (ed25519.PublicKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	prot, err := sys.Protect(priv.Seed())
	if err != nil {
		return nil, err
	}
	c.key, c.PrivateKey = priv, prot
	return pub, nil
}

// ResetIdentity forgets the enrollment (used when the server says the
// agent is unknown).
func (c *Config) ResetIdentity() {
	c.AgentID, c.DeviceID, c.PrivateKey, c.ServerSignKey, c.key = "", "", nil, nil, nil
}

// HTTPBase returns the server base URL without a trailing slash.
func (c *Config) HTTPBase() string { return strings.TrimRight(c.ServerURL, "/") }

// WSURL converts the server base URL to the WebSocket URL for path.
func (c *Config) WSURL(path string) string {
	base := c.HTTPBase()
	switch {
	case strings.HasPrefix(base, "https://"):
		base = "wss://" + strings.TrimPrefix(base, "https://")
	case strings.HasPrefix(base, "http://"):
		base = "ws://" + strings.TrimPrefix(base, "http://")
	}
	return base + path
}
