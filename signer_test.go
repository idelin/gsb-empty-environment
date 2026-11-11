package sigv4chunk

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// This file implements an independent SigV4 signer used only by tests. It is
// written separately from the production verifier and anchored against the
// public AWS SigV4 "get-vanilla" golden test vector (access key AKIDEXAMPLE).

const (
	testAccessKey = "AKIDEXAMPLE"
	testSecret    = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
	testRegion    = "us-east-1"
	testService   = "service"
)

func testHMAC(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func testSigningKey(date, region, service string) []byte {
	kDate := testHMAC([]byte("AWS4"+testSecret), []byte(date))
	kRegion := testHMAC(kDate, []byte(region))
	kService := testHMAC(kRegion, []byte(service))
	return testHMAC(kService, []byte("aws4_request"))
}

func testHexSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// testEncode mirrors the strict component encoding required by SigV4.
func testEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func testCanonicalURI(path string) string {
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		segments[i] = testEncode(seg)
	}
	return strings.Join(segments, "/")
}

func testCanonicalQuery(raw string) string {
	if raw == "" {
		return ""
	}
	type pair struct{ k, v string }
	var pairs []pair
	for _, piece := range strings.Split(raw, "&") {
		k, v, found := strings.Cut(piece, "=")
		if !found {
			v = ""
		}
		pairs = append(pairs, pair{k: strictUnescape(k), v: strictUnescape(v)})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k != pairs[j].k {
			return pairs[i].k < pairs[j].k
		}
		return pairs[i].v < pairs[j].v
	})
	out := make([]string, len(pairs))
	for i, p := range pairs {
		out[i] = testEncode(p.k) + "=" + testEncode(p.v)
	}
	return strings.Join(out, "&")
}

// testSignRequest signs an arbitrary request with the given payload hash and
// signed header list, returning the Authorization header value.
func testSignRequest(r *http.Request, signedHeaders []string, payloadHash, amzDate string) string {
	scopeDate := amzDate[:8]
	scope := strings.Join([]string{scopeDate, testRegion, testService, "aws4_request"}, "/")

	headerLines := make([]string, 0, len(signedHeaders))
	for _, name := range signedHeaders {
		var value string
		if name == "host" {
			value = r.Host
		} else {
			value = r.Header.Get(http.CanonicalHeaderKey(name))
		}
		headerLines = append(headerLines, name+":"+strings.Join(strings.Fields(value), " "))
	}
	sort.Strings(headerLines)
	canonicalHeaders := strings.Join(headerLines, "\n") + "\n"

	canonical := strings.Join([]string{
		r.Method,
		testCanonicalURI(r.URL.Path),
		testCanonicalQuery(r.URL.RawQuery),
		canonicalHeaders,
		strings.Join(sortedCopy(signedHeaders), ";"),
		payloadHash,
	}, "\n")

	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		testHexSHA256([]byte(canonical)),
	}, "\n")
	signature := hex.EncodeToString(testHMAC(testSigningKey(scopeDate, testRegion, testService), []byte(stringToSign)))

	return "AWS4-HMAC-SHA256 " +
		"Credential=" + testAccessKey + "/" + scope + ", " +
		"SignedHeaders=" + strings.Join(sortedCopy(signedHeaders), ";") + ", " +
		"Signature=" + signature
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// testChunkSignature computes one aws-chunked block signature.
func testChunkSignature(key []byte, amzDate, scope string, priorSignature, data []byte) string {
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256-PAYLOAD",
		amzDate,
		scope,
		hex.EncodeToString(priorSignature),
		testHexSHA256(nil),
		testHexSHA256(data),
	}, "\n")
	return hex.EncodeToString(testHMAC(key, []byte(stringToSign)))
}

// buildChunk renders one aws-chunked wire block.
func buildChunk(data []byte, signature string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%x;chunk-signature=%s\r\n", len(data), signature)
	b.Write(data)
	b.WriteString("\r\n")
	return b.Bytes()
}

// signedStreamingRequest builds a complete, correctly signed aws-chunked
// request whose plaintext payload is the concatenation of chunks.
func signedStreamingRequest(method, target string, chunks [][]byte, amzTime time.Time) *http.Request {
	amzDate := amzTime.UTC().Format("20060102T150405Z")
	scopeDate := amzDate[:8]
	scope := strings.Join([]string{scopeDate, testRegion, testService, "aws4_request"}, "/")
	key := testSigningKey(scopeDate, testRegion, testService)

	var total int
	for _, chunk := range chunks {
		total += len(chunk)
	}

	r, err := http.NewRequest(method, target, nil)
	if err != nil {
		panic(err)
	}
	r.Host = r.URL.Host

	// Seed signature is produced by signing the streaming marker request.
	r.Header.Set(amzDateHeader, amzDate)
	r.Header.Set(amzContentSHA256, streamingPayload)
	r.Header.Set(amzDecodedLength, fmt.Sprintf("%d", total))
	signedHeaders := []string{"host", amzContentSHA256, amzDateHeader, amzDecodedLength}
	authHeader := testSignRequest(r, signedHeaders, streamingPayload, amzDate)
	r.Header.Set("Authorization", authHeader)

	auth, err := parseAuthorization(authHeader)
	if err != nil {
		panic(err)
	}

	var body bytes.Buffer
	prior := auth.signature
	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}
		sig := testChunkSignature(key, amzDate, scope, prior, chunk)
		sigBytes, _ := hex.DecodeString(sig)
		body.Write(buildChunk(chunk, sig))
		prior = sigBytes
	}
	body.Write(buildChunk(nil, testChunkSignature(key, amzDate, scope, prior, nil)))
	r.Body = io.NopCloser(bytes.NewReader(body.Bytes()))
	return r
}
