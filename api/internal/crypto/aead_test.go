package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func newBox(t *testing.T) *Box {
	t.Helper()
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	b, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	plaintext := []byte("super-secret-token")

	sealed, err := b.Seal(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, plaintext) {
		t.Fatal("sealed output contains plaintext bytes")
	}

	got, err := b.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("round-trip mismatch: %q vs %q", got, plaintext)
	}
}

func TestEmptyPlaintext(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	sealed, err := b.Seal(nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %d bytes", len(got))
	}
}

func TestLargePlaintext(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	plaintext := make([]byte, 1<<20) // 1 MiB
	if _, err := rand.Read(plaintext); err != nil {
		t.Fatal(err)
	}
	sealed, err := b.Seal(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Error("1 MiB round-trip mismatch")
	}
}

func TestTamperedCiphertext(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	sealed, err := b.Seal([]byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	// Flip a bit in the tag at the end of the sealed blob.
	sealed[len(sealed)-1] ^= 0x01
	if _, err := b.Open(sealed); err == nil {
		t.Fatal("expected error on tampered ciphertext, got nil")
	}
}

func TestTamperedNonce(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	sealed, err := b.Seal([]byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	sealed[0] ^= 0x01
	if _, err := b.Open(sealed); err == nil {
		t.Fatal("expected error on tampered nonce, got nil")
	}
}

func TestWrongKeyRejected(t *testing.T) {
	t.Parallel()
	a := newBox(t)
	b := newBox(t)
	sealed, err := a.Seal([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Open(sealed); err == nil {
		t.Fatal("expected error when opening with a different key, got nil")
	}
}

func TestNoncesAreUnique(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	pt := []byte("same plaintext")
	a, _ := b.Seal(pt)
	c, _ := b.Seal(pt)
	if bytes.Equal(a, c) {
		t.Fatal("two seals produced identical output — nonce is not random")
	}
}

func TestBadKeySize(t *testing.T) {
	t.Parallel()
	for _, n := range []int{0, 16, 31, 33, 64} {
		key := make([]byte, n)
		if _, err := New(key); err == nil {
			t.Errorf("New with %d-byte key should error", n)
		}
	}
}

func TestShortCiphertextRejected(t *testing.T) {
	t.Parallel()
	b := newBox(t)
	if _, err := b.Open([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected error on too-short ciphertext, got nil")
	}
}
