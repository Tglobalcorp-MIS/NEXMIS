package protocol

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

func TestAuthRoundTrip(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	nonce := []byte("0123456789abcdef")
	now := time.Now()
	r := AuthResponse{AgentID: "a1", Timestamp: now, Signature: SignAuth(priv, nonce, "a1", now)}
	if err := VerifyAuth(pub, nonce, r, now); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// Signature must be bound to the agent id.
	r2 := r
	r2.AgentID = "a2"
	if err := VerifyAuth(pub, nonce, r2, now); err == nil {
		t.Fatal("expected failure for different agent id")
	}
	// And to the nonce.
	if err := VerifyAuth(pub, []byte("other"), r, now); err == nil {
		t.Fatal("expected failure for different nonce")
	}
	// Clock skew.
	if err := VerifyAuth(pub, nonce, r, now.Add(10*time.Minute)); err == nil {
		t.Fatal("expected skew failure")
	}
}

func TestCommandSignVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()
	c := Command{CommandID: "c1", AgentID: "a1", Kind: CmdPowerShell, IssuedAt: now, ExpiresAt: now.Add(time.Hour)}
	sc, err := SignCommand(priv, c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := VerifyCommand(pub, "a1", sc, now)
	if err != nil || got.CommandID != "c1" {
		t.Fatalf("verify: %v %+v", err, got)
	}
	if _, err := VerifyCommand(pub, "a2", sc, now); !errors.Is(err, ErrWrongAgent) {
		t.Fatalf("want ErrWrongAgent, got %v", err)
	}
	if _, err := VerifyCommand(pub, "a1", sc, now.Add(2*time.Hour)); !errors.Is(err, ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}
	sc.Payload[len(sc.Payload)-2] ^= 1
	if _, err := VerifyCommand(pub, "a1", sc, now); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
	// An auth signature must not verify as a command signature.
	authSig := SignAuth(priv, []byte("n"), "a1", now)
	if _, err := VerifyCommand(pub, "a1", SignedCommand{Payload: []byte("x"), Signature: authSig}, now); err == nil {
		t.Fatal("cross-purpose signature accepted")
	}
}
