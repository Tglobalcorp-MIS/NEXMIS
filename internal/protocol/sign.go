package protocol

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Domain-separation prefixes so a signature for one purpose can never be
// replayed as another.
const (
	authSigPrefix    = "nexmis-auth-v1\x00"
	commandSigPrefix = "nexmis-cmd-v1\x00"
)

// MaxClockSkew is the tolerated difference between agent and server clocks
// during the handshake.
const MaxClockSkew = 5 * time.Minute

// AuthMessage builds the byte string an agent signs during the handshake.
func AuthMessage(nonce []byte, agentID string, ts time.Time) []byte {
	msg := make([]byte, 0, len(authSigPrefix)+len(nonce)+len(agentID)+32)
	msg = append(msg, authSigPrefix...)
	msg = append(msg, nonce...)
	msg = append(msg, 0)
	msg = append(msg, agentID...)
	msg = append(msg, 0)
	msg = append(msg, ts.UTC().Format(time.RFC3339Nano)...)
	return msg
}

// SignAuth signs a handshake challenge.
func SignAuth(priv ed25519.PrivateKey, nonce []byte, agentID string, ts time.Time) []byte {
	return ed25519.Sign(priv, AuthMessage(nonce, agentID, ts))
}

// VerifyAuth checks a handshake response against the agent's public key.
func VerifyAuth(pub ed25519.PublicKey, nonce []byte, r AuthResponse, now time.Time) error {
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("invalid public key")
	}
	if skew := now.Sub(r.Timestamp); skew > MaxClockSkew || skew < -MaxClockSkew {
		return fmt.Errorf("clock skew too large: %s", skew.Round(time.Second))
	}
	if !ed25519.Verify(pub, AuthMessage(nonce, r.AgentID, r.Timestamp), r.Signature) {
		return errors.New("bad signature")
	}
	return nil
}

// SignCommand marshals and signs a command for delivery to an agent.
func SignCommand(priv ed25519.PrivateKey, c Command) (SignedCommand, error) {
	payload, err := json.Marshal(c)
	if err != nil {
		return SignedCommand{}, err
	}
	return SignedCommand{Payload: payload, Signature: ed25519.Sign(priv, append([]byte(commandSigPrefix), payload...))}, nil
}

// Command verification errors.
var (
	ErrBadSignature = errors.New("command signature invalid")
	ErrWrongAgent   = errors.New("command addressed to another agent")
	ErrExpired      = errors.New("command expired")
)

// VerifyCommand checks the signature, the addressee and the validity window
// and returns the decoded command. Replay protection (rejecting a CommandID
// already executed) is the caller's responsibility.
func VerifyCommand(pub ed25519.PublicKey, agentID string, sc SignedCommand, now time.Time) (Command, error) {
	var c Command
	if len(pub) != ed25519.PublicKeySize {
		return c, ErrBadSignature
	}
	if !ed25519.Verify(pub, append([]byte(commandSigPrefix), sc.Payload...), sc.Signature) {
		return c, ErrBadSignature
	}
	if err := json.Unmarshal(sc.Payload, &c); err != nil {
		return c, fmt.Errorf("decode command: %w", err)
	}
	if c.AgentID != agentID {
		return c, ErrWrongAgent
	}
	if !c.ExpiresAt.IsZero() && now.After(c.ExpiresAt) {
		return c, ErrExpired
	}
	return c, nil
}
