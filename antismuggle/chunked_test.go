package antismuggle

import (
	"errors"
	"io"
	"testing"
)

func bodyHardError(t *testing.T, raw string, want Reason) {
	t.Helper()
	conn := newTestConn(t, raw)
	req, err := conn.Next()
	if err != nil {
		t.Fatalf("header frame should parse: %v", err)
	}
	_, err = io.ReadAll(req.Body)
	var he *Error
	if !errors.As(err, &he) {
		t.Fatalf("expected *Error %q, got %T: %v", want, err, err)
	}
	if he.Reason != want {
		t.Fatalf("expected reason %q, got %q (%v)", want, he.Reason, he)
	}
	if !he.CloseConnection {
		t.Fatalf("reason %q must force close", want)
	}
}

func TestChunkedHappyPath(t *testing.T) {
	raw := "POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding:chunked\r\n\r\n" +
		"5\r\nhello\r\n6\r\n world\r\n0\r\n\r\n"
	conn := newTestConn(t, raw)
	req, err := conn.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := drain(t, req); got != "hello world" {
		t.Fatalf("decoded body = %q", got)
	}
	if _, err := conn.Next(); err != io.EOF {
		t.Fatalf("expected clean EOF at boundary, got %v", err)
	}
}

func TestChunkedPipelineSecondFrame(t *testing.T) {
	raw := "POST /a HTTP/1.1\r\nHost: a\r\nTransfer-Encoding:chunked\r\n\r\n" +
		"4\r\nping\r\n0\r\n\r\n" +
		"GET /b HTTP/1.1\r\nHost: a\r\n\r\n"
	conn := newTestConn(t, raw)
	first, err := conn.Next()
	if err != nil {
		t.Fatalf("first frame: %v", err)
	}
	if got := drain(t, first); got != "ping" {
		t.Fatalf("first body = %q", got)
	}
	second, err := conn.Next()
	if err != nil {
		t.Fatalf("pipelined second frame: %v", err)
	}
	if second.Target != "/b" || second.Transfer != BodyNone {
		t.Fatalf("second frame mis-parsed: %#v", second)
	}
}

func TestMalformedChunkSizeRejected(t *testing.T) {
	const head = "POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding:chunked\r\n\r\n"
	cases := map[string]string{
		"negative size":    head + "-5\r\nhello\r\n0\r\n\r\n",
		"hex leading junk": head + "x5\r\nhello\r\n0\r\n\r\n",
		"leading plus":     head + "+5\r\nhello\r\n0\r\n\r\n",
		"underscore":       head + "5_\r\nhello\r\n0\r\n\r\n",
		"space in size":    head + "5 \r\nhello\r\n0\r\n\r\n",
		"empty size":       head + "\r\nhello\r\n0\r\n\r\n",
		"decimal garbage":  head + "5g\r\nhello\r\n0\r\n\r\n",
		"size too big":     head + "999999999999999999999999\r\nx\r\n0\r\n\r\n",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			want := ReasonBadChunkSize
			if name == "size too big" {
				want = ReasonChunkTooLarge
			}
			bodyHardError(t, raw, want)
		})
	}

	// Chunk larger than the configured cap is rejected up front.
	cfg := DefaultConfig()
	cfg.MaxChunkSize = 4
	conn := newTestConn(t, head+"10\r\nabcdefghijklmnop\r\n0\r\n\r\n", cfg)
	req, err := conn.Next()
	if err != nil {
		t.Fatalf("frame parse: %v", err)
	}
	_, err = io.ReadAll(req.Body)
	var he *Error
	if !errors.As(err, &he) || he.Reason != ReasonChunkTooLarge {
		t.Fatalf("expected chunk_too_large, got %v", err)
	}
}

func TestMissingCRLFAfterChunkRejected(t *testing.T) {
	const head = "POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding:chunked\r\n\r\n"
	bodyHardError(t, head+"5\r\nhelloX6\r\n world\r\n0\r\n\r\n", ReasonBadChunkTerminator)
	bodyHardError(t, head+"5\r\nhello\n6\r\n world\r\n0\r\n\r\n", ReasonBadChunkTerminator)
	bodyHardError(t, head+"5\r\nhello\r6\r\n world\r\n0\r\n\r\n", ReasonBadChunkTerminator)
	bodyHardError(t, head+"5\r\nhel", ReasonTruncated)
}

func TestChunkExtensionPolicy(t *testing.T) {
	const head = "POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding:chunked\r\n\r\n"

	// A short, clean extension is tolerated and ignored.
	good := head + "5;name=value\r\nhello\r\n0\r\n\r\n"
	conn := newTestConn(t, good)
	req, err := conn.Next()
	if err != nil {
		t.Fatalf("valid extension rejected: %v", err)
	}
	if got := drain(t, req); got != "hello" {
		t.Fatalf("body = %q", got)
	}

	// Extension carrying a control character is hard rejected.
	bodyHardError(t, head+"5;x=\tv\r\nhello\r\n0\r\n\r\n", ReasonBadChunkExtension)

	// Overlong extension is hard rejected before its data is read.
	cfg := DefaultConfig()
	cfg.MaxChunkExtension = 4
	conn = newTestConn(t, head+"5;abcdefgh\r\nhello\r\n0\r\n\r\n", cfg)
	req, err = conn.Next()
	if err != nil {
		t.Fatalf("frame parse: %v", err)
	}
	_, err = io.ReadAll(req.Body)
	var he *Error
	if !errors.As(err, &he) || he.Reason != ReasonBadChunkExtension {
		t.Fatalf("expected bad_chunk_extension, got %v", err)
	}
}

func TestBodySizeLimit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MaxBody = 4

	conn := newTestConn(t,
		"POST / HTTP/1.1\r\nHost: a\r\nContent-Length:5\r\n\r\nhello", cfg)
	req, err := conn.Next()
	if err != nil {
		t.Fatalf("frame parse: %v", err)
	}
	_, err = io.ReadAll(req.Body)
	var he *Error
	if !errors.As(err, &he) || he.Reason != ReasonBodyTooLarge {
		t.Fatalf("identity: expected body_too_large, got %v", err)
	}

	conn = newTestConn(t,
		"POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding:chunked\r\n\r\n3\r\nabc\r\n3\r\ndef\r\n0\r\n\r\n", cfg)
	req, err = conn.Next()
	if err != nil {
		t.Fatalf("frame parse: %v", err)
	}
	_, err = io.ReadAll(req.Body)
	if !errors.As(err, &he) || he.Reason != ReasonBodyTooLarge {
		t.Fatalf("chunked: expected body_too_large, got %v", err)
	}
}

func TestTrailerInjectionRejected(t *testing.T) {
	const head = "POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding:chunked\r\n\r\n"

	// Default config: trailers are entirely disabled.
	bodyHardError(t, head+"5\r\nhello\r\n0\r\nX-Trace: ok\r\n\r\n", ReasonTrailerForbidden)

	// Forbidden hop/security headers are blocked even if allow-listed.
	for _, bad := range []string{"Host", "Content-Length", "Authorization",
		"Transfer-Encoding", "Connection", "Trailer"} {
		cfg := DefaultConfig()
		cfg.AllowedTrailers = []string{"host", "content-length", "authorization",
			"transfer-encoding", "connection", "trailer"}
		conn := newTestConn(t, head+"0\r\n"+bad+": evil\r\n\r\n", cfg)
		req, err := conn.Next()
		if err != nil {
			t.Fatalf("frame parse: %v", err)
		}
		_, err = io.ReadAll(req.Body)
		var he *Error
		if !errors.As(err, &he) || he.Reason != ReasonTrailerForbidden {
			t.Fatalf("trailer %q: expected trailer_forbidden, got %v", bad, err)
		}
	}

	// Announcing a forbidden trailer already fails while parsing headers.
	cfg := DefaultConfig()
	cfg.AllowedTrailers = []string{"x-trace"}
	raw := "POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding:chunked\r\nTrailer: Host\r\n\r\n0\r\n\r\n"
	nextHardError(t, newTestConn(t, raw, cfg), ReasonTrailerAnnouncement)

	// An allow-listed trailer is accepted and the frame boundary stays exact.
	cfg = DefaultConfig()
	cfg.AllowedTrailers = []string{"x-trace"}
	good := head + "5\r\nhello\r\n0\r\nX-Trace: abc\r\n\r\nGET /next HTTP/1.1\r\nHost: a\r\n\r\n"
	conn := newTestConn(t, good, cfg)
	req, err := conn.Next()
	if err != nil {
		t.Fatalf("allow-listed trailer rejected: %v", err)
	}
	if got := drain(t, req); got != "hello" {
		t.Fatalf("body = %q", got)
	}
	next, err := conn.Next()
	if err != nil {
		t.Fatalf("next frame after trailers: %v", err)
	}
	if next.Target != "/next" {
		t.Fatalf("next target = %q", next.Target)
	}

	// A non-allow-listed trailer still fails.
	bodyHardError(t, head+"0\r\nX-Other: z\r\n\r\n", ReasonTrailerForbidden)
}

func TestIdentityBodyBoundaryAndTruncation(t *testing.T) {
	conn := newTestConn(t,
		"POST / HTTP/1.1\r\nHost: a\r\nContent-Length:5\r\n\r\nhelloGET /x HTTP/1.1\r\nHost: a\r\n\r\n")
	req, err := conn.Next()
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	if got := drain(t, req); got != "hello" {
		t.Fatalf("body = %q", got)
	}
	next, err := conn.Next()
	if err != nil {
		t.Fatalf("pipelined frame: %v", err)
	}
	if next.Target != "/x" {
		t.Fatalf("target = %q", next.Target)
	}

	// Truncated declared body poisons the stream.
	bodyHardError(t,
		"POST / HTTP/1.1\r\nHost: a\r\nContent-Length:9\r\n\r\nshort",
		ReasonTruncated)
}

func TestNormalizeReFramesChunked(t *testing.T) {
	raw := "POST /submit HTTP/1.1\r\nHost: a\r\nTransfer-Encoding:chunked\r\nTrailer: x-trace\r\n\r\n" +
		"4\r\nping\r\n0\r\nX-Trace: 1\r\n\r\n"
	cfg := DefaultConfig()
	cfg.AllowedTrailers = []string{"x-trace"}
	conn := newTestConn(t, raw, cfg)
	req, err := conn.Next()
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	norm, err := Normalize(req)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	out := string(norm.Bytes())
	if !contains(out, "Content-Length: 4\r\n") {
		t.Fatalf("normalized frame missing single CL: %q", out)
	}
	if contains(out, "Transfer-Encoding") || contains(out, "Trailer") {
		t.Fatalf("normalized frame retained framing headers: %q", out)
	}
	if !hasSuffix(out, "\r\n\r\nping") {
		t.Fatalf("normalized frame body wrong: %q", out)
	}

	// Identity request normalization preserves the exact body and resets CL.
	conn = newTestConn(t,
		"POST /i HTTP/1.1\r\nHost: a\r\nContent-Length:3\r\n\r\nabc")
	req, err = conn.Next()
	if err != nil {
		t.Fatalf("identity frame: %v", err)
	}
	norm, err = Normalize(req)
	if err != nil {
		t.Fatalf("normalize identity: %v", err)
	}
	if string(norm.Body) != "abc" {
		t.Fatalf("identity body = %q", norm.Body)
	}
}

func TestNormalizeChunkedPassthrough(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DecodeChunkedToBody = false
	raw := "POST /p HTTP/1.1\r\nHost: a\r\nTransfer-Encoding:chunked\r\n\r\n4\r\nping\r\n0\r\n\r\n"
	conn := newTestConn(t, raw, cfg)
	req, err := conn.Next()
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	norm, err := Normalize(req)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if !norm.ChunkedPassthrough {
		t.Fatalf("expected passthrough mode")
	}
	if string(norm.Body) != "4\r\nping\r\n0\r\n\r\n" {
		t.Fatalf("raw chunked frame = %q", norm.Body)
	}
	if _, ok := norm.FieldByName("Transfer-Encoding"); !ok {
		t.Fatalf("passthrough must retain Transfer-Encoding")
	}
	for _, f := range norm.Fields {
		if equalFoldASCII(f.Name, "Content-Length") {
			t.Fatalf("passthrough must not synthesize Content-Length")
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func hasSuffix(s, suf string) bool {
	return len(s) >= len(suf) && s[len(s)-len(suf):] == suf
}

func TestKeepAliveSemantics(t *testing.T) {
	cases := map[string]bool{
		"GET / HTTP/1.1\r\nHost: a\r\n\r\n":                           true,
		"GET / HTTP/1.1\r\nHost: a\r\nConnection: close\r\n\r\n":      false,
		"GET / HTTP/1.0\r\nHost: a\r\n\r\n":                           false,
		"GET / HTTP/1.0\r\nHost: a\r\nConnection: keep-alive\r\n\r\n": true,
	}
	for raw, want := range cases {
		conn := newTestConn(t, raw)
		req, err := conn.Next()
		if err != nil {
			t.Fatalf("frame %q: %v", raw, err)
		}
		if req.KeepAlive != want {
			t.Fatalf("KeepAlive for %q = %v, want %v", raw, req.KeepAlive, want)
		}
	}
}
