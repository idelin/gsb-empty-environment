package sessiontakeover

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestCleanStartDiscardsState(t *testing.T) {
	r := newTestRegistry(t, ConflictReject, newFakeClock(), newFakeInspector())

	first := mustConnect(t, r, connectReq("t1", "dev", "alice", "c1"))
	if err := first.AddSubscription("alice/old", 1); err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}
	if err := first.EnqueueOffline(Message{Topic: "t", Payload: []byte("old"), QoS: 1}); err != nil {
		t.Fatalf("EnqueueOffline: %v", err)
	}
	if err := first.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	req := connectReq("t1", "dev", "alice", "c2")
	req.CleanStart = true
	h, res, err := r.Connect(context.Background(), req)
	if err != nil {
		t.Fatalf("clean start Connect: %v", err)
	}
	if res.Outcome != OutcomeCreated || res.Reason != ReasonCleanStart || res.SessionPresent {
		t.Fatalf("clean start result = %+v", res)
	}
	if subs, _ := h.Subscriptions(); len(subs) != 0 {
		t.Fatalf("clean start inherited subscriptions: %+v", subs)
	}
	if h.session.OfflineDepth() != 0 {
		t.Fatalf("clean start inherited offline queue")
	}

	// A normal later resume starts accumulating fresh state.
	again := mustConnect(t, r, connectReq("t1", "dev", "alice", "c3"))
	if err := again.AddSubscription("alice/new", 1); err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}
	assertSubscriptions(t, again, []string{"alice/new"})
}

func TestInvalidAndClosedRegistry(t *testing.T) {
	r := newTestRegistry(t, ConflictReject, newFakeClock(), newFakeInspector())

	bad := connectReq("t1", "", "alice", "c1")
	if _, _, err := r.Connect(context.Background(), bad); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty clientID err = %v", err)
	}
	bad = ConnectRequest{ClientID: "dev", Presented: CredentialRef{ID: "c", Identity: Identity{Tenant: "t1"}}}
	if _, _, err := r.Connect(context.Background(), bad); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty subject err = %v", err)
	}

	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := r.Close(); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("double Close err = %v", err)
	}
	if _, _, err := r.Connect(context.Background(), connectReq("t1", "dev", "alice", "c1")); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("Connect after Close err = %v", err)
	}
	if r.Sweep() != 0 {
		t.Fatalf("Sweep on closed registry mutated state")
	}
}

func TestTerminateAndDisplaceCallback(t *testing.T) {
	r := newTestRegistry(t, ConflictReject, newFakeClock(), newFakeInspector())

	h := mustConnect(t, r, connectReq("t1", "dev", "alice", "c1"))
	var displaced atomic.Int32
	h.OnDisplaced = func(reason DisplaceReason) {
		if reason != DisplaceTakeover {
			t.Errorf("displace reason = %v, want takeover", reason)
		}
		displaced.Add(1)
	}
	if err := h.AddSubscription("alice/x", 1); err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}

	next := mustConnect(t, r, connectReq("t1", "dev", "alice", "c2"))
	if displaced.Load() != 1 {
		t.Fatalf("OnDisplaced fired %d times, want 1", displaced.Load())
	}
	if gen := next.Generation(); gen != 2 {
		t.Fatalf("generation = %d, want 2", gen)
	}
	id, err := next.Identity()
	if err != nil || id.Subject != "alice" || id.Tenant != "t1" {
		t.Fatalf("Identity = %+v, %v", id, err)
	}
	sess, err := next.Session()
	if err != nil || sess.ClientID() != "dev" {
		t.Fatalf("Session accessor = %v, %v", sess, err)
	}

	// Old handle operations all fail; double-disconnect is also rejected.
	if err := h.Disconnect(); !errors.Is(err, ErrHandleClosed) {
		t.Fatalf("displaced Disconnect err = %v", err)
	}

	if !r.TerminateSession("t1", "dev", "alice") {
		t.Fatalf("TerminateSession returned false")
	}
	if r.TerminateSession("t1", "dev", "alice") {
		t.Fatalf("double TerminateSession reported deletion twice")
	}
	if err := next.AddSubscription("post-delete", 0); !errors.Is(err, ErrHandleClosed) {
		t.Fatalf("terminated handle still usable: %v", err)
	}
	if _, ok := r.Lookup("t1", "dev", "alice"); ok {
		t.Fatalf("terminated session still present")
	}
	// Terminated sessions give a fresh start on the next CONNECT.
	fresh := mustConnect(t, r, connectReq("t1", "dev", "alice", "c3"))
	if subs, _ := fresh.Subscriptions(); len(subs) != 0 {
		t.Fatalf("terminated state resurrected: %+v", subs)
	}
}

func TestSubscriptionRemovalAndAccessors(t *testing.T) {
	r := newTestRegistry(t, ConflictReject, newFakeClock(), newFakeInspector())
	h := mustConnect(t, r, connectReq("t1", "dev", "alice", "c1"))

	if err := h.AddSubscription("a/b", 2); err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}
	if err := h.AddSubscription("", 1); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty filter err = %v", err)
	}
	if err := h.AddSubscription("a/c", 3); !errors.Is(err, ErrInvalidQoS) {
		t.Fatalf("bad QoS err = %v", err)
	}
	if !h.session.HasSubscription("a/b") {
		t.Fatalf("HasSubscription missed live filter")
	}
	if err := h.RemoveSubscription("a/b"); err != nil {
		t.Fatalf("RemoveSubscription: %v", err)
	}
	if h.session.HasSubscription("a/b") {
		t.Fatalf("subscription survived removal")
	}
	if h.session.ExpiresAt().IsZero() != true {
		t.Fatalf("default session should never expire")
	}
	if msgs := h.session.OfflineMessages(); len(msgs) != 0 {
		t.Fatalf("unexpected offline messages: %+v", msgs)
	}
}

func TestOutboundLedgerErrors(t *testing.T) {
	r := newTestRegistry(t, ConflictReject, newFakeClock(), newFakeInspector())
	h := mustConnect(t, r, connectReq("t1", "dev", "alice", "c1"))

	if err := h.TrackOutbound(1, Message{QoS: 0}); !errors.Is(err, ErrInvalidQoS) {
		t.Fatalf("QoS0 track err = %v", err)
	}
	if err := h.TrackOutbound(9, Message{QoS: 2}); err != nil {
		t.Fatalf("TrackOutbound: %v", err)
	}
	if err := h.TrackOutbound(9, Message{QoS: 2}); !errors.Is(err, ErrDuplicatePacketID) {
		t.Fatalf("duplicate packet err = %v", err)
	}
	if err := h.MarkPubRec(99); !errors.Is(err, ErrUnknownPacketID) {
		t.Fatalf("unknown MarkPubRec err = %v", err)
	}
	if err := h.AckOutbound(99); !errors.Is(err, ErrUnknownPacketID) {
		t.Fatalf("unknown AckOutbound err = %v", err)
	}
}

func TestInboundWindowPruning(t *testing.T) {
	clock := newFakeClock()
	s := &Session{
		tenant:     "t1",
		clientID:   "dev",
		subject:    "alice",
		subs:       make(map[string]Subscription),
		offline:    make([]queuedItem, 0),
		outbound:   newOutboundLedger(),
		inbound:    newInboundLedger(3),
		active:     true,
		maxOffline: 2,
	}
	for _, id := range []uint16{1, 2, 3} {
		if s.RememberInbound(id, clock.Now()) {
			t.Fatalf("fresh ID %d flagged duplicate", id)
		}
		clock.Advance(time.Second)
	}
	// Fourth ID evicts the oldest (ID 1); recent ones stay protected.
	if s.RememberInbound(4, clock.Now()) {
		t.Fatalf("fresh ID 4 flagged duplicate")
	}
	if s.IsInboundDuplicate(2) != true || s.IsInboundDuplicate(3) != true {
		t.Fatalf("recent packet IDs were evicted prematurely")
	}
	if s.IsInboundDuplicate(1) {
		t.Fatalf("oldest ID not pruned at window boundary")
	}
}

func TestBackgroundReaper(t *testing.T) {
	clock := newFakeClock()
	r := NewRegistry(Config{
		ConflictPolicy:       ConflictReject,
		Inspector:            AllowAllInspector{},
		Clock:                clock,
		DefaultSessionExpiry: time.Minute,
		ReapInterval:         5 * time.Millisecond,
	})

	h := mustConnect(t, r, connectReq("t1", "dev", "alice", "c1"))
	if err := h.Disconnect(); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	clock.Advance(2 * time.Minute)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := r.Lookup("t1", "dev", "alice"); !ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := r.Lookup("t1", "dev", "alice"); ok {
		t.Fatalf("background reaper did not remove expired session")
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close reaper: %v", err)
	}
}

func TestWillSetClearedAndMessageDefensiveCopies(t *testing.T) {
	r := newTestRegistry(t, ConflictReject, newFakeClock(), newFakeInspector())
	h := mustConnect(t, r, connectReq("t1", "dev", "alice", "c1"))

	payload := []byte("secret")
	if err := h.SetWill(&Will{Topic: "w", Payload: payload, QoS: 1}); err != nil {
		t.Fatalf("SetWill: %v", err)
	}
	payload[0] = 'X' // mutating caller memory must not corrupt stored will
	if got, _ := h.Will(); string(got.Payload) != "secret" {
		t.Fatalf("stored will aliases caller payload: %q", got.Payload)
	}

	if err := h.SetWill(nil); err != nil {
		t.Fatalf("clear will: %v", err)
	}
	if got, _ := h.Will(); got != nil {
		t.Fatalf("will not cleared: %+v", got)
	}

	msg := Message{Topic: "t", Payload: []byte("abc"), QoS: 1}
	if err := h.EnqueueOffline(msg); err != nil {
		t.Fatalf("EnqueueOffline: %v", err)
	}
	msg.Payload[0] = 'Z'
	queued, _ := h.DrainOffline()
	if string(queued[0].Payload) != "abc" {
		t.Fatalf("queued message aliases caller payload: %q", queued[0].Payload)
	}
}
