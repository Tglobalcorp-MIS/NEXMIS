// Package config loads nexmis-server configuration from environment
// variables (NEXMIS_*), optionally seeded from a KEY=VALUE file.
package config

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// Listen is the HTTP(S) listen address.
	Listen string
	// PublicURL is the externally reachable base URL (used in installer
	// commands and agent download links), e.g. https://mis.corp.local.
	PublicURL string
	// TLSCert / TLSKey enable built-in TLS. Leave empty when a reverse proxy
	// terminates TLS.
	TLSCert string
	TLSKey  string
	// TrustProxy makes the server honour X-Forwarded-For / X-Forwarded-Proto
	// from the reverse proxy.
	TrustProxy bool

	DatabaseURL string
	// DataDir holds the server signing key, uploaded packages and agent
	// binaries.
	DataDir string
	// AgentDir holds published agent binaries and manifest.json. Defaults
	// to <DataDir>/agent; the container image points it at the bundled
	// binaries so upgrading the server image also publishes the new agent.
	AgentDir string
	// MasterKey (32 bytes) encrypts secrets stored in the database.
	MasterKey []byte

	// InitialAdminPassword seeds the local "admin" account on first start.
	InitialAdminPassword string

	SessionTTL   time.Duration
	LogLevel     string
	LogFormat    string // text | json
	Timezone     string
	MaxUploadMiB int64
	// AllowedOrigins lists extra Origins accepted for state-changing
	// requests (the PublicURL origin is always accepted).
	AllowedOrigins []string
}

// Load reads configuration. If NEXMIS_CONFIG points at a file, its
// KEY=VALUE lines are applied as defaults before the environment.
func Load() (*Config, error) {
	if f := os.Getenv("NEXMIS_CONFIG"); f != "" {
		if err := loadEnvFile(f); err != nil {
			return nil, fmt.Errorf("read %s: %w", f, err)
		}
	}
	c := &Config{
		Listen:               env("NEXMIS_LISTEN", ":8080"),
		PublicURL:            strings.TrimRight(env("NEXMIS_PUBLIC_URL", "http://localhost:8080"), "/"),
		TLSCert:              env("NEXMIS_TLS_CERT", ""),
		TLSKey:               env("NEXMIS_TLS_KEY", ""),
		TrustProxy:           envBool("NEXMIS_TRUST_PROXY", false),
		DatabaseURL:          env("NEXMIS_DATABASE_URL", "postgres://nexmis:nexmis@localhost:5432/nexmis?sslmode=disable"),
		DataDir:              env("NEXMIS_DATA_DIR", "./data"),
		InitialAdminPassword: env("NEXMIS_INITIAL_ADMIN_PASSWORD", ""),
		LogLevel:             env("NEXMIS_LOG_LEVEL", "info"),
		LogFormat:            env("NEXMIS_LOG_FORMAT", "text"),
		Timezone:             env("NEXMIS_TIMEZONE", "Asia/Taipei"),
	}
	var err error
	if c.SessionTTL, err = time.ParseDuration(env("NEXMIS_SESSION_TTL", "12h")); err != nil {
		return nil, fmt.Errorf("NEXMIS_SESSION_TTL: %w", err)
	}
	if c.MaxUploadMiB, err = strconv.ParseInt(env("NEXMIS_MAX_UPLOAD_MIB", "2048"), 10, 64); err != nil {
		return nil, fmt.Errorf("NEXMIS_MAX_UPLOAD_MIB: %w", err)
	}
	for _, o := range strings.Split(env("NEXMIS_ALLOWED_ORIGINS", ""), ",") {
		if o = strings.TrimSpace(o); o != "" {
			c.AllowedOrigins = append(c.AllowedOrigins, strings.TrimRight(o, "/"))
		}
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return nil, errors.New("NEXMIS_TLS_CERT and NEXMIS_TLS_KEY must be set together")
	}
	c.AgentDir = env("NEXMIS_AGENT_DIR", filepath.Join(c.DataDir, "agent"))
	if err := os.MkdirAll(c.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	if c.MasterKey, err = loadMasterKey(c.DataDir); err != nil {
		return nil, err
	}
	return c, nil
}

// loadMasterKey takes NEXMIS_MASTER_KEY (base64, 32 bytes) or, if unset,
// reads/creates <data>/master.key. Losing this key makes stored secrets
// (e.g. the AD bind password) unreadable, so it must be backed up.
func loadMasterKey(dataDir string) ([]byte, error) {
	if v := os.Getenv("NEXMIS_MASTER_KEY"); v != "" {
		k, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(k) != 32 {
			return nil, errors.New("NEXMIS_MASTER_KEY must be 32 bytes, base64 encoded")
		}
		return k, nil
	}
	path := filepath.Join(dataDir, "master.key")
	if b, err := os.ReadFile(path); err == nil {
		k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
		if err != nil || len(k) != 32 {
			return nil, fmt.Errorf("%s is corrupt", path)
		}
		return k, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(k)+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write master key: %w", err)
	}
	return k, nil
}

func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
	return s.Err()
}

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	v, err := strconv.ParseBool(env(k, strconv.FormatBool(def)))
	if err != nil {
		return def
	}
	return v
}
