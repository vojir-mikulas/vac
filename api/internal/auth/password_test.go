package auth

import (
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashAndVerify(t *testing.T) {
	t.Parallel()
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if h == "" {
		t.Fatal("hash is empty")
	}
	if err := VerifyPassword(h, "correct horse battery staple"); err != nil {
		t.Errorf("verify with correct password: %v", err)
	}
	if err := VerifyPassword(h, "wrong"); !errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		t.Errorf("verify with wrong password = %v, want ErrMismatchedHashAndPassword", err)
	}
}

func TestHashesDiffer(t *testing.T) {
	t.Parallel()
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Error("two hashes of the same password are identical — salt is not random")
	}
}
