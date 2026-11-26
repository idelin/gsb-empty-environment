package doublesign

import (
	"sync"
	"testing"
)

// Concurrent honest shares from many members must never produce spurious
// convictions, and a concurrent equivocation must freeze exactly once.
func TestConcurrentObserve(t *testing.T) {
	keys := []testKey{
		genKey(t, "v1"),
		genKey(t, "v2"),
		genKey(t, "v3"),
	}
	members := make([]Member, len(keys))
	for i, k := range keys {
		members[i] = k.member(1)
	}
	authID, authPriv, authPub := genAuthority(t)
	set := EpochSet{Epoch: 1, ChainID: testChain, Members: members}
	a := NewAuditor(testChain)
	mustRegister(t, a, set, AnchorRoot(authID, authPriv, set), authPub)

	var wg sync.WaitGroup
	for i, k := range keys {
		wg.Add(1)
		go func(idx int, key testKey) {
			defer wg.Done()
			g := digest(byte(0xA0 + idx))
			limit := uint64(20)
			if idx == 0 {
				limit = 4 // keep v1's honest shares below its equivocation round
			}
			for r := uint64(0); r < limit; r++ {
				a.Observe(share(key, 1, 0, r, PhasePrepare, g, nil, 0))
			}
		}(i, k)
	}

	// v1 equivocates concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.Observe(share(keys[0], 1, 0, 3, PhasePrepare, digest(0xFF), nil, 0))
	}()
	wg.Wait()

	if !a.IsFrozen(keys[0].id) {
		t.Fatal("equivocating v1 must be frozen under concurrency")
	}
	for _, k := range keys[1:] {
		if a.IsFrozen(k.id) {
			t.Fatalf("honest %s must not be frozen", k.id)
		}
	}
	if len(a.FrozenMembers()) != 1 {
		t.Fatalf("expected exactly one frozen member, got %v", a.FrozenMembers())
	}
}
