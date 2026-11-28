package antismuggle

import (
	"io"
	"testing"
)

// oneByteReader forces every underlying Read to return exactly one byte,
// exercising the chunked decoder state machine across all transition paths.
type oneByteReader struct {
	data []byte
	pos  int
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = r.data[r.pos]
	r.pos++
	return 1, nil
}

func TestChunkedOneByteReads(t *testing.T) {
	raw := []byte("POST / HTTP/1.1\r\nHost: a\r\nTransfer-Encoding:chunked\r\n\r\n" +
		"3\r\nabc\r\n2\r\nde\r\n0\r\nX-Trace: ok\r\n\r\n")
	cfg := DefaultConfig()
	cfg.AllowedTrailers = []string{"x-trace"}
	conn := NewConn(&oneByteReader{data: raw}, cfg)

	req, err := conn.Next()
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	got := make([]byte, 0, 5)
	buf := make([]byte, 1)
	for {
		n, err := req.Body.Read(buf)
		got = append(got, buf[:n]...)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read: %v", err)
		}
	}
	if string(got) != "abcde" {
		t.Fatalf("decoded = %q", got)
	}
	if _, err := conn.Next(); err != io.EOF {
		t.Fatalf("expected clean boundary EOF, got %v", err)
	}
}

func TestIdentityOneByteReads(t *testing.T) {
	raw := []byte("POST / HTTP/1.1\r\nHost: a\r\nContent-Length:5\r\n\r\nhello" +
		"GET /next HTTP/1.1\r\nHost: a\r\n\r\n")
	conn := NewConn(&oneByteReader{data: raw}, DefaultConfig())
	req, err := conn.Next()
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	if string(body) != "hello" {
		t.Fatalf("body = %q", body)
	}
	next, err := conn.Next()
	if err != nil {
		t.Fatalf("pipelined frame: %v", err)
	}
	if next.Target != "/next" {
		t.Fatalf("target = %q", next.Target)
	}
}

func TestBodyPendingRejected(t *testing.T) {
	conn := newTestConn(t,
		"POST / HTTP/1.1\r\nHost: a\r\nContent-Length:5\r\n\r\nhello")
	req, err := conn.Next()
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	// Deliberately do not drain req.Body: the first Next reports the pending
	// body and poisons the stream.
	if _, err := conn.Next(); err == nil {
		t.Fatalf("expected body_pending hard error")
	} else if he, ok := err.(*Error); !ok || he.Reason != ReasonBodyPending {
		t.Fatalf("expected body_pending, got %v", err)
	}
	if _, err := conn.Next(); err == nil {
		t.Fatalf("poisoned stream must stay closed")
	} else if he, ok := err.(*Error); !ok || he.Reason != ReasonBodyPending {
		t.Fatalf("first hard error must be sticky, got %v", err)
	}
	_ = req
}
