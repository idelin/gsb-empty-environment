package router

import (
	"errors"
	"sync"
	"testing"
)

const (
	originA = "https://app.example.com"
	originB = "https://other.example.com"
)

func testPolicy() *StaticPolicy {
	return NewStaticPolicy(map[string][]Capability{
		originA: {"read", "write"},
		originB: {"read"},
	})
}

// recordedSend is one captured Sink delivery.
type recordedSend struct {
	portID  string
	payload any
}

// recordingSink is a thread-safe Sink used by the tests.
type recordingSink struct {
	mu      sync.Mutex
	sends   []recordedSend
	gate    chan struct{}
	once    sync.Once
	block   map[string]bool
	inside  map[string]chan struct{}
	entered map[string]bool
}

func newRecordingSink() *recordingSink {
	return &recordingSink{
		gate:    make(chan struct{}),
		block:   make(map[string]bool),
		inside:  make(map[string]chan struct{}),
		entered: make(map[string]bool),
	}
}

func (s *recordingSink) Send(portID string, payload any) {
	s.mu.Lock()
	if s.block[portID] && !s.entered[portID] {
		s.entered[portID] = true
		inside := s.inside[portID]
		s.mu.Unlock()
		close(inside)
		<-s.gate
		s.mu.Lock()
	}
	s.sends = append(s.sends, recordedSend{portID: portID, payload: payload})
	s.mu.Unlock()
}

func (s *recordingSink) snapshot() []recordedSend {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]recordedSend, len(s.sends))
	copy(out, s.sends)
	return out
}

func (s *recordingSink) blockPort(portID string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	entered := make(chan struct{})
	s.block[portID] = true
	s.inside[portID] = entered
	return entered
}

func (s *recordingSink) release() { s.once.Do(func() { close(s.gate) }) }

// TestHandshake_BindsOriginFromTrustedMeta verifies that the authorization
// decision is driven solely by the browser-supplied metadata: a payload that
// claims a different origin is refused, and spoofing the payload origin
// cannot widen the allowlist decision.
func TestHandshake_BindsOriginFromTrustedMeta(t *testing.T) {
	r := New(testPolicy(), newRecordingSink())

	meta := TrustedMeta{TabID: 1, DocumentID: "doc-1", Origin: originA}

	port, err := r.Handshake(meta, HandshakeRequest{
		ClaimedOrigin: originA,
		PortID:        "p1",
		Capabilities:  []Capability{"read", "write"},
	})
	if err != nil {
		t.Fatalf("handshake with matching claimed origin: %v", err)
	}
	if got := port.Origin(); got != originA {
		t.Fatalf("port origin = %q, want %q", got, originA)
	}

	// The untrusted body must not be allowed to assert another origin.
	if _, err := r.Handshake(meta, HandshakeRequest{
		ClaimedOrigin: originB,
		PortID:        "p2",
	}); !errors.Is(err, ErrOriginMismatch) {
		t.Fatalf("spoofed payload origin: err = %v, want ErrOriginMismatch", err)
	}

	// Policy is evaluated against the trusted origin, not the payload.
	metaB := TrustedMeta{TabID: 2, DocumentID: "doc-2", Origin: originB}
	if _, err := r.Handshake(metaB, HandshakeRequest{
		PortID:       "p3",
		Capabilities: []Capability{"write"},
	}); !errors.Is(err, ErrRejected) {
		t.Fatalf("capability outside allowlist: err = %v, want ErrRejected", err)
	}

	if _, err := r.Handshake(TrustedMeta{}, HandshakeRequest{PortID: "p4"}); !errors.Is(err, ErrBadHandshake) {
		t.Fatalf("empty metadata: err = %v, want ErrBadHandshake", err)
	}
}

// TestNavigation_RevokesAllOldCapabilities is the core lifecycle rule: after
// a same-tab navigation, every port, capability and in-flight lease of the
// previous document is dead and cannot reach the replacement document.
func TestNavigation_RevokesAllOldCapabilities(t *testing.T) {
	sink := newRecordingSink()
	r := New(testPolicy(), sink)

	oldKey := DocKey{TabID: 7, DocumentID: "doc-A"}
	oldPort, err := r.Handshake(
		TrustedMeta{TabID: 7, DocumentID: "doc-A", Origin: originA},
		HandshakeRequest{PortID: "port-a", Capabilities: []Capability{"read", "write"}},
	)
	if err != nil {
		t.Fatalf("initial handshake: %v", err)
	}
	oldLease := oldPort.Lease()
	if err := oldPort.Deliver("before-navigation"); err != nil {
		t.Fatalf("delivery before navigation: %v", err)
	}

	// Same tab, new document id: the handshake must atomically revoke the
	// old document before binding the new one.
	newPort, err := r.Handshake(
		TrustedMeta{TabID: 7, DocumentID: "doc-B", Origin: originA},
		HandshakeRequest{PortID: "port-b", Capabilities: []Capability{"read"}},
	)
	if err != nil {
		t.Fatalf("post-navigation handshake: %v", err)
	}

	if !oldPort.Closed() {
		t.Fatal("old port still live after navigation")
	}
	if err := oldPort.Deliver("stale"); !errors.Is(err, ErrPortClosed) {
		t.Fatalf("old port delivery after navigation: err = %v, want ErrPortClosed", err)
	}
	if err := oldLease.Deliver("stale-lease"); !errors.Is(err, ErrPortClosed) {
		t.Fatalf("old lease delivery after navigation: err = %v, want ErrPortClosed", err)
	}

	docs := r.ActiveDocuments()
	if len(docs) != 1 || docs[0] != newPort.Document() {
		t.Fatalf("active documents = %v, want only doc-B", docs)
	}
	if err := newPort.Deliver("after-navigation"); err != nil {
		t.Fatalf("new port delivery: %v", err)
	}

	// Stale lifecycle events arriving late must be harmless no-ops and must
	// never revoke the replacement document's ports.
	if err := r.OnDisconnect("port-a"); !errors.Is(err, ErrUnknownPort) {
		t.Fatalf("stale disconnect: err = %v, want ErrUnknownPort", err)
	}
	if err := r.OnDocumentClose(oldKey); !errors.Is(err, ErrUnknownPort) {
		t.Fatalf("stale document close: err = %v, want ErrUnknownPort", err)
	}
	if newPort.Closed() {
		t.Fatal("stale events for doc-A tore down doc-B's port")
	}

	sends := sink.snapshot()
	for _, send := range sends {
		if send.portID == "port-a" && send.payload != "before-navigation" {
			t.Fatalf("old document received post-revocation delivery: %+v", send)
		}
	}
	if len(sends) != 2 {
		t.Fatalf("sends = %+v, want exactly the two legitimate deliveries", sends)
	}
}

// TestParallelDocuments_OutOfOrderDisconnects runs two documents in parallel
// and feeds disconnect/close events back in a shuffled order. Delayed
// callbacks must land only on the document that still owns their generation;
// tab-id and port-id reuse must never cross the document boundary.
func TestParallelDocuments_OutOfOrderDisconnects(t *testing.T) {
	sink := newRecordingSink()
	r := New(testPolicy(), sink)

	// Two parallel documents, one port each.
	port1, err := r.Handshake(
		TrustedMeta{TabID: 1, DocumentID: "doc-1", Origin: originA},
		HandshakeRequest{PortID: "worker-1", Capabilities: []Capability{"read"}},
	)
	if err != nil {
		t.Fatalf("handshake doc-1: %v", err)
	}
	port2, err := r.Handshake(
		TrustedMeta{TabID: 2, DocumentID: "doc-2", Origin: originA},
		HandshakeRequest{PortID: "worker-2", Capabilities: []Capability{"write"}},
	)
	if err != nil {
		t.Fatalf("handshake doc-2: %v", err)
	}

	// Slow workers capture leases before any teardown happens.
	lease1 := port1.Lease()
	lease2 := port2.Lease()

	// Tab 1 navigates: tab id is reused, old port is revoked, and a new
	// document reuses the very same port identifier.
	if err := r.OnDisconnect("worker-1"); err != nil {
		t.Fatalf("disconnect worker-1: %v", err)
	}
	port3, err := r.Handshake(
		TrustedMeta{TabID: 1, DocumentID: "doc-3", Origin: originA},
		HandshakeRequest{PortID: "worker-1", Capabilities: []Capability{"read"}},
	)
	if err != nil {
		t.Fatalf("handshake doc-3 with reused tab and port ids: %v", err)
	}

	// Stale events for the replaced doc-1 arrive interleaved with the live
	// generation's traffic and must never kill the replacement port.
	if err := r.OnDocumentClose(DocKey{TabID: 1, DocumentID: "doc-1"}); !errors.Is(err, ErrUnknownPort) {
		t.Fatalf("stale close doc-1: err = %v, want ErrUnknownPort", err)
	}
	// A stale disconnect carrying the reused port id is indistinguishable
	// from the live one on the wire; the duplicate event must therefore
	// remain idempotent even after the new port later disconnects. Assert
	// here that the stale document close above left the new port intact.
	if port3.Closed() {
		t.Fatal("out-of-order events for the old generation killed the new port")
	}

	// Late results from the previous generation are dropped.
	if err := lease1.Deliver("late-doc-1"); !errors.Is(err, ErrPortClosed) {
		t.Fatalf("lease from doc-1: err = %v, want ErrPortClosed", err)
	}
	if err := port1.Deliver("late-doc-1-direct"); !errors.Is(err, ErrPortClosed) {
		t.Fatalf("closed port delivery: err = %v, want ErrPortClosed", err)
	}

	// doc-2 is still alive, so its delayed callback goes through.
	if err := lease2.Deliver("late-doc-2"); err != nil {
		t.Fatalf("lease from live doc-2: %v", err)
	}

	// The delayed disconnect for doc-2 then arrives; repeat events are
	// idempotent and the revoked lease stays dead.
	if err := r.OnDisconnect("worker-2"); err != nil {
		t.Fatalf("late disconnect worker-2: %v", err)
	}
	if err := lease2.Deliver("after-disconnect"); !errors.Is(err, ErrPortClosed) {
		t.Fatalf("lease after disconnect: err = %v, want ErrPortClosed", err)
	}
	if err := r.OnDisconnect("worker-2"); !errors.Is(err, ErrUnknownPort) {
		t.Fatalf("duplicate disconnect: err = %v, want ErrUnknownPort", err)
	}

	// The replacement document remains fully functional.
	if err := port3.Deliver("doc-3-result"); err != nil {
		t.Fatalf("replacement port delivery: %v", err)
	}

	sends := sink.snapshot()
	got := map[string]int{}
	for _, send := range sends {
		got[send.payload.(string)]++
	}
	want := map[string]int{"late-doc-2": 1, "doc-3-result": 1}
	for payload, n := range want {
		if got[payload] != n {
			t.Fatalf("delivery %q count = %d, want %d; all sends: %+v", payload, got[payload], n, sends)
		}
	}
	for _, banned := range []string{"late-doc-1", "late-doc-1-direct", "after-disconnect"} {
		if _, ok := got[banned]; ok {
			t.Fatalf("stale payload %q was delivered: %+v", banned, sends)
		}
	}
}

// TestClose_DrainsInFlightDelivery guarantees revocation never completes
// while a goroutine is still inside the Sink, so no message can be emitted
// for a document after its capabilities have been torn down.
func TestClose_DrainsInFlightDelivery(t *testing.T) {
	sink := newRecordingSink()
	r := New(testPolicy(), sink)

	port, err := r.Handshake(
		TrustedMeta{TabID: 3, DocumentID: "doc-x", Origin: originA},
		HandshakeRequest{PortID: "slow", Capabilities: []Capability{"read"}},
	)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}

	entered := sink.blockPort("slow")
	delivered := make(chan error, 1)
	go func() { delivered <- port.Deliver("in-flight") }()
	<-entered

	closed := make(chan error, 1)
	go func() { closed <- r.OnDocumentClose(port.Document()) }()

	// Until the blocked send drains, close must not return.
	select {
	case err := <-closed:
		t.Fatalf("document close returned before in-flight delivery drained: %v", err)
	default:
	}

	sink.release()
	if err := <-delivered; err != nil {
		t.Fatalf("in-flight delivery: %v", err)
	}
	if err := <-closed; err != nil {
		t.Fatalf("document close: %v", err)
	}
	if err := port.Deliver("after-close"); !errors.Is(err, ErrPortClosed) {
		t.Fatalf("post-close delivery: err = %v, want ErrPortClosed", err)
	}
}

// TestHandshake_MultiplePortsPerDocument confirms a document can own several
// ports and that closing one leaves the others untouched.
func TestHandshake_MultiplePortsPerDocument(t *testing.T) {
	sink := newRecordingSink()
	r := New(testPolicy(), sink)
	meta := TrustedMeta{TabID: 9, DocumentID: "doc-m", Origin: originA}

	first, err := r.Handshake(meta, HandshakeRequest{PortID: "a"})
	if err != nil {
		t.Fatalf("first handshake: %v", err)
	}
	second, err := r.Handshake(meta, HandshakeRequest{PortID: "b"})
	if err != nil {
		t.Fatalf("second handshake on same document: %v", err)
	}
	if first.Document() != second.Document() {
		t.Fatalf("ports bound to %v and %v, want the same document", first.Document(), second.Document())
	}

	if err := r.OnDisconnect("a"); err != nil {
		t.Fatalf("disconnect port a: %v", err)
	}
	if !first.Closed() {
		t.Fatal("port a should be closed")
	}
	if second.Closed() {
		t.Fatal("port b must survive disconnect of its sibling")
	}
	if err := second.Deliver("sibling-alive"); err != nil {
		t.Fatalf("sibling delivery: %v", err)
	}

	if err := r.OnDocumentClose(DocKey{TabID: meta.TabID, DocumentID: meta.DocumentID}); err != nil {
		t.Fatalf("document close: %v", err)
	}
	if err := second.Deliver("after-doc-close"); !errors.Is(err, ErrPortClosed) {
		t.Fatalf("delivery after document close: err = %v, want ErrPortClosed", err)
	}

	sends := sink.snapshot()
	if len(sends) != 1 || sends[0].payload != "sibling-alive" {
		t.Fatalf("sends = %+v, want only the sibling delivery", sends)
	}
}
