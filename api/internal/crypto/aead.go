// Package crypto provides authenticated encryption (AES-256-GCM) keyed by the
// VAC master key. It is used to seal secrets at rest: env vars, SSH private
// keys, TOTP secrets.
//
// The wire format is `nonce || ciphertext || tag`, where nonce is a fresh
// random 12-byte value. A different key yields different ciphertexts for the
// same plaintext, and any single bit flip causes Open to fail.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// KeySize is the required length, in bytes, of the master key.
const KeySize = 32

// Box wraps an AEAD cipher with a single key. It is safe for concurrent use.
type Box struct {
	aead cipher.AEAD
}

// New returns a Box keyed by the given 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("crypto: key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: aes init: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: gcm init: %w", err)
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts and authenticates plaintext, returning nonce||ciphertext||tag.
func (b *Box) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("crypto: nonce: %w", err)
	}
	return b.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Open decrypts and verifies the output of Seal. It returns an error if the
// ciphertext has been tampered with or was sealed by a different key.
func (b *Box) Open(sealed []byte) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(sealed) < ns+b.aead.Overhead() {
		return nil, errors.New("crypto: ciphertext too short")
	}
	nonce, ct := sealed[:ns], sealed[ns:]
	plaintext, err := b.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: decrypt: %w", err)
	}
	return plaintext, nil
}
