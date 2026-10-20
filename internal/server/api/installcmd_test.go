package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstallScript(t *testing.T) {
	s := installScript("https://mis.corp.local", "nxe_abc", "")
	for _, want := range []string{
		"-Headers @{'X-NEXMIS-Enroll-Token'='nxe_abc'}",
		"-Uri ('https://mis.corp.local/api/agent/v1/installer?arch='+(Get-Variable a -ValueOnly))",
		"install --server 'https://mis.corp.local' --token 'nxe_abc'; exit",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "NexmisPin") || strings.Contains(s, "--pin") {
		t.Errorf("unpinned script must use the system trust store:\n%s", s)
	}

	pin := strings.Repeat("ab", 32)
	s = installScript("https://mis.corp.local", "nxe_abc", pin)
	if !strings.Contains(s, "[NexmisPin]::Enable([byte[]](0xab,0xab,") || !strings.Contains(s, "--pin '"+pin+"'") {
		t.Errorf("pinned script must check and pass the pin:\n%s", s)
	}
	if strings.Contains(pinCallbackCS, "'") {
		t.Error("pinCallbackCS is embedded in a single-quoted string")
	}
	// The line is pasted into PowerShell or cmd.exe inside double quotes.
	if strings.ContainsAny(s, shellUnsafe) {
		t.Errorf("script contains characters a shell would alter:\n%s", s)
	}

	if got := psQuote("it's"); got != "'it''s'" {
		t.Errorf("psQuote: %s", got)
	}
}

func writeSelfSigned(t *testing.T) (path string, der []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mis.corp.local"},
		DNSNames:     []string{"mis.corp.local"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, der
}

func TestUntrustedCertPin(t *testing.T) {
	path, der := writeSelfSigned(t)
	sum := sha256.Sum256(der)
	if got := untrustedCertPin(path, "mis.corp.local"); got != hex.EncodeToString(sum[:]) {
		t.Fatalf("self-signed pin = %q", got)
	}
	if got := untrustedCertPin("", "mis.corp.local"); got != "" {
		t.Fatalf("no built-in TLS must not pin: %q", got)
	}
	if got := untrustedCertPin(filepath.Join(t.TempDir(), "missing.pem"), "mis.corp.local"); got != "" {
		t.Fatalf("unreadable cert must not pin: %q", got)
	}
}
