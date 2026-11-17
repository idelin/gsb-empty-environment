package netsec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

func TestAssignStableAndDistinct(t *testing.T) {
	a := newTestAllocator(t, nil, DefaultInitialCodeLength)
	ctx := context.Background()

	alice, err := a.Assign(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	aliceAgain, err := a.Assign(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := a.Assign(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}

	if alice != aliceAgain {
		t.Fatalf("unstable code: %q then %q", alice, aliceAgain)
	}
	if len(alice) != DefaultInitialCodeLength {
		t.Fatalf("code length = %d, want %d", len(alice), DefaultInitialCodeLength)
	}
	if alice == bob {
		t.Fatalf("different people share code %q", alice)
	}
}

func TestDeliberateCollisionDoesNotMerge(t *testing.T) {
	mac := digestMapMAC(map[string][]byte{
		"alice": collidingDigest(0x01, 0x01, 0x01),
		"bob":   collidingDigest(0x02, 0x01, 0x01),
		"carol": collidingDigest(0x02, 0x03, 0x01),
	})
	a := newTestAllocator(t, mac, 4)
	ctx := context.Background()

	alice, err := a.Assign(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := a.Assign(ctx, "bob")
	if err != nil {
		t.Fatal(err)
	}
	carol, err := a.Assign(ctx, "carol")
	if err != nil {
		t.Fatal(err)
	}

	if len(alice) != 4 {
		t.Fatalf("first code length = %d, want 4", len(alice))
	}
	if len(bob) <= len(alice) || !strings.HasPrefix(bob, alice) {
		t.Fatalf("colliding code %q did not extend owner %q", bob, alice)
	}
	if len(carol) <= len(bob) || !strings.HasPrefix(carol, bob) {
		t.Fatalf("second colliding code %q did not extend %q", carol, bob)
	}

	// Codes already handed to event writers must never change.
	if got, err := a.Assign(ctx, "alice"); err != nil || got != alice {
		t.Fatalf("alice code changed after collisions: %q, %v", got, err)
	}
	if got, err := a.Assign(ctx, "bob"); err != nil || got != bob {
		t.Fatalf("bob code changed after later collision: %q, %v", got, err)
	}

	events := []struct {
		code   string
		person string
		want   string
	}{
		{alice, "alice", alice},
		{alice, "bob", bob},   // bob's old event carries the original short code
		{bob, "carol", carol}, // carol's event predates its extension
		{bob, "bob", bob},
	}
	resolved := make(map[string]string)
	for _, event := range events {
		got, err := a.Resolve(ctx, event.code, event.person)
		if err != nil {
			t.Fatalf("resolve(%q, %q): %v", event.code, event.person, err)
		}
		if got != event.want {
			t.Fatalf("resolve(%q, %q) = %q, want %q", event.code, event.person, got, event.want)
		}
		if prior, ok := resolved[event.person]; ok && prior != got {
			t.Fatalf("%s mapped to both %q and %q", event.person, prior, got)
		}
		resolved[event.person] = got
	}
	if resolved["alice"] == resolved["bob"] || resolved["bob"] == resolved["carol"] {
		t.Fatalf("colliding people were merged: %#v", resolved)
	}

	if _, err := a.Resolve(ctx, bob, "alice"); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("cross-identity resolve error = %v, want ErrIdentityMismatch", err)
	}
	if _, err := a.Resolve(ctx, alice, "mallory"); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("unknown identity resolve error = %v, want ErrIdentityMismatch", err)
	}
	if _, err := a.Resolve(ctx, "ZZZZZZ", "alice"); !errors.Is(err, ErrUnknownCode) {
		t.Fatalf("unknown code resolve error = %v, want ErrUnknownCode", err)
	}
}

func TestCandidatesExposeAmbiguityWithoutMerging(t *testing.T) {
	mac := digestMapMAC(map[string][]byte{
		"alice": collidingDigest(0x01, 0x01, 0x01),
		"bob":   collidingDigest(0x02, 0x01, 0x01),
		"carol": collidingDigest(0x02, 0x03, 0x01),
	})
	a := newTestAllocator(t, mac, 4)
	ctx := context.Background()

	alice := assign(t, a, "alice")
	bob := assign(t, a, "bob")
	carol := assign(t, a, "carol")

	got, err := a.Candidates(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{alice, bob, carol}
	sort.Strings(want)
	if !equalStrings(got, want) {
		t.Fatalf("candidates(%q) = %#v, want %#v", alice, got, want)
	}

	got, err = a.Candidates(ctx, bob)
	if err != nil {
		t.Fatal(err)
	}
	want = []string{bob, carol}
	sort.Strings(want)
	if !equalStrings(got, want) {
		t.Fatalf("candidates(%q) = %#v, want %#v", bob, got, want)
	}

	if _, err := a.Candidates(ctx, "NOPE"); !errors.Is(err, ErrUnknownCode) {
		t.Fatalf("unknown candidates error = %v, want ErrUnknownCode", err)
	}
}

func TestRestartRecoveryKeepsCodesAndAliases(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/pseudonyms.json"
	mac := digestMapMAC(map[string][]byte{
		"alice": collidingDigest(0x01, 0x01, 0x01),
		"bob":   collidingDigest(0x02, 0x01, 0x01),
		"carol": collidingDigest(0x02, 0x03, 0x01),
		"dave":  collidingDigest(0x02, 0x03, 0x04),
	})

	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	a := newTestAllocatorWithStore(t, mac, 4, store)
	alice := assign(t, a, "alice")
	bob := assign(t, a, "bob")
	carol := assign(t, a, "carol")

	// A fresh allocator simulates process restart.
	store2, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	restarted := newTestAllocatorWithStore(t, mac, 4, store2)

	for person, want := range map[string]string{"alice": alice, "bob": bob, "carol": carol} {
		got, err := restarted.Assign(ctx, person)
		if err != nil {
			t.Fatalf("assign %s after restart: %v", person, err)
		}
		if got != want {
			t.Fatalf("%s code after restart = %q, want %q", person, got, want)
		}
	}
	if got, err := restarted.Resolve(ctx, alice, "bob"); err != nil || got != bob {
		t.Fatalf("bob alias after restart = %q, %v; want %q", got, err, bob)
	}
	if got, err := restarted.Resolve(ctx, bob, "carol"); err != nil || got != carol {
		t.Fatalf("carol alias after restart = %q, %v; want %q", got, err, carol)
	}

	// Extending the collision bucket after restart must not rewrite old codes.
	dave := assign(t, restarted, "dave")
	if len(dave) <= len(carol) || !strings.HasPrefix(dave, carol) {
		t.Fatalf("dave code %q did not extend carol %q", dave, carol)
	}
	for person, want := range map[string]string{"alice": alice, "bob": bob, "carol": carol} {
		got, err := restarted.Assign(ctx, person)
		if err != nil {
			t.Fatalf("reassign %s: %v", person, err)
		}
		if got != want {
			t.Fatalf("%s code changed after restart extension: %q, want %q", person, got, want)
		}
	}

	raw, err := store2.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var snap snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("snapshot is not valid JSON: %v", err)
	}
	if snap.Version != snapshotVersion || len(snap.Entries) != 4 {
		t.Fatalf("unexpected snapshot: %#v", snap)
	}
	entriesByCode := make(map[string]snapshotEntry, len(snap.Entries))
	for _, entry := range snap.Entries {
		entriesByCode[entry.Code] = entry
	}
	if got := entriesByCode[bob].Aliases; !equalStrings(got, []string{alice}) {
		t.Fatalf("bob persisted aliases = %#v, want [%q]", got, alice)
	}
	if got := entriesByCode[carol].Aliases; !equalStrings(got, []string{alice, bob}) {
		t.Fatalf("carol persisted aliases = %#v, want [%q %q]", got, alice, bob)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("snapshot permissions = %o, want 600", perm)
		}
	}
}

func TestFailedSaveRollsBackAllocation(t *testing.T) {
	store := &failingStore{}
	mac := digestMapMAC(map[string][]byte{
		"alice": collidingDigest(0x01, 0x01, 0x01),
		"bob":   collidingDigest(0x02, 0x01, 0x01),
	})
	a := newTestAllocatorWithStore(t, mac, 4, store)
	ctx := context.Background()

	alice := assign(t, a, "alice")
	store.setFailing(true)

	// An existing assignment does not need a write and remains usable.
	if got, err := a.Assign(ctx, "alice"); err != nil || got != alice {
		t.Fatalf("existing assignment during store outage = %q, %v", got, err)
	}
	if _, err := a.Assign(ctx, "bob"); err == nil {
		t.Fatal("assignment succeeded despite failed save")
	} else if !strings.Contains(err.Error(), "save store") {
		t.Fatalf("unexpected save error: %v", err)
	}

	store.setFailing(false)
	bob := assign(t, a, "bob")
	if len(bob) != 5 || !strings.HasPrefix(bob, alice) {
		t.Fatalf("bob code = %q, want one-character extension of %q", bob, alice)
	}
	if got, err := a.Resolve(ctx, alice, "bob"); err != nil || got != bob {
		t.Fatalf("bob alias after failed save = %q, %v; want %q", got, err, bob)
	}

	restarted := newTestAllocatorWithStore(t, mac, 4, store)
	if got, err := restarted.Assign(ctx, "bob"); err != nil || got != bob {
		t.Fatalf("bob after restart = %q, %v; want %q", got, err, bob)
	}
}

func TestConcurrentAssignmentsAreAtomicAndUnique(t *testing.T) {
	store := NewMemoryStore()
	a := newTestAllocatorWithStore(t, nil, 6, store)
	ctx := context.Background()

	const (
		people  = 24
		workers = 12
	)
	results := make([][]string, people)
	errs := make(chan error, people*workers)
	var resultsMu sync.Mutex
	var wg sync.WaitGroup
	for person := 0; person < people; person++ {
		for worker := 0; worker < workers; worker++ {
			wg.Add(1)
			go func(person int) {
				defer wg.Done()
				code, err := a.Assign(ctx, personID(person))
				if err != nil {
					errs <- err
					return
				}
				resultsMu.Lock()
				results[person] = append(results[person], code)
				resultsMu.Unlock()
			}(person)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	seen := make(map[string]string, people)
	for person := 0; person < people; person++ {
		if len(results[person]) != workers {
			t.Fatalf("person %d got %d results", person, len(results[person]))
		}
		first := results[person][0]
		for _, code := range results[person][1:] {
			if code != first {
				t.Fatalf("person %d got unstable codes %q and %q", person, first, code)
			}
		}
		if other, exists := seen[first]; exists {
			t.Fatalf("code %q assigned to %s and person %d", first, other, person)
		}
		seen[first] = personID(person)
	}

	restarted := newTestAllocatorWithStore(t, nil, 6, store)
	for person := 0; person < people; person++ {
		code, err := restarted.Assign(ctx, personID(person))
		if err != nil {
			t.Fatal(err)
		}
		if code != results[person][0] {
			t.Fatalf("person %d code after restart = %q, want %q", person, code, results[person][0])
		}
	}
}

func TestConcurrentCollisionKeepsIdentitiesSeparate(t *testing.T) {
	mac := digestMapMAC(map[string][]byte{
		"alice": collidingDigest(0x01, 0x01, 0x01),
		"bob":   collidingDigest(0x02, 0x01, 0x01),
		"carol": collidingDigest(0x02, 0x03, 0x01),
	})
	a := newTestAllocator(t, mac, 4)
	ctx := context.Background()

	const workers = 32
	people := []string{"alice", "bob", "carol"}
	codes := make(map[string][]string)
	var mu sync.Mutex
	errs := make(chan error, len(people)*workers)
	var wg sync.WaitGroup
	for _, person := range people {
		for worker := 0; worker < workers; worker++ {
			wg.Add(1)
			go func(person string) {
				defer wg.Done()
				code, err := a.Assign(ctx, person)
				if err != nil {
					errs <- err
					return
				}
				mu.Lock()
				codes[person] = append(codes[person], code)
				mu.Unlock()
			}(person)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	distinct := make(map[string]string, len(people))
	for _, person := range people {
		first := codes[person][0]
		for _, code := range codes[person] {
			if code != first {
				t.Fatalf("%s got unstable codes %q and %q", person, first, code)
			}
		}
		if other, exists := distinct[first]; exists {
			t.Fatalf("%s and %s share %q", other, person, first)
		}
		distinct[first] = person
	}
}

func TestIncompatibleAndCorruptStores(t *testing.T) {
	ctx := context.Background()
	path := t.TempDir() + "/pseudonyms.json"
	store, _ := NewFileStore(path)
	a := newTestAllocatorWithStore(t, nil, 8, store)
	assign(t, a, "alice")

	wrongKey := []byte("abcdef0123456789abcdef0123456789")
	if _, err := NewAllocator(Options{Key: wrongKey, Store: store, InitialCodeLength: 8}); !errors.Is(err, ErrIncompatibleStore) {
		t.Fatalf("wrong-key open error = %v, want ErrIncompatibleStore", err)
	}
	if _, err := NewAllocator(Options{Key: testKey, Store: store, InitialCodeLength: 9}); !errors.Is(err, ErrIncompatibleStore) {
		t.Fatalf("wrong-length open error = %v, want ErrIncompatibleStore", err)
	}

	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewAllocator(Options{Key: testKey, Store: store, InitialCodeLength: 8}); !errors.Is(err, ErrCorruptStore) {
		t.Fatalf("corrupt-store open error = %v, want ErrCorruptStore", err)
	}

	raw, err := json.Marshal(snapshot{
		Version:           snapshotVersion,
		KeyID:             keyCheckValue(testKey),
		InitialCodeLength: 8,
		Entries: []snapshotEntry{{
			Identity: strings.Repeat("00", 32),
			Code:     "AAAAAAAB",
			Length:   8,
			Aliases:  nil,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewAllocator(Options{Key: testKey, Store: store, InitialCodeLength: 8}); !errors.Is(err, ErrCorruptStore) {
		t.Fatalf("inconsistent-code open error = %v, want ErrCorruptStore", err)
	}
	_ = ctx
}

func TestCanceledContextDoesNotAllocate(t *testing.T) {
	store := NewMemoryStore()
	a := newTestAllocatorWithStore(t, nil, 6, store)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := a.Assign(ctx, "alice"); !errors.Is(err, context.Canceled) {
		t.Fatalf("assign with canceled context error = %v", err)
	}
	raw, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 0 {
		t.Fatalf("canceled assignment wrote a snapshot: %s", raw)
	}
}

func newTestAllocator(t *testing.T, mac MACFunc, initial int) *Allocator {
	t.Helper()
	return newTestAllocatorWithStore(t, mac, initial, NewMemoryStore())
}

func newTestAllocatorWithStore(t *testing.T, mac MACFunc, initial int, store Store) *Allocator {
	t.Helper()
	a, err := NewAllocator(Options{
		Key:               testKey,
		Store:             store,
		InitialCodeLength: initial,
		MAC:               mac,
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func assign(t *testing.T, a *Allocator, person string) string {
	t.Helper()
	code, err := a.Assign(context.Background(), person)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func digestMapMAC(digests map[string][]byte) MACFunc {
	return func(identifier []byte) []byte {
		if digest, ok := digests[string(identifier)]; ok {
			return append([]byte(nil), digest...)
		}
		sum := sha256.Sum256(identifier)
		return sum[:]
	}
}

func collidingDigest(byte4, byte5, byte6 byte) []byte {
	digest := make([]byte, 32)
	for i := range digest {
		digest[i] = 0x42
	}
	digest[4] = byte4
	digest[5] = byte5
	digest[6] = byte6
	return digest
}

func personID(n int) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	buf := []byte("person-")
	buf = append(buf, digits[n/36], digits[n%36])
	return string(buf)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type failingStore struct {
	mu      sync.Mutex
	data    []byte
	failing bool
}

func (s *failingStore) setFailing(failing bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failing = failing
}

func (s *failingStore) Load(context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.data...), nil
}

func (s *failingStore) Save(_ context.Context, snapshot []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failing {
		return errors.New("injected store failure")
	}
	s.data = append(s.data[:0], snapshot...)
	return nil
}
