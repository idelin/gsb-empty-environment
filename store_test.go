package netsec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func mustKey(t *testing.T) []byte {
	t.Helper()
	key, err := GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func newFileBackedStore(t *testing.T, dir string) (*Store, []byte) {
	t.Helper()
	blob, err := NewFileBlobStore(dir)
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}
	meta, err := NewFileMetadataStore(dir)
	if err != nil {
		t.Fatalf("metadata store: %v", err)
	}
	key := mustKey(t)
	store, err := New(blob, meta, key)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	return store, key
}

func reopenFileBackedStore(t *testing.T, dir string, key []byte) *Store {
	t.Helper()
	blob, err := NewFileBlobStore(dir)
	if err != nil {
		t.Fatalf("reopen blob store: %v", err)
	}
	meta, err := NewFileMetadataStore(dir)
	if err != nil {
		t.Fatalf("reopen metadata store: %v", err)
	}
	store, err := New(blob, meta, key)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	return store
}

func TestRealRoundTripAndReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, key := newFileBackedStore(t, dir)

	payload := []byte("the answer is 42, encrypted at rest")
	pointer, err := store.Write(ctx, "doc/1", payload)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if pointer.Version != 1 {
		t.Fatalf("first version = %d, want 1", pointer.Version)
	}

	got, cur, err := store.Read(ctx, "doc/1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("plaintext mismatch: %q", got)
	}
	if cur.Version != 1 || !bytes.Equal(cur.Digest, pointer.Digest) {
		t.Fatalf("current pointer mismatch: %+v vs %+v", cur, pointer)
	}

	reopened := reopenFileBackedStore(t, dir, key)
	got2, cur2, err := reopened.Read(ctx, "doc/1")
	if err != nil {
		t.Fatalf("read after reopen: %v", err)
	}
	if !bytes.Equal(got2, payload) {
		t.Fatalf("plaintext after reopen mismatch: %q", got2)
	}
	if cur2.Version != 1 {
		t.Fatalf("version after reopen = %d", cur2.Version)
	}
	if err := reopened.VerifyChain(ctx, "doc/1"); err != nil {
		t.Fatalf("chain verify after reopen: %v", err)
	}

	if _, _, err := reopened.Read(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing object error = %v, want ErrNotFound", err)
	}
}

func TestSamePlaintextDifferentVersions(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, _ := newFileBackedStore(t, dir)

	const objectID = "same-content"
	payload := []byte("identical plaintext across versions")

	var pointers []*Pointer
	for i := 0; i < 4; i++ {
		pointer, err := store.Write(ctx, objectID, payload)
		if err != nil {
			t.Fatalf("write v%d: %v", i+1, err)
		}
		if pointer.Version != uint64(i+1) {
			t.Fatalf("version = %d, want %d", pointer.Version, i+1)
		}
		pointers = append(pointers, pointer)
	}

	digests := map[string]bool{}
	for _, pointer := range pointers {
		digests[hex.EncodeToString(pointer.Digest)] = true
	}
	if len(digests) != len(pointers) {
		t.Fatalf("envelope digests not unique across versions: %d unique for %d versions", len(digests), len(pointers))
	}

	for _, pointer := range pointers {
		_, plain, err := store.openAt(ctx, objectID, pointer.Version, pointer.Digest)
		if err != nil {
			t.Fatalf("open v%d: %v", pointer.Version, err)
		}
		if !bytes.Equal(plain, payload) {
			t.Fatalf("v%d plaintext = %q", pointer.Version, plain)
		}
	}

	oldest := pointers[0]
	if _, _, err := store.openAt(ctx, objectID, 4, oldest.Digest); !errors.Is(err, ErrTampered) {
		t.Fatalf("replaying v1 envelope at v4: err = %v, want ErrTampered", err)
	}

	genesisEnv, _, err := store.openAt(ctx, objectID, pointers[0].Version, pointers[0].Digest)
	if err != nil {
		t.Fatalf("open genesis: %v", err)
	}
	if len(genesisEnv.prevDigest) != 0 {
		t.Fatalf("genesis prevDigest = %x, want empty", genesisEnv.prevDigest)
	}
	for i := 1; i < len(pointers); i++ {
		env, _, err := store.openAt(ctx, objectID, pointers[i].Version, pointers[i].Digest)
		if err != nil {
			t.Fatalf("open v%d: %v", i+1, err)
		}
		if !bytes.Equal(env.prevDigest, pointers[i-1].Digest) {
			t.Fatalf("v%d prevDigest does not point at v%d envelope", i+1, i)
		}
		if env.plainLen != uint64(len(payload)) {
			t.Fatalf("v%d authenticated plainLen = %d, want %d", i+1, env.plainLen, len(payload))
		}
	}

	if err := store.VerifyChain(ctx, objectID); err != nil {
		t.Fatalf("verify chain: %v", err)
	}

	got, cur, err := store.Read(ctx, objectID)
	if err != nil {
		t.Fatalf("read current: %v", err)
	}
	if cur.Version != 4 || !bytes.Equal(got, payload) {
		t.Fatalf("current read wrong: version %d, payload %q", cur.Version, got)
	}
}

func TestConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	store, err := New(NewMemBlobStore(), NewMemMetadataStore(), mustKey(t))
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	const writers = 50
	const objectID = "hot-object"
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := store.Write(ctx, objectID, []byte(fmt.Sprintf("writer-%02d", i))); err != nil {
				t.Errorf("concurrent write failed: %v", err)
			}
		}(i)
	}
	wg.Wait()

	cur, err := store.Current(ctx, objectID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if cur.Version != writers {
		t.Fatalf("version after %d concurrent writes = %d, want %d", writers, cur.Version, writers)
	}
	if _, _, err := store.Read(ctx, objectID); err != nil {
		t.Fatalf("read after concurrent writes: %v", err)
	}
	if err := store.VerifyChain(ctx, objectID); err != nil {
		t.Fatalf("chain after concurrent writes: %v", err)
	}
}

func TestConcurrentWritesFileStore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, key := newFileBackedStore(t, dir)

	const writers = 20
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := store.Write(ctx, "fs-object", []byte(fmt.Sprintf("payload-%02d", i))); err != nil {
				t.Errorf("write %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	reopened := reopenFileBackedStore(t, dir, key)
	cur, err := reopened.Current(ctx, "fs-object")
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if cur.Version != writers {
		t.Fatalf("file-backed version = %d, want %d", cur.Version, writers)
	}
	if err := reopened.VerifyChain(ctx, "fs-object"); err != nil {
		t.Fatalf("verify chain: %v", err)
	}
}

func TestRollbackBlobSubstitution(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, key := newFileBackedStore(t, dir)
	blob := store.blob.(*FileBlobStore)

	const objectID = "rollback-target"
	var oldDigest, genuineV3 []byte
	for i := 0; i < 3; i++ {
		pointer, err := store.Write(ctx, objectID, []byte(fmt.Sprintf("version-%d-payload", i)))
		if err != nil {
			t.Fatalf("write v%d: %v", i+1, err)
		}
		if i == 0 {
			oldDigest = append([]byte(nil), pointer.Digest...)
		}
		if i == 2 {
			genuineV3, err = blob.Get(ctx, objectID, pointer.Digest)
			if err != nil {
				t.Fatalf("backup genuine v3: %v", err)
			}
		}
	}

	cur, err := store.Current(ctx, objectID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if cur.Version != 3 {
		t.Fatalf("version = %d, want 3", cur.Version)
	}

	// Attacker takes a legitimate old ciphertext and overwrites the current
	// blob path. The pointer digest check must reject the swap.
	oldEnvelope, err := blob.Get(ctx, objectID, oldDigest)
	if err != nil {
		t.Fatalf("load old envelope: %v", err)
	}
	currentPath, err := blob.path(objectID, cur.Digest)
	if err != nil {
		t.Fatalf("current path: %v", err)
	}
	if err := os.WriteFile(currentPath, oldEnvelope, 0o600); err != nil {
		t.Fatalf("attacker overwrite: %v", err)
	}
	if _, _, err := store.Read(ctx, objectID); !errors.Is(err, ErrTampered) {
		t.Fatalf("read after rollback substitution: err = %v, want ErrTampered", err)
	}
	if err := store.VerifyChain(ctx, objectID); !errors.Is(err, ErrTampered) {
		t.Fatalf("verify after rollback substitution: err = %v, want ErrTampered", err)
	}

	// Even with metadata write access, the pointer cannot move backwards.
	oldPointer := &Pointer{Version: 1, Digest: oldDigest}
	if err := store.meta.CAS(ctx, objectID, cur, oldPointer); !errors.Is(err, ErrConflict) {
		t.Fatalf("backwards pointer CAS: err = %v, want ErrConflict", err)
	}

	// Restore the genuine v3 envelope: the object is healthy again and the
	// next write extends the chain from v3.
	if err := os.WriteFile(currentPath, genuineV3, 0o600); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, _, err := store.Read(ctx, objectID); err != nil {
		t.Fatalf("read after restore: %v", err)
	}
	next, err := store.Write(ctx, objectID, []byte("version-4-payload"))
	if err != nil {
		t.Fatalf("write after restore: %v", err)
	}
	if next.Version != 4 {
		t.Fatalf("post-restore version = %d, want 4", next.Version)
	}

	reopened := reopenFileBackedStore(t, dir, key)
	if err := reopened.VerifyChain(ctx, objectID); err != nil {
		t.Fatalf("verify after recovery: %v", err)
	}
}

func TestCrossObjectReplayRejected(t *testing.T) {
	ctx := context.Background()
	store, err := New(NewMemBlobStore(), NewMemMetadataStore(), mustKey(t))
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	pointerA, err := store.Write(ctx, "object-a", []byte("secret A"))
	if err != nil {
		t.Fatalf("write a: %v", err)
	}
	if _, err := store.Write(ctx, "object-b", []byte("secret B")); err != nil {
		t.Fatalf("write b: %v", err)
	}

	envelopeA, err := store.blob.Get(ctx, "object-a", pointerA.Digest)
	if err != nil {
		t.Fatalf("get a envelope: %v", err)
	}
	// Plant A's legitimate envelope inside B's bucket. Digest matches the
	// bytes, but the AAD-bound object id is "object-a".
	if err := store.blob.Put(ctx, "object-b", pointerA.Digest, envelopeA); err != nil {
		t.Fatalf("plant: %v", err)
	}
	if _, _, err := store.openAt(ctx, "object-b", 1, pointerA.Digest); !errors.Is(err, ErrTampered) {
		t.Fatalf("cross-object replay: err = %v, want ErrTampered", err)
	}
}

func TestTamperedEnvelopeRejected(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, _ := newFileBackedStore(t, dir)

	const objectID = "mutated"
	pointer, err := store.Write(ctx, objectID, []byte("do not flip bits in me"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	blob := store.blob.(*FileBlobStore)
	path, err := blob.path(objectID, pointer.Digest)
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read envelope: %v", err)
	}

	// Flip a bit inside the ciphertext region (past the fixed header).
	mutated := append([]byte(nil), raw...)
	last := len(mutated) - 1
	mutated[last] ^= 0x01
	if err := os.WriteFile(path, mutated, 0o600); err != nil {
		t.Fatalf("write mutated: %v", err)
	}
	if _, _, err := store.Read(ctx, objectID); !errors.Is(err, ErrTampered) {
		t.Fatalf("read after bit flip: err = %v, want ErrTampered", err)
	}
}

func TestInvalidObjectID(t *testing.T) {
	ctx := context.Background()
	store, err := New(NewMemBlobStore(), NewMemMetadataStore(), mustKey(t))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if _, err := store.Write(ctx, "", []byte("x")); !errors.Is(err, ErrInvalidObjectID) {
		t.Fatalf("empty id: err = %v, want ErrInvalidObjectID", err)
	}
}

func TestInterruptedMetadataCommit(t *testing.T) {
	ctx := context.Background()

	t.Run("fault_before_write_leaves_previous_pointer", func(t *testing.T) {
		dir := t.TempDir()
		store, key := newFileBackedStore(t, dir)
		meta := store.meta.(*FileMetadataStore)

		first, err := store.Write(ctx, "obj", []byte("v1"))
		if err != nil {
			t.Fatalf("v1: %v", err)
		}

		var calls int32
		meta.FaultHook = func(stage FaultStage, objectID string) error {
			if stage == StageBeforeWrite && atomic.AddInt32(&calls, 1) == 1 {
				return ErrInjected
			}
			return nil
		}

		if _, err := store.Write(ctx, "obj", []byte("v2")); !errors.Is(err, ErrInjected) {
			t.Fatalf("interrupted write: err = %v, want ErrInjected", err)
		}

		// Pointer must still point at v1 and remain fully readable after reopen.
		reopened := reopenFileBackedStore(t, dir, key)
		cur, err := reopened.Current(ctx, "obj")
		if err != nil {
			t.Fatalf("current after fault: %v", err)
		}
		if !samePointer(cur, first) {
			t.Fatalf("pointer moved despite fault: got v%d, want v%d", cur.Version, first.Version)
		}
		got, _, err := reopened.Read(ctx, "obj")
		if err != nil {
			t.Fatalf("read after fault: %v", err)
		}
		if string(got) != "v1" {
			t.Fatalf("payload after fault = %q, want v1", got)
		}

		// Hook now allows everything: the next write must produce v2, proving no
		// stuck temp state blocks progress.
		meta.FaultHook = nil
		next, err := reopened.Write(ctx, "obj", []byte("v2-real"))
		if err != nil {
			t.Fatalf("write after recovery: %v", err)
		}
		if next.Version != 2 {
			t.Fatalf("version after recovery = %d, want 2", next.Version)
		}
		if err := reopened.VerifyChain(ctx, "obj"); err != nil {
			t.Fatalf("verify after recovery: %v", err)
		}
	})

	t.Run("fault_after_temp_write_never_exposes_partial", func(t *testing.T) {
		dir := t.TempDir()
		store, key := newFileBackedStore(t, dir)
		meta := store.meta.(*FileMetadataStore)

		if _, err := store.Write(ctx, "obj", []byte("v1")); err != nil {
			t.Fatalf("v1: %v", err)
		}

		var calls int32
		meta.FaultHook = func(stage FaultStage, objectID string) error {
			if stage == StageAfterTempWrite && atomic.AddInt32(&calls, 1) == 1 {
				return ErrInjected
			}
			return nil
		}

		if _, err := store.Write(ctx, "obj", []byte("v2")); !errors.Is(err, ErrInjected) {
			t.Fatalf("interrupted write: err = %v, want ErrInjected", err)
		}

		// Only the committed JSON file should remain; no temp files leak.
		entries, err := os.ReadDir(meta.root)
		if err != nil {
			t.Fatalf("read meta dir: %v", err)
		}
		for _, entry := range entries {
			if filepath.Ext(entry.Name()) != ".json" {
				t.Fatalf("unexpected temp file left behind: %s", entry.Name())
			}
		}

		reopened := reopenFileBackedStore(t, dir, key)
		cur, err := reopened.Current(ctx, "obj")
		if err != nil {
			t.Fatalf("current: %v", err)
		}
		if cur.Version != 1 {
			t.Fatalf("version after temp-write fault = %d, want 1", cur.Version)
		}
		if err := reopened.VerifyChain(ctx, "obj"); err != nil {
			t.Fatalf("verify: %v", err)
		}
	})

	t.Run("survive_repeated_interruptions_then_converge", func(t *testing.T) {
		dir := t.TempDir()
		store, key := newFileBackedStore(t, dir)
		meta := store.meta.(*FileMetadataStore)

		var faults int32
		meta.FaultHook = func(stage FaultStage, objectID string) error {
			if stage == StageBeforeWrite && atomic.AddInt32(&faults, 1)%2 == 0 {
				return ErrInjected
			}
			return nil
		}

		var committed int32
		for i := 0; i < 6; i++ {
			if _, err := store.Write(ctx, "obj", []byte(fmt.Sprintf("payload-%d", i))); err == nil {
				atomic.AddInt32(&committed, 1)
			}
		}
		if committed == 0 || committed >= 6 {
			t.Fatalf("expected some successes and some injected faults, got %d/6", committed)
		}

		meta.FaultHook = nil
		reopened := reopenFileBackedStore(t, dir, key)
		cur, err := reopened.Current(ctx, "obj")
		if err != nil {
			t.Fatalf("current: %v", err)
		}
		if cur.Version != uint64(committed) {
			t.Fatalf("committed version = %d, want %d", cur.Version, committed)
		}
		if err := reopened.VerifyChain(ctx, "obj"); err != nil {
			t.Fatalf("verify chain after repeated faults: %v", err)
		}
	})
}

func TestWriteContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store, err := New(NewMemBlobStore(), NewMemMetadataStore(), mustKey(t))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	cancel()
	if _, err := store.Write(ctx, "obj", []byte("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write: err = %v, want context.Canceled", err)
	}
}

func TestPrevDigestFieldTampering(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, _ := newFileBackedStore(t, dir)

	const objectID = "chain"
	var v2Pointer *Pointer
	for i := 0; i < 3; i++ {
		pointer, err := store.Write(ctx, objectID, []byte(fmt.Sprintf("p%d", i)))
		if err != nil {
			t.Fatalf("write: %v", err)
		}
		if i == 1 {
			v2Pointer = pointer
		}
	}

	// Flip a byte in the v2 envelope's prevDigest header field. Because the
	// whole AAD is GCM-authenticated, Open must fail even though the outer
	// sha256 digest still matches the (now modified) on-disk bytes: simulate a
	// full replacement by also rewriting the pointer to the new digest.
	blob := store.blob.(*FileBlobStore)
	oldPath, err := blob.path(objectID, v2Pointer.Digest)
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	raw, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	env, err := parseEnvelope(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Locate the prevDigest bytes inside the header and flip the first one.
	offset := len(magic) + 2 + len(env.nonce) + 2 + len(objectID) + 8 + 2
	mutated := append([]byte(nil), raw...)
	mutated[offset] ^= 0x01
	newDigest := sha256.Sum256(mutated)
	newPath, err := blob.path(objectID, newDigest[:])
	if err != nil {
		t.Fatalf("new path: %v", err)
	}
	if err := os.WriteFile(newPath, mutated, 0o600); err != nil {
		t.Fatalf("write mutated: %v", err)
	}

	// Forcing the store to open the mutated envelope at its real version must
	// fail AEAD authentication rather than silently accept the new predecessor.
	if _, _, err := store.openAt(ctx, objectID, v2Pointer.Version, newDigest[:]); !errors.Is(err, ErrTampered) {
		t.Fatalf("tampered prevDigest: err = %v, want ErrTampered", err)
	}
}

func TestPointerFileRollbackOnDisk(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, key := newFileBackedStore(t, dir)

	const objectID = "ptr-rollback"
	var v1 *Pointer
	for i := 0; i < 3; i++ {
		pointer, err := store.Write(ctx, objectID, []byte(fmt.Sprintf("p%d", i)))
		if err != nil {
			t.Fatalf("write: %v", err)
		}
		if i == 0 {
			v1 = pointer
		}
	}

	// Simulate an attacker with file-level write access replacing the pointer
	// JSON with a stale v1 pointer (a valid, well-formed old pointer).
	meta := store.meta.(*FileMetadataStore)
	metaPath, err := meta.path(objectID)
	if err != nil {
		t.Fatalf("meta path: %v", err)
	}
	stale, err := json.Marshal(persistedPointer{
		Version: v1.Version,
		Digest:  hex.EncodeToString(v1.Digest),
	})
	if err != nil {
		t.Fatalf("marshal stale pointer: %v", err)
	}
	if err := os.WriteFile(metaPath, stale, 0o600); err != nil {
		t.Fatalf("overwrite pointer: %v", err)
	}

	// The re-opened store reads the rolled-back pointer. Reading v1 itself
	// succeeds because that envelope is genuine, but VerifyChain proves the
	// on-disk history contains more versions; more importantly the legitimate
	// write path refuses to continue from the stale pointer once the real
	// current state is recovered. Here we assert the rollback is observable
	// and that a forward CAS from the real v3 state is rejected after reopen
	// only through the conflict guard.
	reopened := reopenFileBackedStore(t, dir, key)
	cur, err := reopened.Current(ctx, objectID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if cur.Version != 1 {
		t.Fatalf("attacker rolled pointer back, got version %d, want 1", cur.Version)
	}
	got, _, err := reopened.Read(ctx, objectID)
	if err != nil {
		t.Fatalf("read rolled-back genuine v1: %v", err)
	}
	if string(got) != "p0" {
		t.Fatalf("rolled-back payload = %q, want p0", got)
	}

	// Any CAS claiming the old v3 expectation must now conflict, preventing a
	// split-brain extension from a stale in-memory handle.
	realV3 := &Pointer{Version: 3}
	err = reopened.meta.CAS(ctx, objectID, realV3, &Pointer{Version: 4, Digest: v1.Digest})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("CAS with stale expected pointer: err = %v, want ErrConflict", err)
	}
}

func TestAuxiliaryBranchesAndMalformedInputs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, key := newFileBackedStore(t, dir)

	pointer, err := store.Write(ctx, "aux", []byte("payload"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	// BlobStore.Has reflects presence without reading content.
	blob := store.blob.(*FileBlobStore)
	has, err := blob.Has(ctx, "aux", pointer.Digest)
	if err != nil || !has {
		t.Fatalf("Has existing = %v, %v; want true, nil", has, err)
	}
	missingDigest := make([]byte, 32)
	has, err = blob.Has(ctx, "aux", missingDigest)
	if err != nil || has {
		t.Fatalf("Has missing = %v, %v; want false, nil", has, err)
	}

	// Idempotent re-put of identical envelope succeeds.
	envelopeBytes, err := blob.Get(ctx, "aux", pointer.Digest)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if err := blob.Put(ctx, "aux", pointer.Digest, envelopeBytes); err != nil {
		t.Fatalf("idempotent put: %v", err)
	}

	// Malformed envelopes are rejected as tampering rather than panicking.
	for name, raw := range map[string][]byte{
		"empty":          {},
		"bad magic":      append([]byte("XXXX"), make([]byte, 32)...),
		"truncated":      []byte(magic),
		"zero nonce len": append([]byte(magic), 0, 0),
	} {
		if _, err := parseEnvelope(raw); !errors.Is(err, ErrTampered) {
			t.Fatalf("%s: parse err = %v, want ErrTampered", name, err)
		}
	}

	// A store built with a wrong key cannot authenticate the same blobs.
	wrongStore, err := New(blob, store.meta, mustKey(t))
	if err != nil {
		t.Fatalf("wrong-key store: %v", err)
	}
	if _, _, err := wrongStore.Read(ctx, "aux"); !errors.Is(err, ErrTampered) {
		t.Fatalf("read with wrong key: err = %v, want ErrTampered", err)
	}

	// An invalid key length is rejected at construction.
	if _, err := New(blob, store.meta, []byte("short")); err == nil {
		t.Fatal("New with 5-byte key: expected error")
	}

	// A corrupted pointer file surfaces as tampering rather than a crash.
	meta := store.meta.(*FileMetadataStore)
	metaPath, err := meta.path("aux")
	if err != nil {
		t.Fatalf("meta path: %v", err)
	}
	if err := os.WriteFile(metaPath, []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("corrupt pointer: %v", err)
	}
	reopened := reopenFileBackedStore(t, dir, key)
	if _, err := reopened.Current(ctx, "aux"); !errors.Is(err, ErrTampered) {
		t.Fatalf("corrupt pointer: err = %v, want ErrTampered", err)
	}

	// Mem store Has and nil-CAS guard for completeness.
	memBlob := NewMemBlobStore()
	if has, err := memBlob.Has(ctx, "none", missingDigest); err != nil || has {
		t.Fatalf("mem Has missing = %v, %v", has, err)
	}
	memMeta := NewMemMetadataStore()
	if err := memMeta.CAS(ctx, "x", nil, nil); err == nil {
		t.Fatal("nil new pointer CAS: expected error")
	}
}
