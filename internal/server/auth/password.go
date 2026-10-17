// Package auth implements password hashing and login throttling.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (OWASP recommended minimum: m=19MiB, t=2, p=1).
const (
	argonTime    = 2
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
)

// HashPassword returns a PHC-formatted Argon2id hash.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// dummyHash is verified against when a user does not exist so that response
// timing does not reveal valid usernames.
var dummyHash, _ = HashPassword("nexmis-dummy-password")

// VerifyPassword checks pw against a PHC hash in constant time.
func VerifyPassword(hash, pw string) bool {
	if hash == "" {
		verify(dummyHash, pw)
		return false
	}
	return verify(hash, pw)
}

func verify(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ErrWeakPassword describes the password policy.
var ErrWeakPassword = errors.New("密碼至少 10 個字元，且需包含大小寫字母、數字或符號其中三類")

// CheckPasswordPolicy enforces a minimum complexity for local accounts.
func CheckPasswordPolicy(pw string) error {
	if len([]rune(pw)) < 10 {
		return ErrWeakPassword
	}
	var upper, lower, digit, other bool
	for _, r := range pw {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		default:
			other = true
		}
	}
	n := 0
	for _, b := range []bool{upper, lower, digit, other} {
		if b {
			n++
		}
	}
	if n < 3 {
		return ErrWeakPassword
	}
	return nil
}

// Throttle limits login attempts per key (client IP) with a sliding window.
type Throttle struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func NewThrottle(max int, window time.Duration) *Throttle {
	return &Throttle{max: max, window: window, hits: map[string][]time.Time{}}
}

// Allow records an attempt and reports whether it is within the limit.
func (t *Throttle) Allow(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	cut := now.Add(-t.window)
	h := t.hits[key][:0]
	for _, ts := range t.hits[key] {
		if ts.After(cut) {
			h = append(h, ts)
		}
	}
	if len(h) >= t.max {
		t.hits[key] = h
		return false
	}
	t.hits[key] = append(h, now)
	if len(t.hits) > 10000 {
		for k, v := range t.hits {
			if len(v) == 0 || v[len(v)-1].Before(cut) {
				delete(t.hits, k)
			}
		}
	}
	return true
}
