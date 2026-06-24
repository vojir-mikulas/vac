package guard

import (
	"strings"
	"testing"
	"time"
)

func testKey() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i + 1)
	}
	return k
}

func TestMintVerify_RoundTrip(t *testing.T) {
	s := New(testKey())
	tok := s.Mint(KindSession, "tool.example.com", "alice", time.Hour)
	user, ok := s.Verify(KindSession, "tool.example.com", tok)
	if !ok {
		t.Fatal("expected valid token")
	}
	if user != "alice" {
		t.Fatalf("user = %q, want alice", user)
	}
}

func TestVerify_HostBinding(t *testing.T) {
	s := New(testKey())
	tok := s.Mint(KindSession, "tool.example.com", "alice", time.Hour)
	// Case-insensitive on the bound host...
	if _, ok := s.Verify(KindSession, "TOOL.EXAMPLE.COM", tok); !ok {
		t.Error("host match should be case-insensitive")
	}
	// ...but a different host must not validate.
	if _, ok := s.Verify(KindSession, "other.example.com", tok); ok {
		t.Error("token must not validate for a different host")
	}
}

func TestVerify_KindBinding(t *testing.T) {
	s := New(testKey())
	tok := s.Mint(KindExchange, "tool.example.com", "alice", time.Hour)
	if _, ok := s.Verify(KindSession, "tool.example.com", tok); ok {
		t.Error("an exchange token must not validate as a session token")
	}
}

func TestVerify_Expired(t *testing.T) {
	s := New(testKey())
	tok := s.Mint(KindSession, "tool.example.com", "alice", -time.Second)
	if _, ok := s.Verify(KindSession, "tool.example.com", tok); ok {
		t.Error("expired token must not validate")
	}
}

func TestVerify_Tampered(t *testing.T) {
	s := New(testKey())
	tok := s.Mint(KindSession, "tool.example.com", "alice", time.Hour)

	// Flip a character in the payload segment.
	b, sig, _ := strings.Cut(tok, ".")
	mangled := flipFirst(b) + "." + sig
	if _, ok := s.Verify(KindSession, "tool.example.com", mangled); ok {
		t.Error("payload tamper must invalidate the signature")
	}

	// Flip a character in the signature segment.
	if _, ok := s.Verify(KindSession, "tool.example.com", b+"."+flipFirst(sig)); ok {
		t.Error("signature tamper must fail verification")
	}

	// Garbage with no separator.
	if _, ok := s.Verify(KindSession, "tool.example.com", "not-a-token"); ok {
		t.Error("malformed token must fail")
	}
}

func TestVerify_WrongKey(t *testing.T) {
	a := New(testKey())
	other := make([]byte, 32)
	for i := range other {
		other[i] = 0xAA
	}
	b := New(other)
	tok := a.Mint(KindSession, "tool.example.com", "alice", time.Hour)
	if _, ok := b.Verify(KindSession, "tool.example.com", tok); ok {
		t.Error("a token signed by a different key must not validate")
	}
}

func TestNilSigner_FailsClosed(t *testing.T) {
	var s *Signer // mimics a missing master key
	if got := s.Mint(KindSession, "tool.example.com", "alice", time.Hour); got != "" {
		t.Errorf("nil signer Mint = %q, want empty", got)
	}
	if _, ok := s.Verify(KindSession, "tool.example.com", "whatever"); ok {
		t.Error("nil signer must never verify a token")
	}
}

// flipFirst returns s with its first byte changed, keeping the same length.
func flipFirst(s string) string {
	if s == "" {
		return "x"
	}
	c := s[0]
	if c == 'A' {
		c = 'B'
	} else {
		c = 'A'
	}
	return string(c) + s[1:]
}
