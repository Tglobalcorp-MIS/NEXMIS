package api

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// installCommand returns the one-line installer and, when the built-in TLS
// certificate is not trusted by the system store (typically self-signed),
// the SHA-256 pin the installer uses for it. The command is "" when the
// server URL is not HTTPS (the download would be open to tampering and the
// agent refuses cleartext servers anyway) or contains characters the line
// cannot carry safely.
func (s *Server) installCommand(token string) (cmd, pin string) {
	base := strings.TrimRight(s.svc.General(context.Background()).ServerURL, "/")
	if !strings.HasPrefix(base, "https://") {
		return "", ""
	}
	if s.svc.Cfg != nil {
		if u, err := url.Parse(base); err == nil {
			pin = untrustedCertPin(s.svc.Cfg.TLSCert, u.Hostname())
		}
	}
	script := installScript(base, token, pin)
	if strings.ContainsAny(script, shellUnsafe) {
		return "", ""
	}
	return `powershell -NoProfile -ExecutionPolicy Bypass -Command "` + script + `"`, pin
}

// shellUnsafe are the characters a pasting shell could alter inside the
// double-quoted -Command argument: PowerShell expands $ and ` (and " ends
// the string); cmd.exe expands %VAR% even within quotes. installScript
// avoids them all, so the same line works pasted into PowerShell or cmd.exe.
// -EncodedCommand would avoid quoting too, but endpoint protection (Trend
// Micro behaviour monitoring) kills encoded PowerShell that downloads files.
const shellUnsafe = "$`\"%"

// pinCallbackCS validates the server by the SHA-256 of its leaf certificate
// (fail closed). It is compiled C#, like the NexmisTls class in
// install-agent.ps1, because Invoke-WebRequest calls the validation callback
// on a thread without a PowerShell runspace, where a script block fails.
// It must not contain ' (it is embedded in a single-quoted string) nor any
// shellUnsafe character.
const pinCallbackCS = `using System; using System.Net; using System.Net.Security; using System.Security.Cryptography; using System.Security.Cryptography.X509Certificates; ` +
	`public static class NexmisPin { static byte[] Pin; ` +
	`public static void Enable(byte[] pin) { Pin = pin; ServicePointManager.ServerCertificateValidationCallback = Check; } ` +
	`public static bool Check(object s, X509Certificate c, X509Chain ch, SslPolicyErrors e) { ` +
	`if (c == null || Pin == null) return false; ` +
	`byte[] h; using (SHA256 sha = SHA256.Create()) { h = sha.ComputeHash(c.GetRawCertData()); } ` +
	`if (h.Length != Pin.Length) return false; ` +
	`for (int i = 0; i < h.Length; i++) { if (h[i] != Pin[i]) return false; } ` +
	`return true; } }`

// installScript downloads the agent with the enrollment token in a header
// (never in the URL) and runs "nexmis-agent install". Variables are reached
// through Set-Variable/Get-Variable because $ is unavailable. With a pin,
// the download is authenticated the way the agent will later authenticate
// the server: by the leaf certificate's SHA-256 instead of the chain.
func installScript(base, token, pin string) string {
	var b strings.Builder
	b.WriteString("Set-Variable ErrorActionPreference Stop; Set-Variable ProgressPreference SilentlyContinue; ")
	b.WriteString("[Net.ServicePointManager]::SecurityProtocol=[Net.SecurityProtocolType]::Tls12; ")
	if raw, err := hex.DecodeString(pin); pin != "" && err == nil {
		bs := make([]string, len(raw))
		for i, c := range raw {
			bs[i] = fmt.Sprintf("0x%02x", c)
		}
		b.WriteString("Add-Type -TypeDefinition '" + pinCallbackCS + "'; ")
		fmt.Fprintf(&b, "[NexmisPin]::Enable([byte[]](%s)); ", strings.Join(bs, ","))
	}
	b.WriteString("Set-Variable a amd64; if ((Get-Item Env:PROCESSOR_ARCHITECTURE).Value -eq 'ARM64') { Set-Variable a arm64 }; ")
	b.WriteString("Set-Variable p (Join-Path ([IO.Path]::GetTempPath()) 'nexmis-agent.exe'); ")
	fmt.Fprintf(&b, "Invoke-WebRequest -UseBasicParsing -Uri (%s+(Get-Variable a -ValueOnly)) -Headers @{'X-NEXMIS-Enroll-Token'=%s} -OutFile (Get-Variable p -ValueOnly); ",
		psQuote(base+"/api/agent/v1/installer?arch="), psQuote(token))
	fmt.Fprintf(&b, "& (Get-Variable p -ValueOnly) install --server %s --token %s", psQuote(base), psQuote(token))
	if pin != "" {
		fmt.Fprintf(&b, " --pin %s", psQuote(pin))
	}
	b.WriteString("; exit (Get-Variable LASTEXITCODE -ValueOnly)")
	return b.String()
}

// psQuote returns s as a PowerShell single-quoted (verbatim) string.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// untrustedCertPin returns the hex SHA-256 of the leaf certificate in
// certFile when it does not verify for host against the system roots (for
// example a self-signed certificate), and "" when it does, when there is no
// built-in TLS (a reverse proxy terminates it) or the file is unreadable.
// A publicly or corporately trusted certificate is deliberately not pinned,
// so renewing it does not break agents.
func untrustedCertPin(certFile, host string) string {
	if certFile == "" {
		return ""
	}
	data, err := os.ReadFile(certFile)
	if err != nil {
		return ""
	}
	var certs []*x509.Certificate
	for {
		var blk *pem.Block
		blk, data = pem.Decode(data)
		if blk == nil {
			break
		}
		if blk.Type != "CERTIFICATE" {
			continue
		}
		if c, err := x509.ParseCertificate(blk.Bytes); err == nil {
			certs = append(certs, c)
		}
	}
	if len(certs) == 0 {
		return ""
	}
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	if _, err := certs[0].Verify(x509.VerifyOptions{DNSName: host, Intermediates: inter}); err == nil {
		return ""
	}
	sum := sha256.Sum256(certs[0].Raw)
	return hex.EncodeToString(sum[:])
}
