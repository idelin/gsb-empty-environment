package netsec

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"testing"
)

func newSHA256Verifier() *Verifier {
	return NewVerifier("Digest", func(canonical []byte) (string, error) {
		sum := sha256.Sum256(canonical)
		return "sha256=" + base64.StdEncoding.EncodeToString(sum[:]), nil
	})
}

func baseHeaders() []Field {
	return []Field{
		{Name: "Host", Value: "gateway.example"},
		{Name: "Content-Type", Value: "application/json"},
		{Name: "X-Request-Id", Value: "req-42"},
		{Name: "Trailer", Value: "X-Trace, X-Checksum"},
	}
}

func baseTrailers() []Field {
	return []Field{
		{Name: "X-Trace", Value: "trace-abc"},
		{Name: "X-Checksum", Value: "cksum-9"},
	}
}

func signedMessage(t *testing.T, v *Verifier) Message {
	t.Helper()
	digest, err := v.Sign(baseHeaders(), baseTrailers())
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}
	trailers := append(append([]Field(nil), baseTrailers()...), Field{Name: v.DigestField, Value: digest})
	return Message{Headers: baseHeaders(), Trailers: trailers}
}

func TestSignAndVerifyRoundTrip(t *testing.T) {
	v := newSHA256Verifier()
	msg := signedMessage(t, v)

	merged, err := v.Verify(msg)
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if merged == nil {
		t.Fatal("expected authenticated merged message")
	}
	if len(merged.Headers) != 4 {
		t.Fatalf("headers = %d, want 4", len(merged.Headers))
	}
	if len(merged.Trailers) != 2 {
		t.Fatalf("trailers = %d, want 2 (digest must be stripped)", len(merged.Trailers))
	}
	if len(merged.Fields) != 6 {
		t.Fatalf("merged fields = %d, want 6", len(merged.Fields))
	}
	if _, ok := merged.Get("Digest"); ok {
		t.Fatal("digest field must not be released to downstream")
	}
	if got, ok := merged.Get("x-trace"); !ok || got != "trace-abc" {
		t.Fatalf("Get(X-Trace) = %q, %v", got, ok)
	}
	if vals := merged.Values("X-Request-Id"); len(vals) != 1 || vals[0] != "req-42" {
		t.Fatalf("Values(X-Request-Id) = %v", vals)
	}
	httpHeader := merged.HTTPHeader()
	if httpHeader.Get("X-Checksum") != "cksum-9" {
		t.Fatalf("HTTPHeader missing trailer value: %v", httpHeader)
	}
}

func TestHeadTrailerNameCollisionRejected(t *testing.T) {
	v := newSHA256Verifier()
	msg := signedMessage(t, v)

	msg.Trailers[0] = Field{Name: "X-Request-Id", Value: "spoofed"}

	merged, err := v.Verify(msg)
	if !errors.Is(err, ErrTrailerConflict) {
		t.Fatalf("err = %v, want ErrTrailerConflict", err)
	}
	if merged != nil {
		t.Fatal("downstream must not receive a merged message on trailer/header collision")
	}

	if _, err := v.Sign(baseHeaders(), []Field{{Name: "X-Request-Id", Value: "spoofed"}}); !errors.Is(err, ErrTrailerConflict) {
		t.Fatalf("Sign err = %v, want ErrTrailerConflict", err)
	}
}

func TestUndeclaredTrailerRejected(t *testing.T) {
	v := newSHA256Verifier()
	msg := signedMessage(t, v)

	msg.Trailers = append(msg.Trailers, Field{Name: "X-Injected", Value: "surprise"})

	merged, err := v.Verify(msg)
	if !errors.Is(err, ErrUndeclaredTrailer) {
		t.Fatalf("err = %v, want ErrUndeclaredTrailer", err)
	}
	if merged != nil {
		t.Fatal("downstream must not receive a merged message with an undeclared trailer")
	}
}

func TestForbiddenTrailerFieldsRejected(t *testing.T) {
	v := newSHA256Verifier()
	forbidden := []Field{
		{Name: "Authorization", Value: "Bearer token"},
		{Name: "Proxy-Authorization", Value: "Basic x"},
		{Name: "Host", Value: "evil.example"},
		{Name: "Connection", Value: "keep-alive"},
		{Name: "Forwarded", Value: "for=1.2.3.4"},
		{Name: "Via", Value: "1.1 proxy"},
		{Name: "Content-Length", Value: "42"},
		{Name: "Transfer-Encoding", Value: "chunked"},
		{Name: "Te", Value: "trailers"},
	}
	for _, bad := range forbidden {
		t.Run(bad.Name, func(t *testing.T) {
			headers := append(append([]Field(nil), baseHeaders()...),
				Field{Name: "Trailer", Value: bad.Name})
			trailers := []Field{{Name: bad.Name, Value: bad.Value}}
			if _, err := v.Sign(headers, trailers); !errors.Is(err, ErrForbiddenTrailer) {
				t.Fatalf("Sign err = %v, want ErrForbiddenTrailer", err)
			}

			msg := signedMessage(t, v)
			msg.Trailers[0] = bad
			merged, err := v.Verify(msg)
			if !errors.Is(err, ErrForbiddenTrailer) {
				t.Fatalf("Verify err = %v, want ErrForbiddenTrailer", err)
			}
			if merged != nil {
				t.Fatal("downstream must not receive a merged message with a forbidden trailer")
			}
		})
	}
}

func TestTruncatedTrailersRejected(t *testing.T) {
	v := newSHA256Verifier()

	t.Run("declared trailer missing", func(t *testing.T) {
		msg := signedMessage(t, v)
		msg.Trailers = []Field{
			{Name: "X-Trace", Value: "trace-abc"},
			msg.Trailers[2],
		}
		merged, err := v.Verify(msg)
		if !errors.Is(err, ErrTruncated) {
			t.Fatalf("err = %v, want ErrTruncated", err)
		}
		if merged != nil {
			t.Fatal("downstream must not receive a merged message when the frame is truncated")
		}
	})

	t.Run("digest trailer missing", func(t *testing.T) {
		msg := signedMessage(t, v)
		msg.Trailers = msg.Trailers[:2]
		merged, err := v.Verify(msg)
		if !errors.Is(err, ErrTruncated) {
			t.Fatalf("err = %v, want ErrTruncated", err)
		}
		if merged != nil {
			t.Fatal("downstream must not receive a merged message without the digest")
		}
	})
}

func TestTamperedContentFailsDigest(t *testing.T) {
	v := newSHA256Verifier()

	t.Run("trailer value tampered", func(t *testing.T) {
		msg := signedMessage(t, v)
		msg.Trailers[1].Value = "tampered"
		merged, err := v.Verify(msg)
		if !errors.Is(err, ErrDigestMismatch) {
			t.Fatalf("err = %v, want ErrDigestMismatch", err)
		}
		if merged != nil {
			t.Fatal("downstream must not receive an unauthenticated merged message")
		}
	})

	t.Run("header value tampered", func(t *testing.T) {
		msg := signedMessage(t, v)
		msg.Headers[2].Value = "req-999"
		merged, err := v.Verify(msg)
		if !errors.Is(err, ErrDigestMismatch) {
			t.Fatalf("err = %v, want ErrDigestMismatch", err)
		}
		if merged != nil {
			t.Fatal("downstream must not receive an unauthenticated merged message")
		}
	})
}

func TestDigestBindsOrderAndDuplicateInstances(t *testing.T) {
	v := newSHA256Verifier()
	headers := []Field{
		{Name: "Host", Value: "gateway.example"},
		{Name: "Trailer", Value: "X-Note"},
	}
	trailers := []Field{
		{Name: "X-Note", Value: "one"},
		{Name: "X-Note", Value: "two"},
	}
	digest, err := v.Sign(headers, trailers)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	valid := Message{Headers: headers, Trailers: append(append([]Field(nil), trailers...),
		Field{Name: "Digest", Value: digest})}
	if _, err := v.Verify(valid); err != nil {
		t.Fatalf("original ordering must verify: %v", err)
	}

	reordered := Message{
		Headers: headers,
		Trailers: []Field{
			{Name: "X-Note", Value: "two"},
			{Name: "X-Note", Value: "one"},
			{Name: "Digest", Value: digest},
		},
	}
	merged, err := v.Verify(reordered)
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("reordering duplicates: err = %v, want ErrDigestMismatch", err)
	}
	if merged != nil {
		t.Fatal("reordered duplicate trailers must not be released without authentication")
	}

	dropped := Message{
		Headers: headers,
		Trailers: []Field{
			{Name: "X-Note", Value: "one"},
			{Name: "Digest", Value: digest},
		},
	}
	if _, err := v.Verify(dropped); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("dropped duplicate instance: err = %v, want ErrDigestMismatch", err)
	}
}

func TestMalformedFieldsRejected(t *testing.T) {
	v := newSHA256Verifier()
	cases := []struct {
		name     string
		headers  []Field
		trailers []Field
	}{
		{
			name:     "pseudo header in trailer",
			headers:  baseHeaders(),
			trailers: []Field{{Name: ":path", Value: "/admin"}},
		},
		{
			name:     "CRLF injection in value",
			headers:  baseHeaders(),
			trailers: []Field{{Name: "X-Trace", Value: "x\r\nX-Injected: y"}},
		},
		{
			name:     "invalid trailer declaration",
			headers:  []Field{{Name: "Trailer", Value: "bad name"}},
			trailers: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := v.Verify(Message{Headers: tc.headers, Trailers: tc.trailers}); !errors.Is(err, ErrMalformedField) {
				t.Fatalf("err = %v, want ErrMalformedField", err)
			}
		})
	}
}

func TestMissingCallbackRejected(t *testing.T) {
	v := &Verifier{DigestField: "Digest"}
	if _, err := v.Verify(Message{Headers: baseHeaders()}); !errors.Is(err, ErrMissingCallback) {
		t.Fatalf("err = %v, want ErrMissingCallback", err)
	}
}

func TestRepeatedDigestRejected(t *testing.T) {
	v := newSHA256Verifier()
	msg := signedMessage(t, v)
	msg.Trailers = append(msg.Trailers, Field{Name: "Digest", Value: "sha256=AAAA"})
	merged, err := v.Verify(msg)
	if !errors.Is(err, ErrMalformedField) {
		t.Fatalf("err = %v, want ErrMalformedField", err)
	}
	if merged != nil {
		t.Fatal("repeated digest trailers must never reach downstream")
	}
}

func TestCanonicalInputIsSectionScopedAndDeterministic(t *testing.T) {
	headers := []Field{{Name: "A", Value: "1"}}
	trailers := []Field{{Name: "B", Value: "2"}, {Name: "B", Value: "3"}}
	got := string(canonicalInput(headers, trailers))
	want := "headers:1\r\nA: 1\r\ntrailers:2\r\nB: 2\r\nB: 3\r\n"
	if got != want {
		t.Fatalf("canonical input = %q, want %q", got, want)
	}
	if string(canonicalInput(headers, trailers)) != got {
		t.Fatal("canonical input must be deterministic")
	}
}

func ExampleVerifier() {
	v := NewVerifier("Digest", func(canonical []byte) (string, error) {
		sum := sha256.Sum256(canonical)
		return "sha256=" + base64.StdEncoding.EncodeToString(sum[:]), nil
	})
	headers := []Field{{Name: "Trailer", Value: "X-Trace"}}
	trailers := []Field{{Name: "X-Trace", Value: "trace-1"}}
	digest, err := v.Sign(headers, trailers)
	if err != nil {
		fmt.Println("sign error:", err)
		return
	}
	msg := Message{
		Headers:  headers,
		Trailers: append(trailers, Field{Name: "Digest", Value: digest}),
	}
	merged, err := v.Verify(msg)
	if err != nil {
		fmt.Println("verify error:", err)
		return
	}
	trace, _ := merged.Get("X-Trace")
	fmt.Println(trace)
	// Output: trace-1
}
