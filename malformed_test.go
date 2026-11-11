package sigv4chunk

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func validAuthHeader() string {
	return "AWS4-HMAC-SHA256 " +
		"Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
		"SignedHeaders=host;x-amz-content-sha256;x-amz-date;x-amz-decoded-content-length, " +
		"Signature=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
}

func TestParseAuthorizationMalformed(t *testing.T) {
	cases := map[string]string{
		"empty":                   "",
		"no space":                "AWS4-HMAC-SHA256",
		"wrong algorithm":         "AWS4-HMAC-MD5 Credential=x, SignedHeaders=host, Signature=" + strings.Repeat("0", 64),
		"missing credential":      "AWS4-HMAC-SHA256 SignedHeaders=host, Signature=" + strings.Repeat("0", 64),
		"missing signed headers":  "AWS4-HMAC-SHA256 Credential=ak/20150830/r/s/aws4_request, Signature=" + strings.Repeat("0", 64),
		"bad signature length":    "AWS4-HMAC-SHA256 Credential=ak/20150830/r/s/aws4_request, SignedHeaders=host, Signature=abcd",
		"non-hex signature":       "AWS4-HMAC-SHA256 Credential=ak/20150830/r/s/aws4_request, SignedHeaders=host, Signature=" + strings.Repeat("z", 64),
		"bad scope length":        "AWS4-HMAC-SHA256 Credential=ak/20150830/r/s, SignedHeaders=host, Signature=" + strings.Repeat("0", 64),
		"bad terminator":          "AWS4-HMAC-SHA256 Credential=ak/20150830/r/s/aws4_bad, SignedHeaders=host, Signature=" + strings.Repeat("0", 64),
		"duplicate signed header": "AWS4-HMAC-SHA256 Credential=ak/20150830/r/s/aws4_request, SignedHeaders=host;host, Signature=" + strings.Repeat("0", 64),
		"field without equals":    "AWS4-HMAC-SHA256 Garbage, Credential=ak/20150830/r/s/aws4_request, SignedHeaders=host, Signature=" + strings.Repeat("0", 64),
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseAuthorization(header); err == nil {
				t.Fatalf("malformed header accepted: %s", name)
			}
		})
	}

	auth, err := parseAuthorization(validAuthHeader())
	if err != nil {
		t.Fatalf("valid header rejected: %v", err)
	}
	if auth.accessKey != "AKIDEXAMPLE" || auth.region != "us-east-1" || auth.service != "service" {
		t.Fatalf("unexpected parsed fields: %+v", auth)
	}
}

func TestMalformedChunks(t *testing.T) {
	auth, err := parseAuthorization(validAuthHeader())
	if err != nil {
		t.Fatal(err)
	}
	key := signingKey(testSecret, auth.date, auth.region, auth.service)

	cases := map[string]string{
		"non-hex size":            "zz;chunk-signature=" + strings.Repeat("0", 64) + "\r\n\r\n",
		"missing signature param": "10\r\n",
		"bad signature hex":       "10;chunk-signature=zzzz\r\n" + strings.Repeat("a", 16) + "\r\n",
		"bad signature length":    "10;chunk-signature=abcd\r\n" + strings.Repeat("a", 16) + "\r\n",
		"param without equals":    "10;badparam\r\n" + strings.Repeat("a", 16) + "\r\n",
		"crlf missing after data": "10;chunk-signature=" + strings.Repeat("0", 64) + "\r\n" + strings.Repeat("a", 16) + "xx",
		"header line too long":    strings.Repeat("a", maxChunkHeaderSize+1),
		"empty body":              "",
	}
	for name, wire := range cases {
		t.Run(name, func(t *testing.T) {
			verifier := newChunkVerifier(
				strings.NewReader(wire),
				"20150830T123600Z", auth.credentialScope(), key, auth.signature, -1,
			)
			if _, err := verifier.verifyAll(); err == nil {
				t.Fatalf("malformed wire accepted: %s", name)
			}
		})
	}
}

func TestMalformedAmzDate(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "https://example.com/", nil)
	r.Header.Set(amzDateHeader, "not-a-date")
	if err := validateAmzDate(r, "20150830", testNow, 0); err == nil {
		t.Fatal("bad date format accepted")
	}

	r.Header.Set(amzDateHeader, testNow.Format("20060102T150405Z"))
	if err := validateAmzDate(r, "20150830", testNow.Add(time.Second), 0); err == nil {
		t.Fatal("zero skew window accepted a one-second-old request")
	}

	r.Header.Del(amzDateHeader)
	if err := validateAmzDate(r, "20150830", testNow, 0); err == nil {
		t.Fatal("missing date accepted")
	}
}

func TestEnsureSignedHeaderValidation(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "https://example.com/", nil)
	r.Host = "example.com"
	r.Header.Set(amzDateHeader, "20150830T123600Z")

	// A signed header that the request does not actually carry.
	if err := ensureSignedHeaders(r, []string{"host", "x-missing"}, nil); err == nil {
		t.Fatal("absent signed header accepted")
	}
	// Mandatory header omitted from the signed set.
	if err := ensureSignedHeaders(r, []string{"host"}, map[string]bool{amzDateHeader: true}); err == nil {
		t.Fatal("missing mandatory signed header accepted")
	}
	// Duplicate entries.
	if err := ensureSignedHeaders(r, []string{"host", "host"}, nil); err == nil {
		t.Fatal("duplicate signed header accepted")
	}
}

func TestMiddlewareBadDeclaredLength(t *testing.T) {
	r := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/o",
		[][]byte{[]byte("abc")}, testNow)
	r.Header.Set(amzDecodedLength, "not-a-number")
	assertRejected(t, r)

	r = signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/o",
		[][]byte{[]byte("abc")}, testNow)
	cfg := testConfig()
	cfg.MaxDecodedLength = 2
	rec, handler := runMiddleware(t, cfg, r)
	if rec.Code != http.StatusRequestEntityTooLarge || handler.called {
		t.Fatalf("size limit not enforced: code=%d handler=%v", rec.Code, handler.called)
	}
}

func TestKeyProviderError(t *testing.T) {
	r := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/o",
		[][]byte{[]byte("abc")}, testNow)
	cfg := testConfig()
	cfg.Keys = func(string) (string, bool, error) {
		return "", false, io.ErrClosedPipe
	}
	rec, handler := runMiddleware(t, cfg, r)
	if rec.Code != http.StatusInternalServerError || handler.called {
		t.Fatalf("key provider error mapped wrong: code=%d handler=%v", rec.Code, handler.called)
	}
}

func TestNilKeysPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Middleware with nil Keys must panic at construction")
		}
	}()
	Middleware(Config{}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
}
