package secrets

import "testing"

func TestBoxRoundTrip(t *testing.T) {
	b, err := NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	s, err := b.Seal("P@ssw0rd!")
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Open(s)
	if err != nil || got != "P@ssw0rd!" {
		t.Fatalf("got %q %v", got, err)
	}
	other, _ := NewBox(append(make([]byte, 31), 1))
	if _, err := other.Open(s); err == nil {
		t.Fatal("expected failure with wrong key")
	}
	if v, _ := b.Seal(""); v != "" {
		t.Fatal("empty should stay empty")
	}
}

func TestSigningKeyPersistence(t *testing.T) {
	dir := t.TempDir()
	k1, err := LoadSigningKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := LoadSigningKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !k1.Equal(k2) {
		t.Fatal("key changed between loads")
	}
}
