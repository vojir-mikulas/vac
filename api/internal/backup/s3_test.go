package backup

import (
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestEncodeRFC3986(t *testing.T) {
	cases := map[string]string{
		"abcXYZ09-_.~": "abcXYZ09-_.~", // unreserved pass through
		"a b":          "a%20b",
		"a/b":          "a%2Fb", // slash is encoded by the segment encoder
		"é":            "%C3%A9",
		"k=v&x":        "k%3Dv%26x",
	}
	for in, want := range cases {
		if got := encodeRFC3986(in); got != want {
			t.Errorf("encodeRFC3986(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCanonicalURIPath_PreservesSlash(t *testing.T) {
	d := newS3Destination(S3Config{Endpoint: "s3.example.com", Bucket: "b", Region: "us-east-1"}, t.TempDir())
	req, _ := http.NewRequest(http.MethodGet, d.objectURL("blog/db/a b.dump"), nil)
	got := canonicalURIPath(req.URL)
	want := "/b/blog/db/a%20b.dump"
	if got != want {
		t.Errorf("canonicalURIPath = %q, want %q", got, want)
	}
}

func TestCanonicalQuery_SortedAndEncoded(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "http://h/bucket?prefix=blog/db/&list-type=2", nil)
	got := canonicalQuery(req.URL)
	want := "list-type=2&prefix=blog%2Fdb%2F"
	if got != want {
		t.Errorf("canonicalQuery = %q, want %q", got, want)
	}
}

// HMAC-SHA256 with key "key" over the classic pangram — a stable reference
// vector that pins the signing primitive.
func TestHmacSHA256_ReferenceVector(t *testing.T) {
	got := hex.EncodeToString(hmacSHA256([]byte("key"), "The quick brown fox jumps over the lazy dog"))
	want := "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"
	if got != want {
		t.Errorf("hmacSHA256 = %s, want %s", got, want)
	}
}

func TestEmptyPayloadHashConstant(t *testing.T) {
	if got := hashHex(nil); got != emptyPayloadHash {
		t.Errorf("sha256(\"\") = %s, want %s", got, emptyPayloadHash)
	}
}

func TestSign_WellFormed(t *testing.T) {
	d := newS3Destination(S3Config{
		Endpoint:  "s3.example.com",
		Region:    "eu-west-1",
		Bucket:    "mybucket",
		AccessKey: "AKIDEXAMPLE",
		SecretKey: "secret",
	}, t.TempDir())
	d.now = func() time.Time { return time.Date(2026, 6, 1, 3, 0, 0, 0, time.UTC) }

	req, _ := http.NewRequest(http.MethodGet, d.objectURL("blog/db/x.dump"), nil)
	d.sign(req, emptyPayloadHash)

	if req.Header.Get("X-Amz-Date") != "20260601T030000Z" {
		t.Errorf("x-amz-date = %q", req.Header.Get("X-Amz-Date"))
	}
	if req.Header.Get("X-Amz-Content-Sha256") != emptyPayloadHash {
		t.Errorf("content-sha256 header missing/wrong")
	}
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
		t.Fatalf("auth prefix wrong: %q", auth)
	}
	if !strings.Contains(auth, "Credential=AKIDEXAMPLE/20260601/eu-west-1/s3/aws4_request") {
		t.Errorf("credential scope wrong: %q", auth)
	}
	if !strings.Contains(auth, "SignedHeaders=host;x-amz-content-sha256;x-amz-date") {
		t.Errorf("signed headers wrong: %q", auth)
	}
	sig := regexp.MustCompile(`Signature=([0-9a-f]{64})`).FindStringSubmatch(auth)
	if sig == nil {
		t.Errorf("signature not a 64-hex string: %q", auth)
	}

	// Deterministic: signing again with the same clock yields the same header.
	req2, _ := http.NewRequest(http.MethodGet, d.objectURL("blog/db/x.dump"), nil)
	d.sign(req2, emptyPayloadHash)
	if req2.Header.Get("Authorization") != auth {
		t.Errorf("signing not deterministic")
	}
}
