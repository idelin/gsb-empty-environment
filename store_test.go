package netsec

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func testMeta(id string) ObjectMeta {
	return ObjectMeta{
		ID:   id,
		Kind: "document",
		Data: []byte("payload"),
	}
}

func TestMemoryStoreVisibilityLifecycle(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	labels := Labels{"owner": "alice", "classification": "secret"}
	digest := labels.Digest()

	token, err := store.CreateObject(ctx, testMeta("obj-1"))
	if err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	if !store.HasPending("obj-1") {
		t.Fatal("object was not staged")
	}
	if _, err := store.Get(ctx, "obj-1"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("Get staged object = %v, want ErrObjectNotFound", err)
	}

	if err := store.PersistLabels(ctx, token, labels, LabelDigest{}); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("PersistLabels with wrong digest = %v, want ErrDigestMismatch", err)
	}
	if _, err := store.Get(ctx, "obj-1"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("Get after rejected labels = %v", err)
	}

	if err := store.PersistLabels(ctx, token, labels, digest); err != nil {
		t.Fatalf("PersistLabels: %v", err)
	}
	if err := store.Publish(ctx, token); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := store.Stats(); got != (StoreStats{Pending: 0, Committed: 1}) {
		t.Fatalf("Stats = %#v", got)
	}

	record, err := store.Get(ctx, "obj-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !record.Labels.Equal(labels) {
		t.Fatalf("labels = %#v, want %#v", record.Labels, labels)
	}
	if record.LabelDigest != digest {
		t.Fatalf("digest = %s, want %s", record.LabelDigest, digest)
	}
	if !bytes.Equal(record.Data, []byte("payload")) {
		t.Fatalf("data = %q", record.Data)
	}

	// Mutating the returned record must not alter stored state.
	record.Labels["owner"] = "mallory"
	record.Data[0] = 'X'
	again, err := store.Get(ctx, "obj-1")
	if err != nil {
		t.Fatalf("Get again: %v", err)
	}
	if again.Labels["owner"] != "alice" || !bytes.Equal(again.Data, []byte("payload")) {
		t.Fatalf("store returned aliased data: labels=%#v data=%q", again.Labels, again.Data)
	}

	// Abort is idempotent and must never remove a committed object.
	if err := store.Abort(ctx, token); err != nil {
		t.Fatalf("Abort committed: %v", err)
	}
	if _, err := store.Get(ctx, "obj-1"); err != nil {
		t.Fatalf("committed object disappeared after Abort: %v", err)
	}
}

func TestMemoryStoreAbortRequiresOwningToken(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()

	token, err := store.CreateObject(ctx, testMeta("obj-1"))
	if err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	if err := store.Abort(ctx, Token("foreign-token")); err != nil {
		t.Fatalf("Abort foreign token: %v", err)
	}
	if !store.HasPending("obj-1") {
		t.Fatal("foreign token aborted a staged object")
	}

	labels := Labels{"owner": "alice"}
	if err := store.PersistLabels(ctx, token, labels, labels.Digest()); err != nil {
		t.Fatalf("PersistLabels: %v", err)
	}
	if err := store.Publish(ctx, token); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := store.Abort(ctx, token); err != nil {
		t.Fatalf("Abort after publish: %v", err)
	}
	if _, err := store.Get(ctx, "obj-1"); err != nil {
		t.Fatalf("Abort removed committed object: %v", err)
	}

	if _, err := store.CreateObject(ctx, testMeta("obj-1")); !errors.Is(err, ErrObjectExists) {
		t.Fatalf("duplicate CreateObject = %v, want ErrObjectExists", err)
	}
}

func TestMemoryStoreDuplicateWhilePending(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	if _, err := store.CreateObject(ctx, testMeta("obj-1")); err != nil {
		t.Fatalf("first CreateObject: %v", err)
	}
	if _, err := store.CreateObject(ctx, testMeta("obj-1")); !errors.Is(err, ErrObjectExists) {
		t.Fatalf("second CreateObject = %v, want ErrObjectExists", err)
	}
}

func TestMemoryStorePublishesEmptyLabelSet(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	labels := Labels{}
	token, err := store.CreateObject(ctx, testMeta("obj-1"))
	if err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	if err := store.PersistLabels(ctx, token, labels, labels.Digest()); err != nil {
		t.Fatalf("PersistLabels: %v", err)
	}
	if err := store.Publish(ctx, token); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	record, err := store.Get(ctx, "obj-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(record.Labels) != 0 {
		t.Fatalf("labels = %#v", record.Labels)
	}
	if record.LabelDigest != labels.Digest() {
		t.Fatalf("digest = %s, want %s", record.LabelDigest, labels.Digest())
	}
}

func TestMemoryStoreFaultsAtEachOperation(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("injected store failure")
	labels := Labels{"owner": "alice"}
	digest := labels.Digest()

	t.Run("create-object", func(t *testing.T) {
		store := NewMemoryStore()
		store.SetFaults(Faults{CreateObject: boom})
		if _, err := store.CreateObject(ctx, testMeta("obj-1")); !errors.Is(err, boom) {
			t.Fatalf("CreateObject = %v, want injected error", err)
		}
		if got := store.Stats(); got.Pending != 0 || got.Committed != 0 {
			t.Fatalf("Stats = %#v", got)
		}
	})

	t.Run("persist-labels", func(t *testing.T) {
		store := NewMemoryStore()
		token, err := store.CreateObject(ctx, testMeta("obj-1"))
		if err != nil {
			t.Fatalf("CreateObject: %v", err)
		}
		store.SetFaults(Faults{PersistLabels: boom})
		if err := store.PersistLabels(ctx, token, labels, digest); !errors.Is(err, boom) {
			t.Fatalf("PersistLabels = %v, want injected error", err)
		}
		if err := store.Publish(ctx, token); !errors.Is(err, ErrIncompleteRecord) {
			t.Fatalf("Publish without labels = %v, want ErrIncompleteRecord", err)
		}
		if _, err := store.Get(ctx, "obj-1"); !errors.Is(err, ErrObjectNotFound) {
			t.Fatalf("Get = %v, want ErrObjectNotFound", err)
		}
		store.ClearFaults()
		if err := store.Abort(ctx, token); err != nil {
			t.Fatalf("Abort: %v", err)
		}
		if got := store.Stats(); got.Pending != 0 || got.Committed != 0 {
			t.Fatalf("Stats = %#v", got)
		}
	})

	t.Run("publish", func(t *testing.T) {
		store := NewMemoryStore()
		token, err := store.CreateObject(ctx, testMeta("obj-1"))
		if err != nil {
			t.Fatalf("CreateObject: %v", err)
		}
		if err := store.PersistLabels(ctx, token, labels, digest); err != nil {
			t.Fatalf("PersistLabels: %v", err)
		}
		store.SetFaults(Faults{Publish: boom})
		if err := store.Publish(ctx, token); !errors.Is(err, boom) {
			t.Fatalf("Publish = %v, want injected error", err)
		}
		if _, err := store.Get(ctx, "obj-1"); !errors.Is(err, ErrObjectNotFound) {
			t.Fatalf("Get = %v, want ErrObjectNotFound", err)
		}
		store.ClearFaults()
		if err := store.Abort(ctx, token); err != nil {
			t.Fatalf("Abort: %v", err)
		}
		if got := store.Stats(); got.Pending != 0 || got.Committed != 0 {
			t.Fatalf("Stats = %#v", got)
		}
	})

	t.Run("abort", func(t *testing.T) {
		store := NewMemoryStore()
		token, err := store.CreateObject(ctx, testMeta("obj-1"))
		if err != nil {
			t.Fatalf("CreateObject: %v", err)
		}
		store.SetFaults(Faults{Abort: boom})
		if err := store.Abort(ctx, token); !errors.Is(err, boom) {
			t.Fatalf("Abort = %v, want injected error", err)
		}
		if !store.HasPending("obj-1") {
			t.Fatal("failed Abort removed the staged object")
		}
		if _, err := store.Get(ctx, "obj-1"); !errors.Is(err, ErrObjectNotFound) {
			t.Fatalf("Get = %v, want ErrObjectNotFound", err)
		}
		store.ClearFaults()
		if err := store.Abort(ctx, token); err != nil {
			t.Fatalf("Abort after clearing faults: %v", err)
		}
		if store.HasPending("obj-1") {
			t.Fatal("staged object survived successful Abort")
		}
	})
}

func TestMemoryStoreFailNextIsConsumed(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	boom := errors.New("one shot")
	store.FailNext(OpCreateObject, boom)
	if _, err := store.CreateObject(ctx, testMeta("obj-1")); !errors.Is(err, boom) {
		t.Fatalf("first CreateObject = %v, want one-shot error", err)
	}
	if _, err := store.CreateObject(ctx, testMeta("obj-1")); err != nil {
		t.Fatalf("second CreateObject = %v, want success", err)
	}
}

func TestMemoryStoreRejectsInvalidMetadata(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	for _, meta := range []ObjectMeta{
		{Kind: "document"},
		{ID: "x"},
		{ID: "x", Kind: "document", ParentID: "x"},
	} {
		if _, err := store.CreateObject(ctx, meta); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("CreateObject(%#v) = %v, want ErrInvalidRequest", meta, err)
		}
	}
}
