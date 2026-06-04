package backup

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/netguard"
)

// S3Config is the sealed dest_config shape for the `s3` destination. It targets
// any S3-compatible endpoint (AWS S3 / Backblaze B2 / MinIO) using path-style
// addressing and SigV4 — implemented against the stdlib so VAC pulls no SDK
// (matching the project's hand-rolled-to-dodge-deps house style; see
// docs/deviations.md).
type S3Config struct {
	Endpoint  string `json:"endpoint"`   // host[:port], e.g. s3.amazonaws.com or minio:9000
	Region    string `json:"region"`     // e.g. us-east-1 (default when empty)
	Bucket    string `json:"bucket"`     //
	AccessKey string `json:"access_key"` //
	SecretKey string `json:"secret_key"` //
	UseSSL    bool   `json:"use_ssl"`    // https when true, http otherwise
	Prefix    string `json:"prefix"`     // optional key prefix within the bucket
}

func (c *S3Config) validate() error {
	switch {
	case c.Endpoint == "":
		return fmt.Errorf("backup: s3 endpoint is required")
	case c.Bucket == "":
		return fmt.Errorf("backup: s3 bucket is required")
	case c.AccessKey == "" || c.SecretKey == "":
		return fmt.Errorf("backup: s3 access_key and secret_key are required")
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	return nil
}

// S3Destination uploads via a single PUT after staging the (unknown-length)
// dump to a temp file so Content-Length is known — S3 PUT requires it, and an
// io.Pipe stream has no length. Staging is on disk, never in RAM.
type S3Destination struct {
	cfg        S3Config
	http       *http.Client
	now        func() time.Time
	stagingDir string
}

func newS3Destination(cfg S3Config, stagingDir string) *S3Destination {
	// The endpoint is operator-controlled and never validated against internal
	// addresses, and s3Error reflects the response body back to the caller — a
	// working SSRF read primitive without a guard. The netguard dialer refuses to
	// connect to private/loopback/link-local/metadata addresses and pins the dial
	// to the validated IP (no DNS-rebind window).
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = netguard.DialContext(10*time.Second, 30*time.Second)
	return &S3Destination{
		cfg:        cfg,
		http:       &http.Client{Timeout: 10 * time.Minute, Transport: transport},
		now:        time.Now,
		stagingDir: stagingDir,
	}
}

func (s *S3Destination) scheme() string {
	if s.cfg.UseSSL {
		return "https"
	}
	return "http"
}

// objectKey prepends the configured prefix to a backup key.
func (s *S3Destination) objectKey(key string) string {
	if s.cfg.Prefix == "" {
		return key
	}
	return strings.Trim(s.cfg.Prefix, "/") + "/" + key
}

// objectURL builds the path-style URL for an object key (already prefix-applied).
func (s *S3Destination) objectURL(objKey string) string {
	return fmt.Sprintf("%s://%s/%s/%s", s.scheme(), s.cfg.Endpoint, s.cfg.Bucket, encodeS3Path(objKey))
}

func (s *S3Destination) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	// Stage to a temp file so the PUT has a Content-Length. The reader is fully
	// drained here, which also unblocks the docker-exec writer feeding it.
	if err := os.MkdirAll(s.stagingDir, 0o750); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(s.stagingDir, "s3-stage-*")
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	size, err := io.Copy(tmp, r)
	if err != nil {
		return 0, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}

	objKey := s.objectKey(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, s.objectURL(objKey), tmp)
	if err != nil {
		return 0, err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	s.sign(req, unsignedPayload)
	resp, err := s.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return 0, s3Error("PUT", objKey, resp)
	}
	return size, nil
}

func (s *S3Destination) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	objKey := s.objectKey(key)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.objectURL(objKey), nil)
	if err != nil {
		return nil, err
	}
	s.sign(req, emptyPayloadHash)
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer func() { _ = resp.Body.Close() }()
		return nil, s3Error("GET", objKey, resp)
	}
	return resp.Body, nil
}

func (s *S3Destination) Prune(ctx context.Context, prefix string, keep int) error {
	if keep <= 0 {
		return nil
	}
	keys, err := s.list(ctx, s.objectKey(prefix))
	if err != nil {
		return err
	}
	if len(keys) <= keep {
		return nil
	}
	// Newest first by sortable timestamp embedded in the key.
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	var firstErr error
	for _, k := range keys[keep:] {
		if err := s.delete(ctx, k); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// list returns every object key under prefix via ListObjectsV2 (one page is
// plenty for the small per-(app,service) backup sets we prune).
func (s *S3Destination) list(ctx context.Context, prefix string) ([]string, error) {
	q := url.Values{}
	q.Set("list-type", "2")
	q.Set("prefix", prefix)
	u := fmt.Sprintf("%s://%s/%s?%s", s.scheme(), s.cfg.Endpoint, s.cfg.Bucket, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	s.sign(req, emptyPayloadHash)
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return nil, s3Error("LIST", prefix, resp)
	}
	var parsed struct {
		Contents []struct {
			Key string `xml:"Key"`
		} `xml:"Contents"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("backup: parse s3 list: %w", err)
	}
	out := make([]string, 0, len(parsed.Contents))
	for _, c := range parsed.Contents {
		out = append(out, c.Key)
	}
	return out, nil
}

func (s *S3Destination) delete(ctx context.Context, objKey string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, s.objectURL(objKey), nil)
	if err != nil {
		return err
	}
	s.sign(req, emptyPayloadHash)
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return s3Error("DELETE", objKey, resp)
	}
	return nil
}

func s3Error(op, key string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("backup: s3 %s %s: %d %s", op, key, resp.StatusCode, msg)
}

// --- AWS Signature Version 4 (stdlib) ---

const (
	unsignedPayload  = "UNSIGNED-PAYLOAD"
	emptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" // sha256("")
)

// sign adds the x-amz-date, x-amz-content-sha256, host and Authorization headers
// to req per SigV4 for the S3 service. payloadHash is UNSIGNED-PAYLOAD for
// streamed PUTs or the sha256 of the (empty) body for the rest.
func (s *S3Destination) sign(req *http.Request, payloadHash string) {
	now := s.now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	req.Header.Set("Host", req.URL.Host)

	signedHeaders, canonicalHeaders := canonicalHeaderSet(req)

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURIPath(req.URL),
		canonicalQuery(req.URL),
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := strings.Join([]string{dateStamp, s.cfg.Region, "s3", "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		hashHex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := sigV4SigningKey(s.cfg.SecretKey, dateStamp, s.cfg.Region, "s3")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))

	auth := fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.cfg.AccessKey, scope, signedHeaders, signature,
	)
	req.Header.Set("Authorization", auth)
}

// canonicalHeaderSet returns the signed-headers list and the canonical headers
// block for the host + x-amz-* headers we set. Keeping the signed set minimal
// (host, x-amz-content-sha256, x-amz-date) is valid SigV4 and avoids surprises
// from Go-added headers.
func canonicalHeaderSet(req *http.Request) (signed, canonical string) {
	names := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		var v string
		switch n {
		case "host":
			v = req.URL.Host
		default:
			v = req.Header.Get(n)
		}
		b.WriteString(n)
		b.WriteByte(':')
		b.WriteString(strings.TrimSpace(v))
		b.WriteByte('\n')
	}
	return strings.Join(names, ";"), b.String()
}

// canonicalURIPath URI-encodes each path segment per SigV4 (preserving "/").
func canonicalURIPath(u *url.URL) string {
	if u.Path == "" {
		return "/"
	}
	segments := strings.Split(u.Path, "/")
	for i, seg := range segments {
		segments[i] = encodeRFC3986(seg)
	}
	return strings.Join(segments, "/")
}

// canonicalQuery builds the canonical query string: sorted, RFC3986-encoded.
func canonicalQuery(u *url.URL) string {
	q := u.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := q[k]
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, encodeRFC3986(k)+"="+encodeRFC3986(v))
		}
	}
	return strings.Join(parts, "&")
}

// encodeS3Path encodes an object key for use in a URL path, preserving "/".
func encodeS3Path(key string) string {
	segments := strings.Split(key, "/")
	for i, seg := range segments {
		segments[i] = encodeRFC3986(seg)
	}
	return strings.Join(segments, "/")
}

// encodeRFC3986 percent-encodes per AWS rules: unreserved chars (A-Z a-z 0-9
// -._~) pass through; everything else is %XX uppercase.
func encodeRFC3986(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func sigV4SigningKey(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func hashHex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
