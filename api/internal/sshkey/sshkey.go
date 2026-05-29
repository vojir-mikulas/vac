// Package sshkey mints ED25519 deploy-key pairs in the OpenSSH wire
// format VAC needs to hand to Git hosts and to `git -c
// core.sshCommand=...` at clone time.
package sshkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// KeyPair is the artefact of Generate. PublicLine is suitable for pasting
// into a Git host's deploy-keys list. PrivatePEM is the OpenSSH-PEM
// representation that `ssh` reads via `-i`.
type KeyPair struct {
	PublicLine  string
	PrivatePEM  []byte
	Fingerprint string
}

// Generate mints a fresh ED25519 keypair. `comment` is appended to the
// public-key line; pass the app slug so the deploy-key list on the Git
// host is readable.
func Generate(comment string) (KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, fmt.Errorf("sshkey: ed25519: %w", err)
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return KeyPair{}, fmt.Errorf("sshkey: wrap public key: %w", err)
	}
	// MarshalAuthorizedKey returns "ssh-ed25519 BASE64\n" — strip the
	// trailing newline and append our comment so the line we hand the user
	// is one tidy `<algo> <key> <comment>`.
	authLine := strings.TrimRight(string(ssh.MarshalAuthorizedKey(sshPub)), "\n")
	publicLine := fmt.Sprintf("%s %s", authLine, sanitiseComment(comment))

	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return KeyPair{}, fmt.Errorf("sshkey: marshal private key: %w", err)
	}
	privatePEM := pem.EncodeToMemory(block)

	return KeyPair{
		PublicLine:  publicLine,
		PrivatePEM:  privatePEM,
		Fingerprint: ssh.FingerprintSHA256(sshPub),
	}, nil
}

// sanitiseComment keeps the comment safe for the single-line authorized_keys
// format: no newlines, no leading/trailing whitespace.
func sanitiseComment(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if s == "" {
		return "vac-key"
	}
	return "vac-key-" + s
}
