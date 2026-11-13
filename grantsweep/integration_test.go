package grantsweep_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"example.org/netsec/q0158/grant"
)

func serviceKey() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = 7
	}
	return k
}

func newStore(t *testing.T, clock grant.Clock) *grant.Store {
	t.Helper()
	auth := grant.AuthorityFunc(func(grant.Principal, grant.KeyResource, grant.Context, grant.Context) error {
		return nil
	})
	s, err := grant.NewStore(serviceKey(), clock, auth)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func createReq() grant.CreateRequest {
	return grant.CreateRequest{
		GrantID: "g1", RequestID: "r1", Creator: "alice",
		Key:               grant.KeyResource{KeyID: "k1", Version: 1},
		GranteePrincipal:  "bob",
		RetiringPrincipal: "carol",
		Operations:        []grant.Operation{grant.OpDecrypt},
		Constraints:       grant.Context{"env": "prod"},
		TTL:               time.Hour,
		AllowPending:      true,
		PendingWindow:     time.Minute,
	}
}

func TestProductionStoreRejectsEveryModeledWeakness(t *testing.T) {
	clock := grant.NewFakeClock()
	s := newStore(t, clock)
	_, active, err := s.Create(createReq())
	if err != nil {
		t.Fatal(err)
	}

	base := grant.AuthorizeRequest{
		Caller: "bob", Operation: grant.OpDecrypt,
		Key:     grant.KeyResource{KeyID: "k1", Version: 1},
		Context: grant.Context{"env": "prod"}, Token: active.Marshal(),
	}
	mustDeny := func(name string, mut func(*grant.AuthorizeRequest)) {
		t.Helper()
		req := base
		mut(&req)
		if _, err := s.Authorize(req); err == nil {
			t.Fatalf("%s: production store must deny", name)
		}
	}

	mustDeny("extra context", func(r *grant.AuthorizeRequest) { r.Context["trace"] = "x" })
	mustDeny("missing context", func(r *grant.AuthorizeRequest) { r.Context = grant.Context{} })
	mustDeny("cross principal", func(r *grant.AuthorizeRequest) { r.Caller = "mallory" })
	mustDeny("wrong operation", func(r *grant.AuthorizeRequest) { r.Operation = grant.OpSign })
	mustDeny("rotated version", func(r *grant.AuthorizeRequest) { r.Key.Version = 2 })
	mustDeny("wrong key id", func(r *grant.AuthorizeRequest) { r.Key.KeyID = "k2" })

	// Subset grant: missing required key must deny.
	subReq := createReq()
	subReq.GrantID = "g2"
	subReq.RequestID = "r2"
	subReq.Constraints = nil
	subReq.Subset = grant.Context{"env": "prod"}
	_, subToken, err := s.Create(subReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authorize(grant.AuthorizeRequest{
		Caller: "bob", Operation: grant.OpDecrypt,
		Key:     grant.KeyResource{KeyID: "k1", Version: 1},
		Context: grant.Context{"trace": "x"}, Token: subToken.Marshal(),
	}); err == nil {
		t.Fatal("subset: production store must deny missing key")
	}
}

func TestProductionStorePendingIsBoundToCreatorAndRequest(t *testing.T) {
	clock := grant.NewFakeClock()
	s := newStore(t, clock)
	_, pending, err := s.BeginCreate(createReq())
	if err != nil {
		t.Fatal(err)
	}

	legit := grant.AuthorizeRequest{
		Caller: "alice", Operation: grant.OpDecrypt,
		Key:     grant.KeyResource{KeyID: "k1", Version: 1},
		Context: grant.Context{"env": "prod"}, Token: pending.Marshal(),
		RequestID: "r1", AllowPending: true,
	}
	if _, err := s.Authorize(legit); err != nil {
		t.Fatalf("legit pending use: %v", err)
	}

	foreign := legit
	foreign.Caller = "mallory"
	_, err = s.Authorize(foreign)
	if !errors.Is(err, grant.ErrDenied) {
		t.Fatalf("cross-client pending: want ErrDenied, got %v", err)
	}

	clock.Advance(2 * time.Minute)
	_, err = s.Authorize(legit)
	if !errors.Is(err, grant.ErrPendingWindow) {
		t.Fatalf("expired window: want ErrPendingWindow, got %v", err)
	}
}

// TestProductionStoreConcurrentRetire hammers Authorize and Retire with real
// goroutines. A successful Decrypt is only possible while the grant is Active;
// once Retire returns, every subsequent call must fail.
func TestProductionStoreConcurrentRetire(t *testing.T) {
	s := newStore(t, grant.NewFakeClock())
	_, tok, err := s.Create(createReq())
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var allowedAfterRetire, denied int64
	var mu sync.Mutex
	retired := false

	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			_, err := s.Authorize(grant.AuthorizeRequest{
				Caller: "bob", Operation: grant.OpDecrypt,
				Key:     grant.KeyResource{KeyID: "k1", Version: 1},
				Context: grant.Context{"env": "prod"}, Token: tok.Marshal(),
			})
			if err != nil {
				mu.Lock()
				denied++
				mu.Unlock()
				continue
			}
			mu.Lock()
			if retired {
				allowedAfterRetire++
			}
			mu.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		if err := s.Retire("carol", "g1"); err != nil {
			t.Errorf("Retire: %v", err)
		}
		mu.Lock()
		retired = true
		mu.Unlock()
	}()
	wg.Wait()

	if allowedAfterRetire != 0 {
		t.Fatalf("%d Decrypt calls succeeded after Retire returned", allowedAfterRetire)
	}
	if denied == 0 {
		t.Fatal("expected at least one post-retire denial")
	}
}
