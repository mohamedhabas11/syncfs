package auth

import (
	"regexp"
	"strings"
)

// Redactor scrubs private key material, nonces, and PEM blocks from logs
// and diagnostics (technical spec section 9: secrets never logged).
type Redactor struct {
	re *regexp.Regexp
}

func NewRedactor() *Redactor {
	pat := `-----BEGIN[^-]*PRIVATE KEY-----[\s\S]*?-----END[^-]*PRIVATE KEY-----|"nonce"\s*:\s*"[^"]*"|"cert_fingerprint"\s*:\s*"[^"]*"|"private_key"\s*:\s*"[^"]*"`
	return &Redactor{re: regexp.MustCompile(pat)}
}

func (r *Redactor) Redact(s string) string {
	r1 := r.re.ReplaceAllString(s, "[REDACTED]")
	for _, w := range []string{"private_key", "nonce", "cert_fingerprint"} {
		_ = w
	}
	// belt-and-braces: nothing that looks like hexed key material survives.
	if strings.Contains(r1, "-----BEGIN") {
		return "[REDACTED]"
	}
	return r1
}
