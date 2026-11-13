package netsec

import (
	"bufio"
	"bytes"
	"fmt"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
)

func header(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return h
}

func TestAdjudicate(t *testing.T) {
	cases := []struct {
		name    string
		header  http.Header
		bodyLen int
		want    Decision
	}{
		{"both headers rejected", header("Content-Length", "10", "Transfer-Encoding", "chunked"), 10, DecisionReject},
		{"te only rejected", header("Transfer-Encoding", "chunked"), 0, DecisionReject},
		{"cl only matching forwarded", header("Content-Length", "5"), 5, DecisionForward},
		{"cl only short body rejected", header("Content-Length", "5"), 4, DecisionReject},
		{"cl only long body rejected", header("Content-Length", "5"), 6, DecisionReject},
		{"cl zero empty body forwarded", header("Content-Length", "0"), 0, DecisionForward},
		{"duplicate cl rejected", header("Content-Length", "5", "Content-Length", "5"), 5, DecisionReject},
		{"invalid cl rejected", header("Content-Length", "abc"), 0, DecisionReject},
		{"negative cl rejected", header("Content-Length", "-1"), 0, DecisionReject},
		{"no headers empty body forwarded", header(), 0, DecisionForward},
		{"no headers with body rejected", header(), 3, DecisionReject},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Adjudicate(tc.header, tc.bodyLen); got.Decision != tc.want {
				t.Fatalf("Adjudicate() = %v (%s), want %v", got.Decision, got.Reason, tc.want)
			}
		})
	}
}

func TestBuildDualHeaderRequest(t *testing.T) {
	const firstChunk = "HELLO"
	const smuggled = "GET /smuggled HTTP/1.1\r\nHost: gateway.internal\r\n\r\n"

	raw := BuildDualHeaderRequest("POST", "/upload", firstChunk, smuggled)

	// Parse the header block with textproto so both Content-Length and
	// Transfer-Encoding survive (net/http would strip Content-Length).
	br := bufio.NewReader(bytes.NewReader(raw))
	if _, err := br.ReadString('\n'); err != nil { // request line
		t.Fatalf("read request line: %v", err)
	}
	mime, err := textproto.NewReader(br).ReadMIMEHeader()
	if err != nil {
		t.Fatalf("read headers: %v", err)
	}
	header := http.Header(mime)
	cl, err := strconv.Atoi(header.Get("Content-Length"))
	if err != nil || cl <= 0 {
		t.Fatalf("missing Content-Length: %v", err)
	}
	if !strings.EqualFold(header.Get("Transfer-Encoding"), "chunked") {
		t.Fatal("missing Transfer-Encoding: chunked")
	}

	// The gateway must reject this dual-header shape.
	if v := Adjudicate(header, cl); v.Decision != DecisionReject {
		t.Fatalf("dual-header request not rejected: %v", v)
	}

	// Locate the body and confirm Content-Length ends exactly at the end of
	// the first chunk.
	body := raw[bytes.Index(raw, []byte("\r\n\r\n"))+4:]
	wantPrefix := fmt.Sprintf("%x\r\n%s\r\n", len(firstChunk), firstChunk)
	if cl != len(wantPrefix) {
		t.Fatalf("Content-Length = %d, want %d (end of first chunk)", cl, len(wantPrefix))
	}
	if string(body[:cl]) != wantPrefix {
		t.Fatalf("body[:CL] = %q, want %q", body[:cl], wantPrefix)
	}

	// What the front-end leaves behind starts with the terminating chunk and
	// then the smuggled request.
	rest := string(body[cl:])
	if !strings.HasPrefix(rest, "0\r\n\r\n"+smuggled) {
		t.Fatalf("trailing bytes = %q, want terminating chunk + smuggled request", rest)
	}
}
