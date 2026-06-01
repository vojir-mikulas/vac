package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

func TestParseRef(t *testing.T) {
	tests := []struct {
		ref, kind, name string
	}{
		{"refs/heads/main", KindPush, "main"},
		{"refs/heads/release/1.2", KindPush, "release/1.2"},
		{"refs/tags/v1.2.3", KindTag, "v1.2.3"},
		{"main", KindPush, "main"}, // bare generic ref
	}
	for _, tc := range tests {
		t.Run(tc.ref, func(t *testing.T) {
			kind, name := ParseRef(tc.ref)
			if kind != tc.kind || name != tc.name {
				t.Errorf("ParseRef(%q) = (%q,%q), want (%q,%q)", tc.ref, kind, name, tc.kind, tc.name)
			}
		})
	}
}

func TestMatchTriggers(t *testing.T) {
	rules := []store.DeployTrigger{
		{Event: KindPush, Filter: "main"},
		{Event: KindPush, Filter: "release/*"},
		{Event: KindTag, Filter: "v*"},
		{Event: KindTag, Filter: ""}, // any tag
	}
	tests := []struct {
		name      string
		kind, ref string
		rules     []store.DeployTrigger
		wantMatch bool
	}{
		{"exact branch", KindPush, "main", rules, true},
		{"glob branch", KindPush, "release/1.2", rules, true},
		{"glob branch nested", KindPush, "release/a/b", rules, true},
		{"non-matching branch", KindPush, "feature/x", rules, false},
		{"tag matches v*", KindTag, "v2.0.0", rules, true},
		{"tag matches empty filter", KindTag, "nightly", rules, true},
		{"push does not fire tag rule", KindPush, "v2.0.0", []store.DeployTrigger{{Event: KindTag, Filter: "v*"}}, false},
		{"tag does not fire push rule", KindTag, "main", []store.DeployTrigger{{Event: KindPush, Filter: ""}}, false},
		{"no rules", KindPush, "main", nil, false},
		{"empty push filter matches any branch", KindPush, "anything", []store.DeployTrigger{{Event: KindPush, Filter: ""}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchTriggers(tc.rules, tc.kind, tc.ref); got != tc.wantMatch {
				t.Errorf("MatchTriggers(%q,%q) = %v, want %v", tc.kind, tc.ref, got, tc.wantMatch)
			}
		})
	}
}

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pattern, s string
		want       bool
	}{
		{"main", "main", true},
		{"main", "mainx", false},
		{"release/*", "release/1.2", true},
		{"v*", "v1.0.0", true},
		{"v*", "1.0.0", false},
		{"feat-?", "feat-1", true},
		{"feat-?", "feat-12", false},
		{"a.b", "axb", false}, // dot is literal, not regex any
	}
	for _, tc := range tests {
		t.Run(tc.pattern+"|"+tc.s, func(t *testing.T) {
			if got := globMatch(tc.pattern, tc.s); got != tc.want {
				t.Errorf("globMatch(%q,%q) = %v, want %v", tc.pattern, tc.s, got, tc.want)
			}
		})
	}
}

func TestExtractRef(t *testing.T) {
	t.Run("from json body", func(t *testing.T) {
		body := []byte(`{"ref":"refs/heads/main","after":"abc"}`)
		r := httptest.NewRequest(http.MethodPost, "/webhooks/x", nil)
		got, err := ExtractRef(r, body)
		if err != nil || got != "refs/heads/main" {
			t.Fatalf("ExtractRef = (%q,%v)", got, err)
		}
	})
	t.Run("from query when body empty", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/webhooks/x?ref=refs/tags/v1", nil)
		got, err := ExtractRef(r, nil)
		if err != nil || got != "refs/tags/v1" {
			t.Fatalf("ExtractRef = (%q,%v)", got, err)
		}
	})
	t.Run("no ref", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/webhooks/x", nil)
		if _, err := ExtractRef(r, []byte(`{"zen":"ping"}`)); !errors.Is(err, ErrNoRef) {
			t.Fatalf("err = %v, want ErrNoRef", err)
		}
	})
}

func TestVerify(t *testing.T) {
	secret := []byte("s3cr3t")
	body := []byte(`{"ref":"refs/heads/main"}`)

	ghSig := func(sec, b []byte) string {
		mac := hmac.New(sha256.New, sec)
		mac.Write(b)
		return "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}

	t.Run("github valid", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/webhooks/x", nil)
		r.Header.Set("X-Hub-Signature-256", ghSig(secret, body))
		if err := Verify(secret, body, r); err != nil {
			t.Fatalf("Verify = %v, want nil", err)
		}
	})
	t.Run("github wrong secret", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/webhooks/x", nil)
		r.Header.Set("X-Hub-Signature-256", ghSig([]byte("wrong"), body))
		if err := Verify(secret, body, r); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("Verify = %v, want ErrBadSignature", err)
		}
	})
	t.Run("github tampered body", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/webhooks/x", nil)
		r.Header.Set("X-Hub-Signature-256", ghSig(secret, body))
		if err := Verify(secret, []byte(`{"ref":"refs/heads/evil"}`), r); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("Verify = %v, want ErrBadSignature", err)
		}
	})
	t.Run("gitlab valid", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/webhooks/x", nil)
		r.Header.Set("X-Gitlab-Token", "s3cr3t")
		if err := Verify(secret, body, r); err != nil {
			t.Fatalf("Verify = %v, want nil", err)
		}
	})
	t.Run("gitlab wrong token", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/webhooks/x", nil)
		r.Header.Set("X-Gitlab-Token", "nope")
		if err := Verify(secret, body, r); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("Verify = %v, want ErrBadSignature", err)
		}
	})
	t.Run("generic header token", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/webhooks/x", nil)
		r.Header.Set("X-VAC-Token", "s3cr3t")
		if err := Verify(secret, body, r); err != nil {
			t.Fatalf("Verify = %v, want nil", err)
		}
	})
	t.Run("query token is rejected (no secrets in URLs)", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/webhooks/x?token=s3cr3t", nil)
		if err := Verify(secret, body, r); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("Verify = %v, want ErrBadSignature (query token must not authenticate)", err)
		}
	})
	t.Run("no credential", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/webhooks/x", nil)
		if err := Verify(secret, body, r); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("Verify = %v, want ErrBadSignature", err)
		}
	})
	t.Run("malformed github hex", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/webhooks/x", nil)
		r.Header.Set("X-Hub-Signature-256", "sha256=zzzz")
		if err := Verify(secret, body, r); !errors.Is(err, ErrBadSignature) {
			t.Fatalf("Verify = %v, want ErrBadSignature", err)
		}
	})
}
