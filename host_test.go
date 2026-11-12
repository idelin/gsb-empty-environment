package netsec

import (
	"errors"
	"testing"
)

const (
	rootUID  = 0
	nobodyID = 65534
)

// scriptedSnapshotter returns a fixed sequence of snapshots, one per call,
// to deterministically model a demotion landing mid-batch.
type scriptedSnapshotter struct {
	snaps []Credential
	ok    []bool
	idx   int
}

func (s *scriptedSnapshotter) Snapshot() (Credential, bool) {
	i := s.idx
	if i >= len(s.snaps) {
		i = len(s.snaps) - 1
	}
	s.idx++
	return s.snaps[i], s.ok[i]
}

func newRootConn(h *Host, reg *Registry) *Conn {
	cred, _ := reg.Snapshot()
	return h.Accept("peer-a", cred)
}

// Demotion interleaved with receives on a reused connection: messages
// received after the demotion must be authorized with the new, weaker
// snapshot, never with the connection's accept-time identity.
func TestDemotionInterleavedWithReceive(t *testing.T) {
	reg := NewRegistry(rootUID, rootUID)
	h := NewHost(reg)
	conn := newRootConn(h, reg)

	before, err := h.Receive(conn, []byte("h1"), []byte("b1"))
	if err != nil {
		t.Fatalf("receive before demotion: %v", err)
	}
	if err := before.Authorize(RequireUID(rootUID)); err != nil {
		t.Fatalf("pre-demotion message should authorize as root: %v", err)
	}

	reg.Demote(nobodyID, nobodyID)

	after, err := h.Receive(conn, []byte("h2"), []byte("b2"))
	if err != nil {
		t.Fatalf("receive after demotion: %v", err)
	}
	if got := after.Credential(); got.UID != nobodyID || got.Gen != 2 {
		t.Fatalf("post-demotion snapshot = %+v, want uid=%d gen=2", got, nobodyID)
	}
	if err := after.Authorize(RequireUID(rootUID)); !errors.Is(err, ErrDenied) {
		t.Fatalf("reused connection must not extend root privileges, got %v", err)
	}
	if err := after.Authorize(RequireUID(nobodyID)); err != nil {
		t.Fatalf("post-demotion message should authorize as nobody: %v", err)
	}

	// The connection identity remains the old one and is audit-only.
	if id := conn.AuditIdentity(); id.UID != rootUID {
		t.Fatalf("audit identity = %+v, want original uid=%d", id, rootUID)
	}
}

// A batch that straddles a demotion: items received after the demotion
// must carry the new generation even though earlier items in the same
// batch carried the old one.
func TestBatchStraddlingDemotion(t *testing.T) {
	src := &scriptedSnapshotter{
		snaps: []Credential{
			{UID: rootUID, Gen: 1},
			{UID: rootUID, Gen: 1},
			{UID: nobodyID, Gen: 2},
			{UID: nobodyID, Gen: 2},
		},
		ok: []bool{true, true, true, true},
	}
	h := NewHost(src)
	conn := h.Accept("peer-b", Credential{UID: rootUID, Gen: 1})

	items := []Envelope{
		{Handle: []byte("h1"), Body: []byte("b1")},
		{Handle: []byte("h2"), Body: []byte("b2")},
		{Handle: []byte("h3"), Body: []byte("b3")},
		{Handle: []byte("h4"), Body: []byte("b4")},
	}
	msgs, errs := h.ReceiveBatch(conn, items)
	for i, err := range errs {
		if err != nil {
			t.Fatalf("item %d: %v", i, err)
		}
	}
	for i, want := range []struct {
		uid uint32
		gen Generation
	}{{rootUID, 1}, {rootUID, 1}, {nobodyID, 2}, {nobodyID, 2}} {
		if got := msgs[i].Credential(); got.UID != want.uid || got.Gen != want.gen {
			t.Fatalf("item %d snapshot = %+v, want uid=%d gen=%d", i, got, want.uid, want.gen)
		}
	}
	if err := msgs[3].Authorize(RequireUID(rootUID)); !errors.Is(err, ErrDenied) {
		t.Fatalf("post-demotion batch item must not authorize as root, got %v", err)
	}
}

// Missing trusted credentials: receives fail closed, and a batch reports
// per-item failures without aborting the items that follow a recovery.
func TestMissingCredentials(t *testing.T) {
	reg := NewRegistry(rootUID, rootUID)
	h := NewHost(reg)
	conn := newRootConn(h, reg)

	reg.Clear()
	if _, err := h.Receive(conn, []byte("h"), []byte("b")); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("receive without credentials = %v, want ErrNoCredential", err)
	}

	src := &scriptedSnapshotter{
		snaps: []Credential{
			{},
			{UID: nobodyID, Gen: 2},
		},
		ok: []bool{false, true},
	}
	h2 := NewHost(src)
	conn2 := h2.Accept("peer-c", Credential{UID: rootUID, Gen: 1})
	msgs, errs := h2.ReceiveBatch(conn2, []Envelope{
		{Handle: []byte("h1"), Body: []byte("b1")},
		{Handle: []byte("h2"), Body: []byte("b2")},
	})
	if !errors.Is(errs[0], ErrNoCredential) {
		t.Fatalf("item 0 err = %v, want ErrNoCredential", errs[0])
	}
	if errs[1] != nil {
		t.Fatalf("item 1 err = %v, want nil", errs[1])
	}
	if got := msgs[1].Credential(); got.UID != nobodyID || got.Gen != 2 {
		t.Fatalf("item 1 snapshot = %+v, want uid=%d gen=2", got, nobodyID)
	}
}

// Handle and body of one message must belong to the same generation:
// mixed-generation assembly and stale frames are both rejected.
func TestHandleBodySameGeneration(t *testing.T) {
	reg := NewRegistry(rootUID, rootUID)
	h := NewHost(reg)
	conn := newRootConn(h, reg)

	handleFrame := Frame{Gen: 1, Data: []byte("h")}
	reg.Demote(nobodyID, nobodyID) // handle arrived before the demotion
	bodyFrame := Frame{Gen: 2, Data: []byte("b")}

	if _, err := h.Assemble(conn, handleFrame, bodyFrame); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("mixed-generation assemble = %v, want ErrGenerationMismatch", err)
	}
	if _, err := h.Assemble(conn, handleFrame, handleFrame); !errors.Is(err, ErrGenerationMismatch) {
		t.Fatalf("stale-generation assemble = %v, want ErrGenerationMismatch", err)
	}

	msg, err := h.Assemble(conn, bodyFrame, Frame{Gen: 2, Data: []byte("b2")})
	if err != nil {
		t.Fatalf("same-generation assemble: %v", err)
	}
	if got := msg.Credential(); got.UID != nobodyID || got.Gen != 2 {
		t.Fatalf("assembled snapshot = %+v, want uid=%d gen=2", got, nobodyID)
	}
	if err := msg.Authorize(RequireUID(rootUID)); !errors.Is(err, ErrDenied) {
		t.Fatalf("stale handle must not smuggle root privileges, got %v", err)
	}
}

// Receive stamps handle and body from a single snapshot, so they always
// share one generation.
func TestReceiveStampsSingleGeneration(t *testing.T) {
	reg := NewRegistry(rootUID, rootUID)
	h := NewHost(reg)
	conn := newRootConn(h, reg)

	msg, err := h.Receive(conn, []byte("h"), []byte("b"))
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if msg.handleGen != msg.bodyGen || msg.handleGen != msg.cred.Gen {
		t.Fatalf("handle gen=%d body gen=%d cred gen=%d, want all equal",
			msg.handleGen, msg.bodyGen, msg.cred.Gen)
	}
}
