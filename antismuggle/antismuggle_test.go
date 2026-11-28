package antismuggle

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func newTestConn(t *testing.T, raw string, cfg ...*Config) *Conn {
	t.Helper()
	c := DefaultConfig()
	if len(cfg) == 1 {
		c = cfg[0]
	}
	return NewConn(strings.NewReader(raw), c)
}

func nextHardError(t *testing.T, conn *Conn, want Reason) {
	t.Helper()
	req, err := conn.Next()
	if err == nil {
		t.Fatalf("expected hard error %q, got request %#v", want, req)
	}
	var he *Error
	if !errors.As(err, &he) {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if he.Reason != want {
		t.Fatalf("expected reason %q, got %q (%v)", want, he.Reason, he)
	}
	if !he.CloseConnection {
		t.Fatalf("hard error %q must force connection close", want)
	}
	if he.StatusCode() != 400 {
		t.Fatalf("hard error must map to 400, got %d", he.StatusCode())
	}
	if _, err2 := conn.Next(); err2 == nil {
		t.Fatalf("stream must remain poisoned after hard error")
	}
}

func drain(t *testing.T, req *Request) string {
	t.Helper()
	b, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("body read failed: %v", err)
	}
	return string(b)
}

// ---- (1) request line / header block hard validation ----

func TestBareLFRejected(t *testing.T) {
	raw := "POST / HTTP/1.1\nHost: a\nContent-Length: 6\n\n0\n\nG"
	nextHardError(t, newTestConn(t, raw), ReasonBareLF)

	raw2 := "POST / HTTP/1.1\r\nHost: a\nX: y\r\n\r\n"
	nextHardError(t, newTestConn(t, raw2), ReasonBareLF)
}

func TestBareCRRejected(t *testing.T) {
	raw := "POST / HTTP/1.1\rX\nHost: a\r\n\r\n"
	nextHardError(t, newTestConn(t, raw), ReasonBareCR)

	raw2 := "POST / HTTP/1.1\r\nHost: a\rX\r\n\r\n"
	nextHardError(t, newTestConn(t, raw2), ReasonBareCR)
}

func TestObsFoldRejected(t *testing.T) {
	raw := "POST / HTTP/1.1\r\nHost: a\r\nX: foo\r\n bar\r\n\r\n"
	nextHardError(t, newTestConn(t, raw), ReasonObsFold)

	rawTab := "POST / HTTP/1.1\r\nHost: a\r\nX: foo\r\n\tbar\r\n\r\n"
	nextHardError(t, newTestConn(t, rawTab), ReasonObsFold)
}

func TestBadHeaderNameRejected(t *testing.T) {
	cases := map[string]string{
		"space in name":  "POST / HTTP/1.1\r\nBad Name: x\r\n\r\n",
		"space before :": "POST / HTTP/1.1\r\nBad : x\r\n\r\n",
		"tab in name":    "POST / HTTP/1.1\r\nBad\tName: x\r\n\r\n",
		"no colon":       "POST / HTTP/1.1\r\nBadName\r\n\r\n",
		"empty name":     "POST / HTTP/1.1\r\n: x\r\n\r\n",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			nextHardError(t, newTestConn(t, raw), ReasonBadHeaderName)
		})
	}
}

func TestBadRequestLineRejected(t *testing.T) {
	cases := map[string]string{
		"extra spaces": "POST  / HTTP/1.1\r\nHost: a\r\n\r\n",
		"bad version":  "GET / HTTP/2.0\r\nHost: a\r\n\r\n",
		"empty method": " / HTTP/1.1\r\nHost: a\r\n\r\n",
		"empty target": "GET  HTTP/1.1\r\nHost: a\r\n\r\n",
		"method space": "GE T / HTTP/1.1\r\nHost: a\r\n\r\n",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			nextHardError(t, newTestConn(t, raw), ReasonBadRequestLine)
		})
	}
}

// ---- (2) framing anti-smuggling hard gates ----

func TestCLAndTECoexistRejected(t *testing.T) {
	raw := "POST / HTTP/1.1\r\nHost: a\r\nContent-Length: 6\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n"
	nextHardError(t, newTestConn(t, raw), ReasonBothCLAndTE)
}

func TestCLTESmuggleClassicsRejected(t *testing.T) {
	clte := "POST / HTTP/1.1\r\n" +
		"Host: a\r\n" +
		"Content-Length: 44\r\n" +
		"Transfer-Encoding: chunked\r\n\r\n" +
		"0\r\n\r\n" +
		"GET /admin HTTP/1.1\r\nHost: a\r\n\r\n"
	nextHardError(t, newTestConn(t, clte), ReasonBothCLAndTE)

	tecl := "POST / HTTP/1.1\r\n" +
		"Host: a\r\n" +
		"Transfer-Encoding:chunked\r\n\r\n" +
		"5e\r\n" +
		"GPOST / HTTP/1.1\r\nHost: a\r\nContent-Length: 15\r\n\r\n" +
		"x=1\r\n0\r\n\r\n"
	conn := newTestConn(t, tecl)
	req, err := conn.Next()
	if err != nil {
		t.Fatalf("header frame should parse: %v", err)
	}
	if req.Transfer != BodyChunked {
		t.Fatalf("expected chunked transfer")
	}
	if _, err := io.ReadAll(req.Body); err == nil {
		t.Fatalf("TE.CL smuggle payload must fail strict chunk decoding")
	} else {
		var he *Error
		if !errors.As(err, &he) {
			t.Fatalf("expected *Error, got %T", err)
		}
	}
}

func TestMultipleContentLengthRejected(t *testing.T) {
	same := "POST / HTTP/1.1\r\nHost: a\r\nContent-Length:5\r\nContent-Length:5\r\n\r\nhello"
	nextHardError(t, newTestConn(t, same), ReasonMultipleCL)

	conflict := "POST / HTTP/1.1\r\nHost: a\r\nContent-Length:5\r\nContent-Length:6\r\n\r\nhello"
	nextHardError(t, newTestConn(t, conflict), ReasonMultipleCL)

	comma := "POST / HTTP/1.1\r\nHost: a\r\nContent-Length:5, 5\r\n\r\nhello"
	nextHardError(t, newTestConn(t, comma), ReasonBadCL)

	cfg := DefaultConfig()
	cfg.RejectDuplicateContentLength = false
	conn := newTestConn(t, same, cfg)
	req, err := conn.Next()
	if err != nil {
		t.Fatalf("lenient policy should allow equal duplicate CL: %v", err)
	}
	if req.ContentLength != 5 {
		t.Fatalf("expected length 5, got %d", req.ContentLength)
	}
	nextHardError(t, newTestConn(t, conflict, cfg), ReasonMultipleCL)
}

func TestContentLengthSyntaxRejected(t *testing.T) {
	cases := map[string]string{
		"leading space":  "POST / HTTP/1.1\r\nHost: a\r\nContent-Length:  5\r\n\r\nhello",
		"trailing space": "POST / HTTP/1.1\r\nHost: a\r\nContent-Length: 5 \r\n\r\nhello",
		"plus sign":      "POST / HTTP/1.1\r\nHost: a\r\nContent-Length: +5\r\n\r\nhello",
		"negative":       "POST / HTTP/1.1\r\nHost: a\r\nContent-Length: -5\r\n\r\nhello",
		"hex":            "POST / HTTP/1.1\r\nHost: a\r\nContent-Length: 0x5\r\n\r\nhello",
		"trailing junk":  "POST / HTTP/1.1\r\nHost: a\r\nContent-Length: 5x\r\n\r\nhello",
		"empty":          "POST / HTTP/1.1\r\nHost: a\r\nContent-Length: \r\n\r\nhello",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			nextHardError(t, newTestConn(t, raw), ReasonBadCL)
		})
	}
}

func TestTransferEncodingObfuscationRejected(t *testing.T) {
	cases := map[string]string{
		"chunked, identity": "POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding: chunked, identity\r\n\r\n0\r\n\r\n",
		"identity, chunked": "POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding: identity, chunked\r\n\r\n0\r\n\r\n",
		"leading space":     "POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding:  chunked\r\n\r\n0\r\n\r\n",
		"trailing space":    "POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding: chunked \r\n\r\n0\r\n\r\n",
		"uppercase":         "POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding: Chunked\r\n\r\n0\r\n\r\n",
		"quoted":            "POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding: \"chunked\"\r\n\r\n0\r\n\r\n",
		"unknown coding":    "POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding: gzip\r\n\r\n0\r\n\r\n",
		"empty after comma": "POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding: chunked,\r\n\r\n0\r\n\r\n",
		"cow":               "POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding: cow\r\n\r\n0\r\n\r\n",
		"chunked non-last":  "POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding: chunked, cow\r\n\r\n0\r\n\r\n",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			nextHardError(t, newTestConn(t, raw), ReasonBadTE)
		})
	}

	multi := "POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding: chunked\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n"
	nextHardError(t, newTestConn(t, multi), ReasonBadTE)

	cfg := DefaultConfig()
	cfg.AllowChunkedCaseInsensitive = true
	conn := newTestConn(t,
		"POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding:CHUNKED\r\n\r\n0\r\n\r\n", cfg)
	req, err := conn.Next()
	if err != nil {
		t.Fatalf("case-insensitive policy should accept CHUNKED: %v", err)
	}
	if req.Transfer != BodyChunked {
		t.Fatalf("expected chunked transfer")
	}
	if _, err := io.ReadAll(req.Body); err != nil {
		t.Fatalf("empty chunked body should drain cleanly: %v", err)
	}
}

func TestConnectionTransferEncodingSmuggleRejected(t *testing.T) {
	raw := "POST / HTTP/1.1\r\nHost: a\r\nConnection: Transfer-Encoding\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\n"
	nextHardError(t, newTestConn(t, raw), ReasonBadConnectionOption)
}
