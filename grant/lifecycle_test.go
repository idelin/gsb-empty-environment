package grant

import (
	"errors"
	"testing"
	"time"
)

func TestPendingPropagationRules(t *testing.T) {
	clock := NewFakeClock()
	s := newTestStore(t, clock)
	req := baseCreate()
	req.AllowPending = true
	req.PendingWindow = time.Minute

	g, tok, err := s.BeginCreate(req)
	if err != nil {
		t.Fatal(err)
	}
	if g.Status != StatusPending {
		t.Fatalf("BeginCreate status=%s", g.Status)
	}

	pendingCall := AuthorizeRequest{
		Caller: "alice", Operation: OpDecrypt,
		Key:     KeyResource{KeyID: "k1", Version: 1},
		Context: Context{"env": "prod"}, Token: tok.Marshal(),
		RequestID: "req1", AllowPending: true,
	}
	if _, err := s.Authorize(pendingCall); err != nil {
		t.Fatalf("pending call inside window: %v", err)
	}

	foreign := pendingCall
	foreign.Caller = "mallory"
	if _, err := s.Authorize(foreign); !errors.Is(err, ErrDenied) {
		t.Fatalf("cross-client pending: want ErrDenied, got %v", err)
	}

	noOptIn := pendingCall
	noOptIn.AllowPending = false
	if _, err := s.Authorize(noOptIn); !errors.Is(err, ErrPendingWindow) {
		t.Fatalf("no opt-in: want ErrPendingWindow, got %v", err)
	}

	wrongReq := pendingCall
	wrongReq.RequestID = "other"
	if _, err := s.Authorize(wrongReq); !errors.Is(err, ErrDenied) {
		t.Fatalf("wrong requestID: want ErrDenied, got %v", err)
	}

	clock.Advance(2 * time.Minute)
	if _, err := s.Authorize(pendingCall); !errors.Is(err, ErrPendingWindow) {
		t.Fatalf("expired window: want ErrPendingWindow, got %v", err)
	}
}

func TestPendingTokenRejectedWhenNotPermitted(t *testing.T) {
	s := newTestStore(t, NewFakeClock())
	_, tok, err := s.BeginCreate(baseCreate())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Authorize(AuthorizeRequest{
		Caller: "alice", Operation: OpDecrypt,
		Key:     KeyResource{KeyID: "k1", Version: 1},
		Context: Context{"env": "prod"}, Token: tok.Marshal(),
		RequestID: "req1", AllowPending: true,
	})
	if !errors.Is(err, ErrPendingWindow) {
		t.Fatalf("pending not permitted: want ErrPendingWindow, got %v", err)
	}
}

func TestActivationIssuesNewTokenAndKillsPending(t *testing.T) {
	s := newTestStore(t, NewFakeClock())
	req := baseCreate()
	req.AllowPending = true
	req.PendingWindow = time.Minute

	_, pending, err := s.BeginCreate(req)
	if err != nil {
		t.Fatal(err)
	}
	_, active, err := s.Activate("alice", "req1")
	if err != nil {
		t.Fatal(err)
	}
	if pending.Marshal() == active.Marshal() {
		t.Fatal("activation must mint a distinct token")
	}

	_, err = s.Authorize(AuthorizeRequest{
		Caller: "alice", Operation: OpDecrypt,
		Key:     KeyResource{KeyID: "k1", Version: 1},
		Context: Context{"env": "prod"}, Token: pending.Marshal(),
		RequestID: "req1", AllowPending: true,
	})
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("pending token after activation: want ErrDenied, got %v", err)
	}

	if _, err := s.Authorize(AuthorizeRequest{
		Caller: "bob", Operation: OpDecrypt,
		Key:     KeyResource{KeyID: "k1", Version: 1},
		Context: Context{"env": "prod"}, Token: active.Marshal(),
	}); err != nil {
		t.Fatalf("active token: %v", err)
	}
}

func TestIdempotentCreateAndLostResponse(t *testing.T) {
	s := newTestStore(t, NewFakeClock())
	req := baseCreate()
	g1, t1, err := s.Create(req)
	if err != nil {
		t.Fatal(err)
	}
	g2, t2, err := s.Create(req)
	if err != nil {
		t.Fatal(err)
	}
	if g1.GrantID != g2.GrantID || t1.Marshal() != t2.Marshal() {
		t.Fatal("replayed Create must return the same grant and token")
	}

	other := req
	other.Creator = "mallory"
	_, _, err = s.Create(other)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-client GrantID reuse: want ErrConflict, got %v", err)
	}

	mutated := req
	mutated.Operations = []Operation{OpSign}
	_, _, err = s.Create(mutated)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("mutated replay: want ErrConflict, got %v", err)
	}
}

func TestAuthorityDenialLeavesUnusablePending(t *testing.T) {
	clock := NewFakeClock()
	store, err := NewStore(testKey, clock, AuthorityFunc(func(Principal, KeyResource, Context, Context) error {
		return errors.New("host policy forbids Grant")
	}))
	if err != nil {
		t.Fatal(err)
	}
	req := baseCreate()
	req.AllowPending = true
	req.PendingWindow = time.Minute
	_, tok, err := store.BeginCreate(req)
	if !errors.Is(err, ErrPermission) || tok != nil {
		t.Fatalf("want ErrPermission and nil token, got %v", err)
	}

	_, _, err = store.Activate("alice", "req1")
	if !errors.Is(err, ErrPermission) {
		t.Fatalf("activation after denied grant: want ErrPermission, got %v", err)
	}
}
