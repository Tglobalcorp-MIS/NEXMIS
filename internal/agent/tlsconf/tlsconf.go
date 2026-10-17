// Package tlsconf builds the TLS client configuration the agent and its
// helper use to talk to nexmis-server.
package tlsconf

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tglobalcorp-mis/nexmis/internal/agent/helperipc"
)

// Config returns a TLS config honouring an optional private CA and an
// optional leaf-certificate pin. With a pin, chain validation is replaced
// by the pin check (useful for self-signed servers).
func Config(t helperipc.TLS) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if t.CAPEM != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(t.CAPEM)) {
			return nil, errors.New("invalid CA certificate")
		}
		cfg.RootCAs = pool
	}
	pin := strings.ToLower(strings.ReplaceAll(t.PinSHA256, ":", ""))
	if pin != "" {
		want, err := hex.DecodeString(pin)
		if err != nil || len(want) != sha256.Size {
			return nil, errors.New("invalid certificate pin")
		}
		cfg.InsecureSkipVerify = true // replaced by VerifyPeerCertificate below
		cfg.VerifyPeerCertificate = func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("no server certificate")
			}
			got := sha256.Sum256(raw[0])
			if hex.EncodeToString(got[:]) != hex.EncodeToString(want) {
				return fmt.Errorf("server certificate pin mismatch")
			}
			return nil
		}
		return cfg, nil
	}
	cfg.InsecureSkipVerify = t.InsecureSkipVerify
	return cfg, nil
}

// HTTPClient returns an HTTP client using Config. Proxy settings come from
// the environment (HTTPS_PROXY), which Windows services rarely have, so
// direct connections are the norm.
func HTTPClient(t helperipc.TLS, timeout time.Duration) (*http.Client, error) {
	cfg, err := Config(t)
	if err != nil {
		return nil, err
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = cfg
	tr.MaxIdleConns = 2
	tr.IdleConnTimeout = 30 * time.Second
	return &http.Client{Transport: tr, Timeout: timeout}, nil
}
