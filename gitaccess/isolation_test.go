package gitaccess

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// TestBadDeltaBase points a REF_DELTA at a base id that does not exist in the
// database. Reconstruction must fail, must not return partial content, and must
// never populate the capability cache.
func TestBadDeltaBase(t *testing.T) {
	r := newRepo(t)
	base, _ := blobObject("base blob that will be absent\n")
	target, targetID := blobObject("base blob that will be absent\nwith extra bytes\n")

	pb := newPackBuilder()
	missingBase := mustID("1111111111111111111111111111111111111111")
	pb.AddRefDelta(missingBase, base, target)
	data, index := pb.finish()

	pack, err := OpenPack(data, index)
	if err != nil {
		t.Fatalf("structurally valid pack expected: %v", err)
	}
	r.db.RegisterPack(pack)

	if _, err := r.db.LoadVerified(context.Background(), targetID); !errors.Is(err, ErrDeltaBaseMissing) {
		t.Fatalf("want ErrDeltaBaseMissing, got %v", err)
	}

	capability := &Capability{
		tenant:   "tenant-e",
		snapshot: map[string]ObjectID{"broken": targetID},
		allowed:  map[ObjectID]struct{}{targetID: {}},
		db:       r.db,
		cache:    make(map[ObjectID]*Object),
	}
	if _, err := capability.ReadObject(context.Background(), targetID); !errors.Is(err, ErrDeltaBaseMissing) {
		t.Fatalf("capability read want ErrDeltaBaseMissing, got %v", err)
	}
	capability.mu.Lock()
	_, cached := capability.cache[targetID]
	capability.mu.Unlock()
	if cached {
		t.Fatal("failed delta reconstruction was cached")
	}
}

// TestBadOfsDeltaBase corrupts an OFS_DELTA so its negative relative offset
// points before the pack header, where no base entry exists.
func TestBadOfsDeltaBase(t *testing.T) {
	r := newRepo(t)
	base, _ := blobObject("ofs base padding padding padding\n")
	target, targetID := blobObject("ofs base padding padding padding\nwith a changed tail\n")

	pb := newPackBuilder()
	pb.AddObject(base)
	pb.AddOfsDelta(base, target)
	data, index := pb.finish()

	// The delta entry starts with its size header; the byte immediately after
	// is the first OFS varint byte. Overwrite it with 0x7f so the decoded
	// distance points far before any valid entry offset.
	deltaRec := index[1]
	var pos int64
	// Locate the varint by scanning past the entry header's MSB chain.
	pos = deltaRec.Offset + 1
	for data[pos]&0x80 != 0 {
		pos++
	}
	pos++ // first byte after the size header is the OFS varint
	data[pos] = 0x7f

	pack, err := OpenPack(data, index)
	if err != nil {
		// Structural rejection at open time is an acceptable safe outcome.
		if !errors.Is(err, ErrBadPack) {
			t.Fatalf("want ErrBadPack, got %v", err)
		}
		return
	}
	r.db.RegisterPack(pack)
	_, err = r.db.LoadVerified(context.Background(), targetID)
	if !errors.Is(err, ErrDeltaBaseMissing) && !errors.Is(err, ErrBadPack) {
		t.Fatalf("want ErrDeltaBaseMissing or ErrBadPack for bad OFS base, got %v", err)
	}
}

// TestTamperedDeltaRejected corrupts a byte inside a delta zlib region. The
// digest check on reconstruction must reject the result instead of returning
// content that does not match the declared object id.
func TestTamperedDeltaRejected(t *testing.T) {
	r := newRepo(t)
	base, _ := blobObject("tamper base content padding padding\n")
	target, targetID := blobObject("tamper base content padding padding\n!!corrupt suffix!!\n")

	pb := newPackBuilder()
	pb.AddObject(base)
	pb.AddOfsDelta(base, target)
	data, index := pb.finish()

	entry := index[1]
	// Flip a byte near the end of the compressed region while leaving the
	// entry header and the 20 byte pack trailer untouched.
	pos := entry.Offset + entry.Compressed - 2
	data[pos] ^= 0x20

	corruptPack, err := OpenPack(data, index)
	if err != nil {
		if !errors.Is(err, ErrBadPack) {
			t.Fatalf("want ErrBadPack for corrupted pack, got %v", err)
		}
		return
	}
	r.db.RegisterPack(corruptPack)
	_, err = r.db.LoadVerified(context.Background(), targetID)
	if err == nil {
		t.Fatal("corrupted delta reconstructed to a digest-valid object")
	}
	if !errors.Is(err, ErrChecksumMismatch) && !errors.Is(err, ErrBadPack) {
		t.Fatalf("want digest or pack error for corrupted delta, got %v", err)
	}
}

// TestCancelIsolation cancels a read mid delta chain and proves that:
//   - the canceled read returns context.Canceled and returns no object,
//   - nothing partial is retained in the capability cache,
//   - a subsequent uncanceled read succeeds and returns verified content,
//   - a second tenant's capability can never see the first tenant's cache.
func TestCancelIsolation(t *testing.T) {
	r := newRepo(t)
	pack, commitID := buildDeltaPack(t)

	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	pack.stepHook = func() error {
		// Cancel after the chain has entered a delta step, so a base object
		// may already have been produced inside the request-scoped memo.
		once.Do(cancel)
		return nil
	}
	r.db.RegisterPack(pack)
	r.svc.UpdateRef("refs/heads/packed", commitID)

	cap1, err := r.svc.IssueCapability(context.Background(), "tenant-f", []string{"refs/heads/packed"})
	if err != nil {
		t.Fatalf("issue capability: %v", err)
	}

	var blobID ObjectID
	for id := range cap1.allowed {
		obj, lerr := r.db.LoadVerified(context.Background(), id)
		if lerr == nil && obj.Type == BlobObject {
			blobID = id
		}
	}
	if blobID.IsZero() {
		t.Fatal("no reachable blob discovered in delta pack")
	}

	if _, err := cap1.ReadObject(ctx, blobID); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	cap1.mu.Lock()
	_, cached := cap1.cache[blobID]
	cap1.mu.Unlock()
	if cached {
		t.Fatal("canceled read left a partial or complete object in the cache")
	}

	// Remove the hook and read again: reconstruction must now succeed.
	pack.stepHook = nil
	obj, err := cap1.ReadObject(context.Background(), blobID)
	if err != nil {
		t.Fatalf("retry after cancellation: %v", err)
	}
	if hashObject(obj.Type, obj.Content) != blobID {
		t.Fatal("retry returned content that failed the digest check")
	}

	// A different tenant with an unrelated reference cannot use the first
	// tenant's cache or authorization set.
	r.svc.UpdateRef("refs/heads/other", r.commitV1)
	cap2, err := r.svc.IssueCapability(context.Background(), "tenant-g", []string{"refs/heads/other"})
	if err != nil {
		t.Fatalf("issue second tenant capability: %v", err)
	}
	if cap2.Allowed(blobID) {
		t.Fatal("second tenant inherited object visibility from first tenant")
	}
	if _, err := cap2.ReadObject(context.Background(), blobID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized across tenants, got %v", err)
	}
	if cap1.tenant == cap2.tenant {
		t.Fatal("tenant identifiers collapsed")
	}
}

// TestAnnotatedTagReachability walks a tag -> commit -> tree -> blob chain and
// confirms every linked object lands in the capability's allowed set.
func TestAnnotatedTagReachability(t *testing.T) {
	r := newRepo(t)
	tag, tagID := tagObject(r.commitV1, "commit", "v1.0.0")
	addLoose(r.db, tag)
	r.svc.UpdateRef("refs/tags/v1.0.0", tagID)

	capability, err := r.svc.IssueCapability(context.Background(), "tenant-h", []string{"refs/tags/v1.0.0"})
	if err != nil {
		t.Fatalf("issue capability: %v", err)
	}
	for _, id := range []ObjectID{tagID, r.commitV1, r.treeV1, r.blobV1} {
		if !capability.Allowed(id) {
			t.Fatalf("reachable object %s missing from capability set", id)
		}
		if _, err := capability.ReadObject(context.Background(), id); err != nil {
			t.Fatalf("read reachable object %s: %v", id, err)
		}
	}
}

// TestLooseDigestMismatch ensures a loose object whose stored digest does not
// match its content is rejected rather than returned.
func TestLooseDigestMismatch(t *testing.T) {
	db := NewObjectDatabase()
	obj, id := blobObject("integrity protected\n")
	if _, err := db.AddLoose(obj); err != nil {
		t.Fatalf("add loose: %v", err)
	}

	db.mu.Lock()
	entry := db.loose[id]
	entry.typ = TagObject // claim a different type
	db.loose[id] = entry
	db.mu.Unlock()

	if _, err := db.LoadVerified(context.Background(), id); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("want ErrChecksumMismatch, got %v", err)
	}
}

// TestPackTrailerChecksum rejects a pack whose bytes were modified after
// creation, using the trailing SHA-1 as an integrity boundary.
func TestPackTrailerChecksum(t *testing.T) {
	pb := newPackBuilder()
	obj, _ := blobObject("trailer check\n")
	pb.AddObject(obj)
	data, index := pb.finish()

	// Flip a byte in the header region (inside the checksummed span).
	data[13] ^= 0x01
	if _, err := OpenPack(data, index); !errors.Is(err, ErrBadPack) {
		t.Fatalf("want ErrBadPack for bad trailer, got %v", err)
	}
}
