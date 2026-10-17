package core

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP returns the client address, honouring X-Forwarded-For only when
// the server is configured to sit behind a trusted reverse proxy.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			// The right-most entry was appended by our proxy.
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
		if xr := r.Header.Get("X-Real-IP"); xr != "" {
			return strings.TrimSpace(xr)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func clientIP(r *http.Request, trustProxy bool) string { return ClientIP(r, trustProxy) }
