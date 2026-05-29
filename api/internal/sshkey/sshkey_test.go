package sshkey_test

import (
	"crypto/ed25519"
	"encoding/pem"
	"strings"
	"testing"

	gossh "golang.org/x/crypto/ssh"

	"github.com/vojir-mikulas/vac/api/internal/sshkey"
)

func TestGenerate_Roundtrip(t *testing.T) {
	kp, err := sshkey.Generate("my-app")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if !strings.HasPrefix(kp.PublicLine, "ssh-ed25519 ") {
		t.Errorf("public line missing algo prefix: %q", kp.PublicLine)
	}
	if !strings.HasSuffix(kp.PublicLine, " vac-key-my-app") {
		t.Errorf("public line comment wrong: %q", kp.PublicLine)
	}
	if !strings.HasPrefix(kp.Fingerprint, "SHA256:") {
		t.Errorf("fingerprint format: %q", kp.Fingerprint)
	}

	// Public line round-trips through ParseAuthorizedKey.
	parsedPub, _, _, _, err := gossh.ParseAuthorizedKey([]byte(kp.PublicLine))
	if err != nil {
		t.Fatalf("ParseAuthorizedKey: %v", err)
	}
	if parsedPub.Type() != gossh.KeyAlgoED25519 {
		t.Errorf("algo = %s, want ed25519", parsedPub.Type())
	}

	// Private PEM parses and signs something the public key verifies.
	block, _ := pem.Decode(kp.PrivatePEM)
	if block == nil {
		t.Fatal("private key PEM did not decode")
	}
	signer, err := gossh.ParsePrivateKey(kp.PrivatePEM)
	if err != nil {
		t.Fatalf("ParsePrivateKey: %v", err)
	}
	if signer.PublicKey().Type() != gossh.KeyAlgoED25519 {
		t.Errorf("signer algo = %s", signer.PublicKey().Type())
	}

	msg := []byte("vac proof of possession")
	sig, err := signer.Sign(nil, msg)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := parsedPub.Verify(msg, sig); err != nil {
		t.Errorf("public key did not verify signature it should: %v", err)
	}
}

func TestGenerate_FreshKeysAreUnique(t *testing.T) {
	a, err := sshkey.Generate("same-comment")
	if err != nil {
		t.Fatal(err)
	}
	b, err := sshkey.Generate("same-comment")
	if err != nil {
		t.Fatal(err)
	}
	if a.Fingerprint == b.Fingerprint {
		t.Error("two successive generates produced identical fingerprints")
	}
	if string(a.PrivatePEM) == string(b.PrivatePEM) {
		t.Error("two successive generates produced identical private PEMs")
	}
}

func TestGenerate_EmptyCommentFallsBack(t *testing.T) {
	kp, err := sshkey.Generate("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(kp.PublicLine, " vac-key") {
		t.Errorf("expected vac-key fallback comment, got %q", kp.PublicLine)
	}
}

// reference ed25519 to keep the dependency obvious in test output.
var _ = ed25519.PublicKeySize
