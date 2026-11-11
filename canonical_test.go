package sigv4chunk

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCanonicalURIPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"empty becomes slash", "", "/"},
		{"plain path", "/bucket/object", "/bucket/object"},
		{"space encoded", "/bucket/my object", "/bucket/my%20object"},
		{"reserved chars encoded", "/a+b/c=d?d/[1]", "/a%2Bb/c%3Dd%3Fd/%5B1%5D"},
		{"slash preserved between segments", "/a/b/c", "/a/b/c"},
		{"percent sign encoded", "/100%done", "/100%25done"},
		{"dot segments not normalized", "/a/./b/../c", "/a/./b/../c"},
		{"utf8 byte-wise encoding", "/café", "/caf%C3%A9"},
		// The gateway convention re-encodes raw percent signs; it never
		// decodes and reinterprets the client's percent encoding.
		{"raw percent sign re-encoded", "/a%20b", "/a%2520b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := canonicalURIPath(tc.path); got != tc.want {
				t.Fatalf("canonicalURIPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestCanonicalQueryString(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", "", ""},
		{"sorted by key", "b=2&a=1", "a=1&b=2"},
		{"duplicate keys sorted by value", "k=v2&k=v1", "k=v1&k=v2"},
		{"empty value kept", "a=&b=2", "a=&b=2"},
		{"space is percent twenty", "a=b+c", "a=b%2Bc"},
		{"encoded space preserved", "a=b%20c", "a=b%20c"},
		{"slash encoded in query", "prefix=a/b", "prefix=a%2Fb"},
		{"unreserved untouched", "a=A-z_0.9~", "a=A-z_0.9~"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := canonicalQueryString(tc.raw); got != tc.want {
				t.Fatalf("canonicalQueryString(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestCanonicalHeaders(t *testing.T) {
	r, err := http.NewRequest(http.MethodPost, "https://example.com/path", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Host = "example.com"
	r.Header.Set("X-Signed", "  value   with   spaces ")
	r.Header.Add("X-Multi", "one")
	r.Header.Add("X-Multi", "two")

	block, list := canonicalHeaderBlock(r, []string{"x-multi", "host", "x-signed"})
	wantBlock := strings.Join([]string{
		"host:example.com\n",
		"x-multi:one,two\n",
		"x-signed:value with spaces\n",
	}, "")
	if block != wantBlock {
		t.Fatalf("canonical header block = %q, want %q", block, wantBlock)
	}
	if list != "host;x-multi;x-signed" {
		t.Fatalf("signed header list = %q", list)
	}
}

// TestVerifierFailClosed verifies the streaming primitive never returns
// unverified bytes once a chunk fails, and that a missing final empty chunk
// and a declared-length mismatch are both errors.
func TestVerifierFailClosed(t *testing.T) {
	amzTime := testNow
	r := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/o",
		[][]byte{[]byte("first-chunk"), []byte("second-chunk")}, amzTime)

	auth, err := parseAuthorization(r.Header.Get("Authorization"))
	if err != nil {
		t.Fatal(err)
	}
	amzDate := r.Header.Get(amzDateHeader)
	key := signingKey(testSecret, auth.date, auth.region, auth.service)

	wire, _ := io.ReadAll(r.Body)
	tampered := append([]byte(nil), wire...)
	firstLF := strings.Index(string(tampered), "\r\n")
	tampered[firstLF+2] ^= 0x01

	verifier := newChunkVerifier(
		strings.NewReader(string(tampered)),
		amzDate, auth.credentialScope(), key, auth.signature, 24,
	)
	// The first read must fail rather than hand back tampered bytes.
	buf := make([]byte, 64)
	if _, err := verifier.Read(buf); err == nil {
		t.Fatal("expected verification error on first Read")
	}
	if _, err := verifier.Read(buf); err == nil {
		t.Fatal("verifier must fail permanently after the first error")
	}
}

func TestVerifierMissingFinalChunkAndLengthMismatch(t *testing.T) {
	r := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/o",
		[][]byte{[]byte("abcdef")}, testNow)
	auth, err := parseAuthorization(r.Header.Get("Authorization"))
	if err != nil {
		t.Fatal(err)
	}
	amzDate := r.Header.Get(amzDateHeader)
	key := signingKey(testSecret, auth.date, auth.region, auth.service)

	t.Run("declared length mismatch", func(t *testing.T) {
		verifier := newChunkVerifier(
			strings.NewReader(mustReadAll(t, r.Body)),
			amzDate, auth.credentialScope(), key, auth.signature, 5,
		)
		if _, err := verifier.verifyAll(); err == nil {
			t.Fatal("expected declared-length mismatch error")
		}
	})

	t.Run("missing final empty chunk", func(t *testing.T) {
		r2 := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/o",
			[][]byte{[]byte("abcdef")}, testNow)
		wire := mustReadAll(t, r2.Body)
		cut := strings.Index(wire, "0;chunk-signature")
		verifier := newChunkVerifier(
			strings.NewReader(wire[:cut]),
			amzDate, auth.credentialScope(), key, auth.signature, 6,
		)
		if _, err := verifier.verifyAll(); err == nil {
			t.Fatal("expected error when final empty chunk is missing")
		}
	})
}

func mustReadAll(t *testing.T, r interface{ Read([]byte) (int, error) }) string {
	t.Helper()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTimeValidation(t *testing.T) {
	amzDate := testNow.Format("20060102T150405Z")
	r, _ := http.NewRequest(http.MethodGet, "https://example.com/", nil)
	r.Header.Set(amzDateHeader, amzDate)

	if err := validateAmzDate(r, amzDate[:8], testNow, 15*time.Minute); err != nil {
		t.Fatalf("in-window request rejected: %v", err)
	}
	if err := validateAmzDate(r, amzDate[:8], testNow.Add(16*time.Minute), 15*time.Minute); err == nil {
		t.Fatal("skewed request accepted")
	}
	if err := validateAmzDate(r, "20150829", testNow, 15*time.Minute); err == nil {
		t.Fatal("scope date mismatch accepted")
	}
}
