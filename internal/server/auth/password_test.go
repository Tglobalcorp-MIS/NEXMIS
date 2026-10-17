package auth

import (
	"testing"
	"time"
)

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("Correct-Horse-1")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "Correct-Horse-1") {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword(h, "wrong") {
		t.Fatal("wrong password accepted")
	}
	if VerifyPassword("", "anything") {
		t.Fatal("empty hash accepted")
	}
}

func TestPasswordPolicy(t *testing.T) {
	for pw, ok := range map[string]bool{
		"short1A":       false,
		"alllowercase1": false,
		"Lowercase12":   true,
		"lower-case-12": true,
		"密碼很長很長很長Aa1":   true,
	} {
		if got := CheckPasswordPolicy(pw) == nil; got != ok {
			t.Errorf("%q: got %v want %v", pw, got, ok)
		}
	}
}

func TestThrottle(t *testing.T) {
	th := NewThrottle(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !th.Allow("ip") {
			t.Fatal("should allow")
		}
	}
	if th.Allow("ip") {
		t.Fatal("should block 4th")
	}
	if !th.Allow("other") {
		t.Fatal("other key should be independent")
	}
}
