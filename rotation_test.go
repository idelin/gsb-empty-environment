package netsec

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func mustLoad(t *testing.T, store MappingStore) Mapping {
	t.Helper()
	mapping, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return mapping
}

func assertMappingEqual(t *testing.T, got, want Mapping) {
	t.Helper()
	if !reflect.DeepEqual(got.Entries, want.Entries) {
		t.Fatalf("mapping = %#v, want %#v", got.Entries, want.Entries)
	}
}

func TestRotateRewritesKeysAndCircularReferences(t *testing.T) {
	const (
		alice = "alice@example.com"
		bob   = "bob@example.com"
	)
	p := testPseudonymizer(t)
	store := NewMemoryStore()
	ctx := context.Background()

	oldAlice := mustToken(t, p, "tenant-a", "email", alice, 1)
	oldBob := mustToken(t, p, "tenant-a", "email", bob, 1)
	oldOtherTenantAlice := mustToken(t, p, "tenant-b", "email", alice, 1)

	batch := Batch{
		From: 1,
		To:   2,
		Subjects: []Subject{
			{Tenant: "tenant-a", FieldType: "email", Value: alice},
			{Tenant: "tenant-a", FieldType: "email", Value: bob},
			{Tenant: "tenant-b", FieldType: "email", Value: alice},
		},
		Events: []Event{
			{ID: "alice", Key: oldAlice, References: []Token{oldBob}},
			{ID: "bob", Key: oldBob, References: []Token{oldAlice}},
			{ID: "tenant-b", Key: oldOtherTenantAlice, References: []Token{oldOtherTenantAlice}},
		},
	}

	result, err := p.Rotate(ctx, store, batch)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	newAlice := mustToken(t, p, "tenant-a", "email", alice, 2)
	newBob := mustToken(t, p, "tenant-a", "email", bob, 2)
	newOtherTenantAlice := mustToken(t, p, "tenant-b", "email", alice, 2)

	if result.Events[0].Key != newAlice {
		t.Fatalf("alice event key = %q, want %q", result.Events[0].Key, newAlice)
	}
	if got := result.Events[0].References[0]; got.Missing || got.Token != newBob || got.Original != oldBob {
		t.Fatalf("alice -> bob reference = %#v, want token %q", got, newBob)
	}
	if result.Events[1].Key != newBob {
		t.Fatalf("bob event key = %q, want %q", result.Events[1].Key, newBob)
	}
	if got := result.Events[1].References[0]; got.Missing || got.Token != newAlice || got.Original != oldAlice {
		t.Fatalf("bob -> alice reference = %#v, want token %q", got, newAlice)
	}
	if result.Events[2].Key != newOtherTenantAlice {
		t.Fatalf("tenant-b event key = %q, want %q", result.Events[2].Key, newOtherTenantAlice)
	}
	if got := result.Events[2].References[0]; got.Missing || got.Token != newOtherTenantAlice {
		t.Fatalf("tenant-b self reference = %#v, want token %q", got, newOtherTenantAlice)
	}
	if newAlice == newOtherTenantAlice {
		t.Fatal("rotation merged the same email across tenants")
	}

	published := mustLoad(t, store)
	want := Mapping{Entries: map[Token]Token{
		oldAlice:            newAlice,
		oldBob:              newBob,
		oldOtherTenantAlice: newOtherTenantAlice,
	}}
	assertMappingEqual(t, published, want)

	for from, to := range published.Entries {
		row := string(from) + " " + string(to)
		for _, raw := range []string{alice, bob, "tenant-a", "tenant-b"} {
			if strings.Contains(row, raw) {
				t.Fatalf("mapping row leaked raw value %q: %q", raw, row)
			}
		}
	}
	dump := fmt.Sprintf("%+v", result)
	for _, raw := range []string{alice, bob} {
		if strings.Contains(dump, raw) {
			t.Fatalf("rotation result leaked raw value %q", raw)
		}
	}
}

func TestRotateMarksUnresolvedReferencesMissing(t *testing.T) {
	p := testPseudonymizer(t)
	store := NewMemoryStore()

	oldAlice := mustToken(t, p, "tenant-a", "email", "alice@example.com", 1)
	oldGhost := mustToken(t, p, "tenant-a", "email", "ghost@example.com", 1)
	alreadyNewGhost := mustToken(t, p, "tenant-a", "email", "ghost@example.com", 2)

	result, err := p.Rotate(context.Background(), store, Batch{
		From: 1,
		To:   2,
		Subjects: []Subject{
			{Tenant: "tenant-a", FieldType: "email", Value: "alice@example.com"},
		},
		Events: []Event{{
			ID:         "alice",
			Key:        oldAlice,
			References: []Token{oldGhost, alreadyNewGhost},
		}},
	})
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	refs := result.Events[0].References
	if !refs[0].Missing || refs[0].Token != "" || refs[0].Original != oldGhost {
		t.Fatalf("unresolved reference = %#v, want explicit missing with original %q", refs[0], oldGhost)
	}
	if refs[1].Missing || refs[1].Token != alreadyNewGhost || refs[1].Original != alreadyNewGhost {
		t.Fatalf("target-generation reference = %#v, want resolved %q", refs[1], alreadyNewGhost)
	}
	if got := mustLoad(t, store); got.Len() != 1 {
		t.Fatalf("mapping has %d rows, want 1: %#v", got.Len(), got.Entries)
	}
}

func TestRotateRejectsUnresolvedEventKeyWithoutPublishing(t *testing.T) {
	p := testPseudonymizer(t)
	store := NewMemoryStore()
	ghost := mustToken(t, p, "tenant-a", "email", "ghost@example.com", 1)

	_, err := p.Rotate(context.Background(), store, Batch{
		From: 1,
		To:   2,
		Subjects: []Subject{
			{Tenant: "tenant-a", FieldType: "email", Value: "alice@example.com"},
		},
		Events: []Event{{ID: "ghost", Key: ghost}},
	})
	if !errors.Is(err, ErrUnresolvedKey) {
		t.Fatalf("Rotate error = %v, want ErrUnresolvedKey", err)
	}
	if got := mustLoad(t, store); got.Len() != 0 {
		t.Fatalf("failed batch published %d mapping rows", got.Len())
	}
}

func TestRotateValidationFailureLeavesStoreUnchanged(t *testing.T) {
	p := testPseudonymizer(t)
	store := NewMemoryStore()
	ctx := context.Background()

	if _, err := p.Rotate(ctx, store, Batch{
		From: 1,
		To:   2,
		Subjects: []Subject{
			{Tenant: "tenant-a", FieldType: "email", Value: "alice@example.com"},
		},
	}); err != nil {
		t.Fatalf("seed Rotate: %v", err)
	}
	before := mustLoad(t, store)

	if _, err := p.Rotate(ctx, store, Batch{
		From: 2,
		To:   3,
		Subjects: []Subject{
			{Tenant: "tenant-a", FieldType: "email", Value: ""},
		},
	}); !errors.Is(err, ErrEmptyValue) {
		t.Fatalf("empty subject error = %v, want ErrEmptyValue", err)
	}
	assertMappingEqual(t, mustLoad(t, store), before)

	if _, err := p.Rotate(ctx, store, Batch{
		From: 2,
		To:   3,
		Subjects: []Subject{
			{Tenant: "tenant-a", FieldType: "email", Value: "alice@example.com"},
		},
		Events: []Event{{
			ID:         "raw-reference",
			Key:        mustToken(t, p, "tenant-a", "email", "alice@example.com", 2),
			References: []Token{Token("alice@example.com")},
		}},
	}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("raw reference error = %v, want ErrInvalidToken", err)
	}
	assertMappingEqual(t, mustLoad(t, store), before)
}

func TestRotateRejectsConflictingMapping(t *testing.T) {
	p := testPseudonymizer(t)
	store := NewMemoryStore()

	oldAlice := mustToken(t, p, "tenant-a", "email", "alice@example.com", 1)
	conflicting := mustToken(t, p, "tenant-a", "email", "someone-else@example.com", 2)
	if err := store.Publish(context.Background(), Mapping{Entries: map[Token]Token{oldAlice: conflicting}}); err != nil {
		t.Fatalf("seed mapping: %v", err)
	}

	_, err := p.Rotate(context.Background(), store, Batch{
		From: 1,
		To:   2,
		Subjects: []Subject{
			{Tenant: "tenant-a", FieldType: "email", Value: "alice@example.com"},
		},
	})
	if !errors.Is(err, ErrMappingConflict) {
		t.Fatalf("Rotate error = %v, want ErrMappingConflict", err)
	}
	assertMappingEqual(t, mustLoad(t, store), Mapping{Entries: map[Token]Token{oldAlice: conflicting}})
}

func TestRotateFollowsChainsAndTerminatesCycles(t *testing.T) {
	p := testPseudonymizer(t)
	store := NewMemoryStore()
	ctx := context.Background()
	subject := Subject{Tenant: "tenant-a", FieldType: "email", Value: "alice@example.com"}

	if _, err := p.Rotate(ctx, store, Batch{From: 1, To: 2, Subjects: []Subject{subject}}); err != nil {
		t.Fatalf("first Rotate: %v", err)
	}

	oldAlice := mustToken(t, p, "tenant-a", "email", "alice@example.com", 1)
	midAlice := mustToken(t, p, "tenant-a", "email", "alice@example.com", 2)
	newAlice := mustToken(t, p, "tenant-a", "email", "alice@example.com", 3)
	cycleA := mustToken(t, p, "tenant-cycle", "email", "a", 1)
	cycleB := mustToken(t, p, "tenant-cycle", "email", "b", 1)

	snapshot := mustLoad(t, store)
	snapshot.Entries[cycleA] = cycleB
	snapshot.Entries[cycleB] = cycleA
	if err := store.Publish(ctx, snapshot); err != nil {
		t.Fatalf("publish cycle: %v", err)
	}

	result, err := p.Rotate(ctx, store, Batch{
		From:     2,
		To:       3,
		Subjects: []Subject{subject},
		Events: []Event{{
			ID:         "alice",
			Key:        midAlice,
			References: []Token{oldAlice, cycleA},
		}},
	})
	if err != nil {
		t.Fatalf("second Rotate: %v", err)
	}

	if result.Events[0].Key != newAlice {
		t.Fatalf("event key = %q, want %q", result.Events[0].Key, newAlice)
	}
	if ref := result.Events[0].References[0]; ref.Missing || ref.Token != newAlice || ref.Original != oldAlice {
		t.Fatalf("transitive reference = %#v, want %q", ref, newAlice)
	}
	if ref := result.Events[0].References[1]; !ref.Missing || ref.Token != "" || ref.Original != cycleA {
		t.Fatalf("cyclic reference = %#v, want explicit missing", ref)
	}

	published := mustLoad(t, store)
	if published.Entries[oldAlice] != midAlice {
		t.Fatalf("first-generation mapping lost: %#v", published.Entries)
	}
	if published.Entries[midAlice] != newAlice {
		t.Fatalf("second-generation mapping missing: %#v", published.Entries)
	}
	if published.Entries[cycleA] != cycleB || published.Entries[cycleB] != cycleA {
		t.Fatalf("cycle rows changed unexpectedly: %#v", published.Entries)
	}
}

type partialFailureStore struct {
	inner     *MemoryStore
	failAfter int
	attempted int
}

func (s *partialFailureStore) Load(ctx context.Context) (Mapping, error) {
	return s.inner.Load(ctx)
}

func (s *partialFailureStore) Publish(ctx context.Context, mapping Mapping) error {
	staged := make(map[Token]Token, len(mapping.Entries))
	for from, to := range mapping.Entries {
		if s.attempted == s.failAfter {
			return errors.New("simulated storage write failure")
		}
		staged[from] = to
		s.attempted++
	}
	return s.inner.Publish(ctx, Mapping{Entries: staged})
}

func TestRotateMidStorageFailureKeepsPreviousSnapshot(t *testing.T) {
	p := testPseudonymizer(t)
	ctx := context.Background()
	store := &partialFailureStore{inner: NewMemoryStore(), failAfter: -1}

	if _, err := p.Rotate(ctx, store, Batch{
		From: 1,
		To:   2,
		Subjects: []Subject{
			{Tenant: "tenant-a", FieldType: "email", Value: "alice@example.com"},
		},
	}); err != nil {
		t.Fatalf("seed Rotate: %v", err)
	}
	before := mustLoad(t, store.inner)

	store.failAfter = 1
	store.attempted = 0
	_, err := p.Rotate(ctx, store, Batch{
		From: 2,
		To:   3,
		Subjects: []Subject{
			{Tenant: "tenant-a", FieldType: "email", Value: "alice@example.com"},
			{Tenant: "tenant-a", FieldType: "email", Value: "bob@example.com"},
			{Tenant: "tenant-a", FieldType: "email", Value: "carol@example.com"},
		},
		Events: []Event{{
			ID:  "alice",
			Key: mustToken(t, p, "tenant-a", "email", "alice@example.com", 2),
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "simulated storage write failure") {
		t.Fatalf("Rotate error = %v, want simulated storage failure", err)
	}
	if store.attempted != 1 {
		t.Fatalf("storage attempted %d writes before failure, want 1", store.attempted)
	}
	assertMappingEqual(t, mustLoad(t, store.inner), before)
}

func TestMemoryStoreRejectsRawSecrets(t *testing.T) {
	p := testPseudonymizer(t)
	valid := mustToken(t, p, "tenant-a", "email", "alice@example.com", 1)
	store := NewMemoryStore()

	cases := []Mapping{
		{Entries: map[Token]Token{Token("alice@example.com"): valid}},
		{Entries: map[Token]Token{valid: Token("alice@example.com")}},
	}
	for _, mapping := range cases {
		if err := store.Publish(context.Background(), mapping); !errors.Is(err, ErrInvalidMapping) {
			t.Fatalf("Publish(%#v) error = %v, want ErrInvalidMapping", mapping.Entries, err)
		}
		if got := mustLoad(t, store); got.Len() != 0 {
			t.Fatalf("rejected mapping was stored: %#v", got.Entries)
		}
	}
}

func TestMemoryStoreCopiesSnapshots(t *testing.T) {
	p := testPseudonymizer(t)
	oldToken := mustToken(t, p, "tenant-a", "email", "alice@example.com", 1)
	newToken := mustToken(t, p, "tenant-a", "email", "alice@example.com", 2)
	otherToken := mustToken(t, p, "tenant-a", "email", "bob@example.com", 2)
	store := NewMemoryStore()
	ctx := context.Background()

	input := Mapping{Entries: map[Token]Token{oldToken: newToken}}
	if err := store.Publish(ctx, input); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	input.Entries[oldToken] = otherToken
	if got := mustLoad(t, store).Entries[oldToken]; got != newToken {
		t.Fatalf("caller mutation changed store: %q", got)
	}

	snapshot := mustLoad(t, store)
	snapshot.Entries[oldToken] = otherToken
	if got := mustLoad(t, store).Entries[oldToken]; got != newToken {
		t.Fatalf("snapshot mutation changed store: %q", got)
	}
}

func TestRotateHonorsCanceledContext(t *testing.T) {
	p := testPseudonymizer(t)
	store := NewMemoryStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := p.Rotate(ctx, store, Batch{From: 1, To: 2}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Rotate error = %v, want context.Canceled", err)
	}
	if got := mustLoad(t, store); got.Len() != 0 {
		t.Fatalf("canceled rotation published %d rows", got.Len())
	}
}

type countingStore struct {
	inner     *MemoryStore
	loads     int
	publishes int
}

func (s *countingStore) Load(ctx context.Context) (Mapping, error) {
	s.loads++
	return s.inner.Load(ctx)
}

func (s *countingStore) Publish(ctx context.Context, mapping Mapping) error {
	s.publishes++
	return s.inner.Publish(ctx, mapping)
}

func TestRotatePublishesOnceOnlyAfterValidation(t *testing.T) {
	p := testPseudonymizer(t)
	store := &countingStore{inner: NewMemoryStore()}
	ctx := context.Background()
	subject := Subject{Tenant: "tenant-a", FieldType: "email", Value: "alice@example.com"}

	if _, err := p.Rotate(ctx, store, Batch{
		From:     1,
		To:       2,
		Subjects: []Subject{subject},
		Events: []Event{{
			ID:  "alice",
			Key: mustToken(t, p, "tenant-a", "email", "alice@example.com", 1),
		}},
	}); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if store.loads != 1 || store.publishes != 1 {
		t.Fatalf("successful batch calls = %d loads, %d publishes; want 1, 1", store.loads, store.publishes)
	}

	store.loads = 0
	store.publishes = 0
	if _, err := p.Rotate(ctx, store, Batch{
		From: 2,
		To:   3,
		Subjects: []Subject{
			{Tenant: "tenant-a", FieldType: "email", Value: ""},
		},
	}); !errors.Is(err, ErrEmptyValue) {
		t.Fatalf("invalid batch error = %v, want ErrEmptyValue", err)
	}
	if store.publishes != 0 {
		t.Fatalf("invalid batch called Publish %d times", store.publishes)
	}
}
