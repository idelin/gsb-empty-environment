package taskpkg

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var testResponse = []byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: frames\r\n\r\n")

func makeFrame(t *testing.T, frameType byte, body string) []byte {
	t.Helper()
	if len(body) > 0xFFFF {
		t.Fatalf("frame body too large: %d", len(body))
	}
	b := []byte{frameType, byte(len(body) >> 8), byte(len(body))}
	return append(b, body...)
}

// failAfterWriter writes at most limit bytes, then fails.
type failAfterWriter struct {
	limit int
	n     int
}

func (w *failAfterWriter) Write(p []byte) (int, error) {
	if w.n >= w.limit {
		return 0, errors.New("connection reset by peer")
	}
	n := min(len(p), w.limit-w.n)
	w.n += n
	return n, nil
}

type recorder struct {
	data       []string
	management []string
	rejects    []byte
}

func (r *recorder) handler() FrameHandler {
	return FrameHandler{
		OnData:       func(p []byte) { r.data = append(r.data, string(p)) },
		OnManagement: func(p []byte) { r.management = append(r.management, string(p)) },
		OnReject:     func(ft byte, err error) { r.rejects = append(r.rejects, ft) },
	}
}

func TestEarlyManagementFrameRejected(t *testing.T) {
	admitter := NewAdmitter([]byte("test-secret"), time.Minute)
	req := httptest.NewRequest("GET", "http://proxy.internal/socket", nil)
	session, err := admitter.Begin(1, req, "alice", "chat.v1", []byte{FrameTypeData})
	if err != nil {
		t.Fatal(err)
	}

	// A management frame arrives before any data, inside the pre-read buffer.
	preread := append(makeFrame(t, FrameTypeManagement, "shutdown"), makeFrame(t, FrameTypeData, "hello")...)
	var out bytes.Buffer
	if err := session.CompleteUpgrade(&out, testResponse, preread); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), testResponse) {
		t.Fatalf("response not written verbatim: %q", out.Bytes())
	}

	rec := &recorder{}
	if err := session.ServeFrames(strings.NewReader(""), rec.handler()); err != nil {
		t.Fatal(err)
	}
	if len(rec.management) != 0 {
		t.Fatalf("rejected management frame reached callback: %v", rec.management)
	}
	if len(rec.rejects) != 1 || rec.rejects[0] != FrameTypeManagement {
		t.Fatalf("expected one management rejection, got %v", rec.rejects)
	}
	if len(rec.data) != 1 || rec.data[0] != "hello" {
		t.Fatalf("data frame not delivered: %v", rec.data)
	}
}

func TestSubprotocolSubstitution(t *testing.T) {
	admitter := NewAdmitter([]byte("test-secret"), time.Minute)
	req := httptest.NewRequest("GET", "http://proxy.internal/socket", nil)
	digest := DigestRequest(req)

	session, err := admitter.Begin(2, req, "alice", "chat.v1", []byte{FrameTypeData})
	if err != nil {
		t.Fatal(err)
	}
	ticket := session.Ticket()

	// Attacker swaps the subprotocol on a copy of the ticket.
	forged := *ticket
	forged.Subprotocol = "admin.v1"
	if err := admitter.Issuer().Validate(&forged, 2, digest, "admin.v1"); !errors.Is(err, ErrTicketForged) {
		t.Fatalf("subprotocol substitution not detected: %v", err)
	}

	// Attacker escalates the allowed frame types.
	escalated := *ticket
	escalated.AllowedFrames = []byte{FrameTypeData, FrameTypeManagement}
	if err := admitter.Issuer().Validate(&escalated, 2, digest, "chat.v1"); !errors.Is(err, ErrTicketForged) {
		t.Fatalf("frame-type escalation not detected: %v", err)
	}

	// A genuine ticket presented for a different negotiated subprotocol.
	if err := admitter.Issuer().Validate(ticket, 2, digest, "admin.v1"); !errors.Is(err, ErrSubprotoMismatch) {
		t.Fatalf("subprotocol mismatch not detected: %v", err)
	}

	// Pipeline level: a session whose ticket was swapped for a forged admin
	// ticket must reject every frame and never fire the management callback.
	forgedSession, err := admitter.Begin(2, req, "alice", "chat.v1", []byte{FrameTypeData})
	if err != nil {
		t.Fatal(err)
	}
	adminTicket := *forgedSession.Ticket()
	adminTicket.Subprotocol = "admin.v1"
	adminTicket.AllowedFrames = []byte{FrameTypeManagement}
	forgedSession.ticket = &adminTicket

	var out bytes.Buffer
	if err := forgedSession.CompleteUpgrade(&out, testResponse, makeFrame(t, FrameTypeManagement, "pwn")); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	if err := forgedSession.ServeFrames(strings.NewReader(""), rec.handler()); err != nil {
		t.Fatal(err)
	}
	if len(rec.management) != 0 {
		t.Fatalf("forged ticket reached management callback: %v", rec.management)
	}
	if len(rec.rejects) != 1 {
		t.Fatalf("expected forged frame to be rejected, got %v", rec.rejects)
	}
}

func TestHalfWrittenResponse(t *testing.T) {
	admitter := NewAdmitter([]byte("test-secret"), time.Minute)
	req := httptest.NewRequest("GET", "http://proxy.internal/socket", nil)
	session, err := admitter.Begin(7, req, "bob", "chat.v1", []byte{FrameTypeData, FrameTypeManagement})
	if err != nil {
		t.Fatal(err)
	}

	w := &failAfterWriter{limit: len(testResponse) / 2}
	preread := makeFrame(t, FrameTypeManagement, "early-admin")
	err = session.CompleteUpgrade(w, testResponse, preread)
	if err == nil {
		t.Fatal("expected short-write error")
	}

	// The buffer must not have been transferred: no frame may be served.
	rec := &recorder{}
	if err := session.ServeFrames(strings.NewReader(""), rec.handler()); !errors.Is(err, ErrSessionAborted) {
		t.Fatalf("expected ErrSessionAborted, got %v", err)
	}
	if len(rec.management) != 0 || len(rec.data) != 0 {
		t.Fatalf("frames served after aborted upgrade: mgmt=%v data=%v", rec.management, rec.data)
	}

	// The aborted attempt must have advanced the connection generation,
	// invalidating the ticket.
	if err := admitter.Issuer().Validate(session.Ticket(), 7, DigestRequest(req), "chat.v1"); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("ticket of aborted upgrade still valid: %v", err)
	}
}

func TestConnectionGenerationReuse(t *testing.T) {
	admitter := NewAdmitter([]byte("test-secret"), time.Minute)
	req := httptest.NewRequest("GET", "http://proxy.internal/socket", nil)
	const connID = 42

	// First upgrade attempt on the connection aborts halfway.
	first, err := admitter.Begin(connID, req, "mallory", "chat.v1", []byte{FrameTypeData, FrameTypeManagement})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.CompleteUpgrade(&failAfterWriter{limit: 3}, testResponse, nil); err == nil {
		t.Fatal("expected short-write error")
	}

	// The connection is reused; the new session gets a fresh generation.
	second, err := admitter.Begin(connID, req, "mallory", "chat.v1", []byte{FrameTypeData})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := second.CompleteUpgrade(&out, testResponse, nil); err != nil {
		t.Fatal(err)
	}

	// The stale ticket from the aborted attempt must not validate.
	if err := admitter.Issuer().Validate(first.Ticket(), connID, DigestRequest(req), "chat.v1"); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale-generation ticket accepted: %v", err)
	}

	// The new session serves data frames but still rejects management frames.
	rec := &recorder{}
	stream := append(makeFrame(t, FrameTypeData, "ok"), makeFrame(t, FrameTypeManagement, "admin")...)
	if err := second.ServeFrames(bytes.NewReader(stream), rec.handler()); err != nil {
		t.Fatal(err)
	}
	if len(rec.management) != 0 {
		t.Fatalf("management frame reached callback on data-only ticket: %v", rec.management)
	}
	if len(rec.data) != 1 || rec.data[0] != "ok" {
		t.Fatalf("data frame not delivered on new generation: %v", rec.data)
	}

	// Recycling the connection again invalidates the live session's ticket:
	// subsequent frames are rejected before reaching any callback.
	admitter.Issuer().Advance(connID)
	rec2 := &recorder{}
	if err := second.ServeFrames(bytes.NewReader(makeFrame(t, FrameTypeData, "late")), rec2.handler()); err != nil {
		t.Fatal(err)
	}
	if len(rec2.data) != 0 || len(rec2.management) != 0 {
		t.Fatalf("frames served on stale generation: data=%v mgmt=%v", rec2.data, rec2.management)
	}
	if len(rec2.rejects) != 1 {
		t.Fatalf("expected stale-generation rejection, got %v", rec2.rejects)
	}
}

func TestDigestBinding(t *testing.T) {
	admitter := NewAdmitter([]byte("test-secret"), time.Minute)
	req := httptest.NewRequest("GET", "http://proxy.internal/socket", nil)
	session, err := admitter.Begin(9, req, "carol", "chat.v1", []byte{FrameTypeData})
	if err != nil {
		t.Fatal(err)
	}
	other := httptest.NewRequest("GET", "http://proxy.internal/admin", nil)
	if err := admitter.Issuer().Validate(session.Ticket(), 9, DigestRequest(other), "chat.v1"); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("ticket accepted for a different request: %v", err)
	}
}
