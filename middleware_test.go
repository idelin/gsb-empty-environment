package sigv4chunk

import (
	"bytes"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Fixed clock used by all middleware tests so signatures stay reproducible.
var testNow = time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)

func testConfig() Config {
	return Config{
		Keys: func(accessKey string) (string, bool, error) {
			if accessKey == testAccessKey {
				return testSecret, true, nil
			}
			return "", false, nil
		},
		Now:              func() time.Time { return testNow },
		AllowedRegion:    testRegion,
		AllowedService:   testService,
		MaxDecodedLength: 1 << 20,
	}
}

// recordingHandler captures what the downstream handler observes.
type recordingHandler struct {
	called bool
	body   []byte
	length int64
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.called = true
	h.length = r.ContentLength
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.body = data
	w.WriteHeader(http.StatusOK)
}

func runMiddleware(t *testing.T, cfg Config, r *http.Request) (*httptest.ResponseRecorder, *recordingHandler) {
	t.Helper()
	handler := &recordingHandler{}
	server := Middleware(cfg, handler)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, r)
	return rec, handler
}

// TestAWSGoldenGetVanilla anchors the canonical-request construction and
// seed verification against the public AWS SigV4 test suite vector
// "get-vanilla" (IAM ListUsers, 2015-08-30, AKIDEXAMPLE).
func TestAWSGoldenGetVanilla(t *testing.T) {
	amzDate := "20150830T123600Z"
	r, err := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/?Param2=value2&Param1=value1", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Host = "example.amazonaws.com"
	r.Header.Set(amzDateHeader, amzDate)

	signedHeaders := []string{"host", amzDateHeader}
	authHeader := testSignRequest(r, signedHeaders, testHexSHA256(nil), amzDate)
	r.Header.Set("Authorization", authHeader)

	auth, err := parseAuthorization(authHeader)
	if err != nil {
		t.Fatalf("parseAuthorization: %v", err)
	}

	// Published signature for the public IAM ListUsers "get-vanilla" vector.
	const publishedSignature = "b97d918cfa904a5beff61c982a1b6f458b799221646efd99d3219ec94cdf2500"
	if hex.EncodeToString(auth.signature) != publishedSignature {
		t.Fatalf("seed signature does not match published golden value: got %x", auth.signature)
	}

	// Verify the canonical request byte-for-byte against the published form.
	gotCanonical := buildCanonicalRequest(r, auth.signedHeaders, testHexSHA256(nil))
	wantCanonical := strings.Join([]string{
		"GET",
		"/",
		"Param1=value1&Param2=value2",
		"host:example.amazonaws.com",
		"x-amz-date:20150830T123600Z",
		"",
		"host;x-amz-date",
		testHexSHA256(nil),
	}, "\n")
	if gotCanonical != wantCanonical {
		t.Fatalf("canonical request mismatch:\nwant:\n%s\n\ngot:\n%s", wantCanonical, gotCanonical)
	}

	if err := verifySeedSignature(r, auth, testSecret, testHexSHA256(nil)); err != nil {
		t.Fatalf("golden seed signature rejected: %v", err)
	}

	// The same request must be rejected when a query value is tampered with.
	r.URL.RawQuery = "Param1=value1&Param2=tampered"
	if err := verifySeedSignature(r, auth, testSecret, testHexSHA256(nil)); err == nil {
		t.Fatal("tampered query accepted")
	}
}

func TestMiddlewareHappyPath(t *testing.T) {
	chunks := [][]byte{
		[]byte("hello object storage "),
		[]byte("second chunk with more data"),
		nil,
		bytes.Repeat([]byte("Z"), 4096),
	}
	r := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/object/key?uploadId=abc&partNumber=2", chunks, testNow)

	rec, handler := runMiddleware(t, testConfig(), r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if !handler.called {
		t.Fatal("downstream handler not called")
	}
	wantBody := bytes.Join(chunks, nil)
	if !bytes.Equal(handler.body, wantBody) {
		t.Fatalf("decoded body mismatch: got %d bytes, want %d", len(handler.body), len(wantBody))
	}
	if handler.length != int64(len(wantBody)) {
		t.Fatalf("ContentLength = %d, want %d", handler.length, len(wantBody))
	}
	if r.Header.Get(amzContentSHA256) != "" || r.Header.Get(amzDecodedLength) != "" {
		t.Fatal("streaming marker headers must be stripped before forwarding")
	}
}

func TestMiddlewareSingleChunkAndEmptyObject(t *testing.T) {
	cases := [][][]byte{
		{[]byte("only one chunk")},
		{nil}, // zero-byte object: only the final empty chunk
	}
	for i, chunks := range cases {
		r := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/empty", chunks, testNow)
		rec, handler := runMiddleware(t, testConfig(), r)
		if rec.Code != http.StatusOK {
			t.Fatalf("case %d: status = %d, body = %q", i, rec.Code, rec.Body.String())
		}
		var want []byte
		for _, c := range chunks {
			want = append(want, c...)
		}
		if !bytes.Equal(handler.body, want) {
			t.Fatalf("case %d: body mismatch", i)
		}
	}
}

// Each attack below must produce a non-2xx response and must never invoke the
// downstream handler, so no unverified data can be observed.
func TestMiddlewareRejectsBadRequests(t *testing.T) {
	goodChunks := [][]byte{[]byte("chunk-one-data!!"), []byte("chunk-two-data!!")}

	t.Run("tampered chunk data", func(t *testing.T) {
		r := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/o", goodChunks, testNow)
		wire, _ := io.ReadAll(r.Body)
		// Flip a byte inside the first chunk payload (after its CRLF).
		firstLF := bytes.Index(wire, []byte("\r\n"))
		wire[firstLF+2] ^= 0x01
		r.Body = io.NopCloser(bytes.NewReader(wire))
		assertRejected(t, r)
	})

	t.Run("truncated before final empty chunk", func(t *testing.T) {
		r := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/o", goodChunks, testNow)
		wire, _ := io.ReadAll(r.Body)
		cut := bytes.Index(wire, []byte("0;chunk-signature"))
		if cut < 0 {
			t.Fatal("final chunk marker not found")
		}
		r.Body = io.NopCloser(bytes.NewReader(wire[:cut]))
		assertRejected(t, r)
	})

	t.Run("truncated mid chunk", func(t *testing.T) {
		r := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/o", goodChunks, testNow)
		wire, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(wire[:len(wire)/2]))
		assertRejected(t, r)
	})

	t.Run("reordered chunks", func(t *testing.T) {
		r := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/o", goodChunks, testNow)
		wire, _ := io.ReadAll(r.Body)
		// Swap payload bytes between the two blocks but keep each block's
		// original header and signature, breaking the chained verification.
		firstEnd := blockEnd(wire, 0, len(goodChunks[0]))
		secondEnd := blockEnd(wire, firstEnd, len(goodChunks[1]))
		firstBlock := append([]byte(nil), wire[:firstEnd]...)
		secondBlock := append([]byte(nil), wire[firstEnd:secondEnd]...)
		finalBlock := wire[secondEnd:]

		var rebuilt bytes.Buffer
		rebuilt.Write(replaceBlockPayload(firstBlock, goodChunks[1]))
		rebuilt.Write(replaceBlockPayload(secondBlock, goodChunks[0]))
		rebuilt.Write(finalBlock)
		r.Body = io.NopCloser(bytes.NewReader(rebuilt.Bytes()))
		assertRejected(t, r)
	})

	t.Run("declared length smaller than actual", func(t *testing.T) {
		r := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/o", goodChunks, testNow)
		r.Header.Set(amzDecodedLength, "1")
		assertRejected(t, r)
	})

	t.Run("declared length larger than actual", func(t *testing.T) {
		r := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/o", goodChunks, testNow)
		r.Header.Set(amzDecodedLength, "999999")
		assertRejected(t, r)
	})

	t.Run("forged chunk signature", func(t *testing.T) {
		r := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/o", goodChunks, testNow)
		wire, _ := io.ReadAll(r.Body)
		idx := bytes.Index(wire, []byte("chunk-signature="))
		hexStart := idx + len("chunk-signature=")
		wire[hexStart] = swapHexDigit(wire[hexStart])
		r.Body = io.NopCloser(bytes.NewReader(wire))
		assertRejected(t, r)
	})

	t.Run("forged seed signature", func(t *testing.T) {
		r := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/o", goodChunks, testNow)
		auth := r.Header.Get("Authorization")
		idx := strings.Index(auth, "Signature=") + len("Signature=")
		forged := []byte(auth)
		forged[idx] = swapHexDigit(forged[idx])
		r.Header.Set("Authorization", string(forged))
		assertRejected(t, r)
	})

	t.Run("trailing garbage after final chunk", func(t *testing.T) {
		r := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/o", goodChunks, testNow)
		wire, _ := io.ReadAll(r.Body)
		wire = append(wire, []byte("extra-bytes")...)
		r.Body = io.NopCloser(bytes.NewReader(wire))
		assertRejected(t, r)
	})

	t.Run("expired request", func(t *testing.T) {
		old := testNow.Add(-time.Hour)
		r := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/o", goodChunks, old)
		assertRejected(t, r)
	})

	t.Run("unknown access key", func(t *testing.T) {
		r := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/o", goodChunks, testNow)
		cfg := testConfig()
		cfg.Keys = func(string) (string, bool, error) { return "", false, nil }
		rec, handler := runMiddleware(t, cfg, r)
		if rec.Code == http.StatusOK || handler.called {
			t.Fatalf("unknown key accepted: code=%d handler=%v", rec.Code, handler.called)
		}
	})

	t.Run("not a streaming request", func(t *testing.T) {
		r := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/o", goodChunks, testNow)
		r.Header.Set(amzContentSHA256, testHexSHA256([]byte("whatever")))
		assertRejected(t, r)
	})

	t.Run("missing mandatory signed header", func(t *testing.T) {
		r := signedStreamingRequest(http.MethodPut, "https://bucket.s3.example.com/o", goodChunks, testNow)
		auth := r.Header.Get("Authorization")
		auth = strings.ReplaceAll(auth, amzDecodedLength+";", "")
		// Also strip the request header so the omission is real, not merely
		// an unsigned extra header.
		r.Header.Del(amzDecodedLength)
		r.Header.Set("Authorization", auth)
		assertRejected(t, r)
	})
}

func assertRejected(t *testing.T, r *http.Request) {
	t.Helper()
	rec, handler := runMiddleware(t, testConfig(), r)
	if rec.Code == http.StatusOK {
		t.Fatalf("request accepted: %s", rec.Body.String())
	}
	if handler.called {
		t.Fatalf("handler invoked with unverified data, body=%q", handler.body)
	}
}

// blockEnd returns the offset just past the block starting at start, whose
// payload is payloadSize bytes (header + CRLF + payload + CRLF).
func blockEnd(wire []byte, start, payloadSize int) int {
	rel := bytes.Index(wire[start:], []byte("\r\n")) + 2
	return start + rel + payloadSize + 2
}

// replaceBlockPayload swaps the payload of a signed block while keeping its
// original size field and signature header, producing a block whose stored
// signature no longer matches.
func replaceBlockPayload(block, newPayload []byte) []byte {
	headerEnd := bytes.Index(block, []byte("\r\n")) + 2
	header := string(block[:headerEnd])
	prefixEnd := strings.IndexByte(header, ';')
	var rebuilt bytes.Buffer
	rebuilt.WriteString(header[prefixEnd:])
	rebuilt.Write(newPayload)
	rebuilt.WriteString("\r\n")
	return rebuilt.Bytes()
}

func swapHexDigit(b byte) byte {
	switch {
	case b >= '0' && b < '9':
		return b + 1
	case b == '9':
		return '0'
	case b >= 'a' && b < 'f':
		return b + 1
	case b == 'f':
		return 'a'
	default:
		return b
	}
}
