package core

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/hkdf"

	"github.com/tglobalcorp-mis/nexmis/internal/protocol"
	"github.com/tglobalcorp-mis/nexmis/internal/server/store"
)

func deriveKey(master []byte, info string) []byte {
	k := make([]byte, 32)
	r := hkdf.New(sha256.New, master, nil, []byte(info))
	if _, err := r.Read(k); err != nil {
		panic(err)
	}
	return k
}

// fileToken is a stateless, HMAC-signed download authorization. Tokens
// survive server restarts and are bound to one device.
type fileToken struct {
	Kind    string    `json:"k"` // pkg | agent
	ID      uuid.UUID `json:"i,omitempty"`
	Arch    string    `json:"a,omitempty"`
	Device  uuid.UUID `json:"d"`
	Expires int64     `json:"e"`
}

func (s *Service) signFileToken(t fileToken) string {
	payload, _ := json.Marshal(t)
	mac := hmac.New(sha256.New, s.tokKey)
	mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Service) parseFileToken(tok string) (*fileToken, error) {
	p, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return nil, errors.New("malformed token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return nil, err
	}
	want, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, s.tokKey)
	mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil), want) {
		return nil, errors.New("bad token signature")
	}
	var t fileToken
	if err := json.Unmarshal(payload, &t); err != nil {
		return nil, err
	}
	if time.Now().Unix() > t.Expires {
		return nil, errors.New("token expired")
	}
	return &t, nil
}

// fileURL returns a server-relative download URL for a token.
func (s *Service) fileURL(t fileToken) string {
	return "/api/agent/v1/files/" + s.signFileToken(t)
}

// ServeAgentFile streams a package or agent binary for a valid token.
func (s *Service) ServeAgentFile(w http.ResponseWriter, r *http.Request) {
	t, err := s.parseFileToken(r.PathValue("token"))
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var path, name string
	switch t.Kind {
	case "pkg":
		p, err := s.Store.GetPackage(r.Context(), t.ID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		path, name = p.StoragePath, p.FileName
	case "agent":
		m := s.agentManifest()
		b, ok := m.Builds[t.Arch]
		if !ok {
			http.NotFound(w, r)
			return
		}
		path, name = filepath.Join(s.agentDir(), filepath.Base(b.File)), filepath.Base(b.File)
	default:
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(name, `"`, "")+`"`)
	http.ServeContent(w, r, name, st.ModTime(), f) // supports Range for resumable downloads
}

// ---- agent self-update ----

// AgentManifest describes the agent binaries published under
// <data>/agent/manifest.json by the release packaging.
type AgentManifest struct {
	Version   string                       `json:"version"`
	Builds    map[string]AgentManifestFile `json:"builds"`    // "windows/amd64" -> file
	Installer string                       `json:"installer"` // optional MSI for first-time installs
	// AutoUpdate makes connected agents update themselves to Version.
	AutoUpdate bool `json:"auto_update"`
}

type AgentManifestFile struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

var manifestCache struct {
	sync.Mutex
	mod time.Time
	m   AgentManifest
}

func (s *Service) agentDir() string {
	if s.Cfg.AgentDir != "" {
		return s.Cfg.AgentDir
	}
	return filepath.Join(s.Cfg.DataDir, "agent")
}

func (s *Service) agentManifest() AgentManifest {
	path := filepath.Join(s.agentDir(), "manifest.json")
	st, err := os.Stat(path)
	if err != nil {
		return AgentManifest{}
	}
	manifestCache.Lock()
	defer manifestCache.Unlock()
	if st.ModTime().Equal(manifestCache.mod) {
		return manifestCache.m
	}
	var m AgentManifest
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &m) == nil {
		manifestCache.m, manifestCache.mod = m, st.ModTime()
	} else {
		s.Log.Error("invalid agent manifest", "path", path)
	}
	return manifestCache.m
}

// AgentManifest exposes the manifest to the API (download page).
func (s *Service) AgentManifest() AgentManifest { return s.agentManifest() }

// AgentInstallerPath returns the installer (MSI preferred) for download.
func (s *Service) AgentInstallerPath() (string, string, bool) {
	m := s.agentManifest()
	if m.Installer != "" {
		p := filepath.Join(s.agentDir(), filepath.Base(m.Installer))
		if _, err := os.Stat(p); err == nil {
			return p, filepath.Base(m.Installer), true
		}
	}
	if b, ok := m.Builds["windows/amd64"]; ok {
		p := filepath.Join(s.agentDir(), filepath.Base(b.File))
		if _, err := os.Stat(p); err == nil {
			return p, filepath.Base(b.File), true
		}
	}
	return "", "", false
}

func (s *Service) applyAgentUpdate(cfg *protocol.AgentConfig, d *store.Device) {
	m := s.agentManifest()
	if !m.AutoUpdate || m.Version == "" || len(m.Builds) == 0 {
		return
	}
	cfg.LatestAgent = m.Version
	cfg.AgentBuilds = map[string]protocol.AgentBuild{}
	for arch, b := range m.Builds {
		cfg.AgentBuilds[arch] = protocol.AgentBuild{
			URL:    s.fileURL(fileToken{Kind: "agent", Arch: arch, Device: d.ID, Expires: time.Now().Add(7 * 24 * time.Hour).Unix()}),
			SHA256: b.SHA256,
			Size:   b.Size,
		}
	}
}

// AgentDir is where agent binaries are published.
func (s *Service) AgentDir() string { return s.agentDir() }
