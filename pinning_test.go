package netsec

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// hookedStore wraps MemoryStore and invokes a hook whenever a snapshot reads
// a specific alias, simulating a rebind arriving in the middle of chain
// traversal.
type hookedStore struct {
	*MemoryStore

	mu      sync.Mutex
	hookRef AliasRef
	hook    func()
	fired   bool
}

func (s *hookedStore) Snapshot(ctx context.Context) (StoreSnapshot, error) {
	base, err := s.MemoryStore.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return &hookedSnapshot{StoreSnapshot: base, store: s}, nil
}

type hookedSnapshot struct {
	StoreSnapshot
	store *hookedStore
}

func (s *hookedSnapshot) LookupAlias(ref AliasRef) (Binding, bool) {
	binding, ok := s.StoreSnapshot.LookupAlias(ref)
	s.store.mu.Lock()
	if !s.store.fired && ref == s.store.hookRef && s.store.hook != nil {
		s.store.fired = true
		hook := s.store.hook
		s.store.mu.Unlock()
		hook()
		return binding, ok
	}
	s.store.mu.Unlock()
	return binding, ok
}

func TestRebindDuringResolutionIsIgnoredByTransaction(t *testing.T) {
	store := NewMemoryStore()
	keyV1, fpV1 := testKey(t, store, acctA, "key-v1")
	_, fpV2 := testKey(t, store, acctA, "key-v2")
	if fpV1 == fpV2 {
		t.Fatal("rebind target must have a distinct fingerprint")
	}

	entry := ref(acctA, regionCN, aliasSign)
	hop2 := ref(acctA, regionCN, "hop-2")
	hop3 := ref(acctA, regionCN, "hop-3")
	mustBind(t, store, hop3, keyTarget(keyV1.ID))
	mustBind(t, store, hop2, aliasTarget(hop3))
	mustBind(t, store, entry, aliasTarget(hop2))

	hooked := &hookedStore{MemoryStore: store, hookRef: hop2}
	caller := NewCallerIdentity("builder-rebind", []AccountID{acctA})
	resolver := NewResolver(hooked)

	// The hook fires while the first transaction is walking the chain:
	// hop3 and the entry itself get rebound to key-v2 underneath it.
	hooked.hook = func() {
		if _, err := store.Rebind(hop3, keyTarget("key-v2")); err != nil {
			t.Errorf("rebind hop3: %v", err)
		}
		if _, err := store.Rebind(entry, keyTarget("key-v2")); err != nil {
			t.Errorf("rebind entry: %v", err)
		}
	}

	txn, err := resolver.Begin(context.Background(), caller)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	res, err := txn.Resolve(context.Background(), entry)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.FinalFingerprint != fpV1 {
		t.Fatalf("pinned resolution drifted to %x, want original %x",
			res.FinalFingerprint, fpV1)
	}
	if res.EntryVersion != 1 {
		t.Fatalf("entry version = %d, want pinned 1", res.EntryVersion)
	}

	// Re-resolving inside the same transaction must return the identical,
	// cached result even though the live store now points at key-v2.
	pinnedAgain, err := txn.Resolve(context.Background(), entry)
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if pinnedAgain.FinalFingerprint != fpV1 ||
		pinnedAgain.EntryVersion != res.EntryVersion {
		t.Fatalf("per-transaction cache did not pin result: %+v", pinnedAgain)
	}

	// A fresh transaction observes the rebind and must resolve to key-v2.
	nextTxn, err := resolver.Begin(context.Background(), caller)
	if err != nil {
		t.Fatalf("second Begin: %v", err)
	}
	fresh, err := nextTxn.Resolve(context.Background(), entry)
	if err != nil {
		t.Fatalf("fresh Resolve: %v", err)
	}
	if fresh.FinalFingerprint != fpV2 {
		t.Fatalf("new transaction did not observe rebind: got %x want %x",
			fresh.FinalFingerprint, fpV2)
	}
	if fresh.EntryVersion != 2 {
		t.Fatalf("new transaction entry version = %d, want 2", fresh.EntryVersion)
	}
}

func TestConcurrentResolutionsShareSnapshot(t *testing.T) {
	store := NewMemoryStore()
	_, fp := testKey(t, store, acctA, "key-a")

	const refs = 20
	var entries []AliasRef
	for i := 0; i < refs; i++ {
		entry := ref(acctA, regionCN, AliasName("key-"+itoa(i)))
		mustBind(t, store, entry, keyTarget("key-a"))
		entries = append(entries, entry)
	}

	caller := NewCallerIdentity("builder-parallel", []AccountID{acctA})
	txn, err := NewResolver(store).Begin(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, refs)
	for _, entry := range entries {
		wg.Add(1)
		go func(ref AliasRef) {
			defer wg.Done()
			res, err := txn.Resolve(context.Background(), ref)
			if err != nil {
				errs <- err
				return
			}
			if res.FinalFingerprint != fp {
				errs <- errors.New("fingerprint mismatch under concurrency")
			}
		}(entry)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
