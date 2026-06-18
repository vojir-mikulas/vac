package certupload

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"testing"
	"time"
)

// gen builds a self-signed-or-not leaf cert for the given names and validity,
// returning the cert PEM and the matching key PEM. issuerKey nil ⇒ self-signed.
func gen(t *testing.T, cn string, sans []string, notBefore, notAfter time.Time) (certPEM, keyPEM []byte, key *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		DNSNames:     sans,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	kb, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kb})
	return certPEM, keyPEM, key
}

func TestValidate(t *testing.T) {
	now := time.Now()
	year := now.Add(365 * 24 * time.Hour)

	t.Run("matching pair, exact host", func(t *testing.T) {
		cert, key, _ := gen(t, "app.example.com", []string{"app.example.com"}, now.Add(-time.Hour), year)
		meta, err := Validate(cert, key, "app.example.com")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !meta.SelfSigned {
			t.Error("expected self-signed leaf to be flagged")
		}
		if meta.NotAfter.Before(now) {
			t.Error("not-after should be in the future")
		}
	})

	t.Run("wildcard covers subdomain but not apex", func(t *testing.T) {
		cert, key, _ := gen(t, "", []string{"*.example.com"}, now.Add(-time.Hour), year)
		if _, err := Validate(cert, key, "a.example.com"); err != nil {
			t.Errorf("wildcard should cover a.example.com: %v", err)
		}
		if _, err := Validate(cert, key, "example.com"); !errors.Is(err, ErrHostNotCovered) {
			t.Errorf("wildcard must NOT cover the apex; got %v", err)
		}
		if _, err := Validate(cert, key, "a.b.example.com"); !errors.Is(err, ErrHostNotCovered) {
			t.Errorf("wildcard must NOT cover a.b.example.com; got %v", err)
		}
	})

	t.Run("wrong host SAN rejected", func(t *testing.T) {
		cert, key, _ := gen(t, "", []string{"other.example.com"}, now.Add(-time.Hour), year)
		if _, err := Validate(cert, key, "app.example.com"); !errors.Is(err, ErrHostNotCovered) {
			t.Errorf("expected ErrHostNotCovered, got %v", err)
		}
	})

	t.Run("expired cert rejected", func(t *testing.T) {
		cert, key, _ := gen(t, "", []string{"app.example.com"}, now.Add(-48*time.Hour), now.Add(-24*time.Hour))
		if _, err := Validate(cert, key, "app.example.com"); !errors.Is(err, ErrExpired) {
			t.Errorf("expected ErrExpired, got %v", err)
		}
	})

	t.Run("mismatched key rejected", func(t *testing.T) {
		cert, _, _ := gen(t, "", []string{"app.example.com"}, now.Add(-time.Hour), year)
		_, otherKey, _ := gen(t, "", []string{"app.example.com"}, now.Add(-time.Hour), year)
		if _, err := Validate(cert, otherKey, "app.example.com"); err == nil {
			t.Error("expected mismatched key to be rejected")
		}
	})

	t.Run("non-PEM input rejected", func(t *testing.T) {
		if FirstCertBlock([]byte("not a pem")) {
			t.Error("FirstCertBlock should be false for non-PEM input")
		}
	})
}
