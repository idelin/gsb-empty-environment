package gitaccess

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// repo bundles the synthesized objects used by the tests.
type repo struct {
	db       *ObjectDatabase
	svc      *Service
	blobV1   ObjectID
	blobV2   ObjectID
	blobV3   ObjectID
	treeV1   ObjectID
	treeV2   ObjectID
	commitV1 ObjectID
	commitV2 ObjectID
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	db := NewObjectDatabase()

	b1, idB1 := blobObject("release artifact version 1\n")
	b2, idB2 := blobObject("release artifact version 2, rotated contents\n")
	b3, idB3 := blobObject("unrelated artifact, never referenced\n")
	t1, idT1 := treeObject([]treeEntry{{mode: "100644", name: "artifact.bin", id: idB1}})
	t2, idT2 := treeObject([]treeEntry{{mode: "100644", name: "artifact.bin", id: idB2}})
	c1, idC1 := commitObject(commitSpec{tree: idT1, message: "v1\n"})
	c2, idC2 := commitObject(commitSpec{tree: idT2, parents: []ObjectID{idC1}, message: "v2\n"})

	for _, obj := range []*Object{b1, b2, b3, t1, t2, c1, c2} {
		addLoose(db, obj)
	}

	return &repo{
		db:       db,
		svc:      NewService(db),
		blobV1:   idB1,
		blobV2:   idB2,
		blobV3:   idB3,
		treeV1:   idT1,
		treeV2:   idT2,
		commitV1: idC1,
		commitV2: idC2,
	}
}

// TestRefReplacement proves a capability stays pinned to the snapshot it was
// issued against: replacing the same-named reference neither widens nor narrows
// the already granted object set.
func TestRefReplacement(t *testing.T) {
	r := newRepo(t)
	r.svc.UpdateRef("refs/heads/release", r.commitV1)

	cap1, err := r.svc.IssueCapability(context.Background(), "tenant-a", []string{"refs/heads/release"})
	if err != nil {
		t.Fatalf("issue first capability: %v", err)
	}

	// Replace the reference with the same name but a different target.
	r.svc.UpdateRef("refs/heads/release", r.commitV2)
	if got, ok := r.svc.ResolveRef("refs/heads/release"); !ok || got != r.commitV2 {
		t.Fatalf("reference replacement not visible to new reads, got %s", got)
	}

	cap2, err := r.svc.IssueCapability(context.Background(), "tenant-a", []string{"refs/heads/release"})
	if err != nil {
		t.Fatalf("issue second capability: %v", err)
	}

	// cap1 still reads the old snapshot, including v1 objects.
	if !cap1.Allowed(r.commitV1) || !cap1.Allowed(r.blobV1) {
		t.Fatal("old capability lost access to original snapshot")
	}
	if cap1.Allowed(r.commitV2) || cap1.Allowed(r.blobV2) {
		t.Fatal("reference replacement widened an already issued capability")
	}
	if got := cap1.Snapshot()["refs/heads/release"]; got != r.commitV1 {
		t.Fatalf("capability snapshot mutated: got %s want %s", got, r.commitV1)
	}

	// cap2 is pinned to the new snapshot and cannot read revoked objects.
	if !cap2.Allowed(r.commitV2) || !cap2.Allowed(r.blobV2) {
		t.Fatal("new capability cannot see replacement snapshot")
	}
	// v1 stays reachable through v2's parent chain; the unrelated v3 blob is
	// the genuine negative control for snapshot isolation.
	if cap2.Allowed(r.blobV3) {
		t.Fatal("new capability can read an object outside its snapshot")
	}
	if !cap2.Allowed(r.blobV1) {
		t.Fatal("v1 parent history must remain reachable from the v2 snapshot")
	}

	obj, err := cap1.ReadObject(context.Background(), r.blobV1)
	if err != nil {
		t.Fatalf("read original blob: %v", err)
	}
	if string(obj.Content) != "release artifact version 1\n" {
		t.Fatalf("unexpected blob content %q", obj.Content)
	}

	if _, err := cap1.ReadObject(context.Background(), r.blobV2); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized for widened read, got %v", err)
	}
	if _, err := cap2.ReadObject(context.Background(), r.blobV3); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized for unrelated read, got %v", err)
	}
}

// TestConcurrentRefUpdate hammers reference replacement concurrently with
// capability issuance and reads. Every issued capability must remain internally
// consistent, and no capability may ever read an object outside its snapshot.
func TestConcurrentRefUpdate(t *testing.T) {
	r := newRepo(t)
	r.svc.UpdateRef("refs/heads/release", r.commitV1)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			target := r.commitV1
			if i%2 == 0 {
				target = r.commitV2
			}
			r.svc.UpdateRef("refs/heads/release", target)

			capability, err := r.svc.IssueCapability(context.Background(), "tenant-c", []string{"refs/heads/release"})
			if err != nil {
				t.Errorf("issue capability: %v", err)
				return
			}
			pinned := capability.Snapshot()["refs/heads/release"]
			switch pinned {
			case r.commitV1:
				if capability.Allowed(r.blobV2) {
					t.Error("v1 capability sees v2 blob after concurrent update")
				}
			case r.commitV2:
				if capability.Allowed(r.blobV3) {
					t.Error("v2 capability sees unrelated blob after concurrent update")
				}
			default:
				t.Errorf("unexpected snapshot target %s", pinned)
			}
			if _, err := capability.ReadObject(context.Background(), pinned); err != nil {
				t.Errorf("read pinned commit: %v", err)
			}
		}(i)
	}
	wg.Wait()
}

// buildDeltaPack creates a pack whose blob is reachable only through a chain
// of OFS deltas, plus the tree and commit that reference it.
func buildDeltaPack(t *testing.T) (*Pack, ObjectID) {
	t.Helper()
	base, _ := blobObject("delta payload line 000\n")
	prev := base

	pb := newPackBuilder()
	pb.AddObject(base)
	var headID ObjectID
	for i := 1; i <= 3; i++ {
		next, nextID := blobObject(fmt.Sprintf("delta payload line %03d\n", i))
		gotID := pb.AddOfsDelta(prev, next)
		if gotID != nextID {
			t.Fatalf("delta id mismatch: got %s want %s", gotID, nextID)
		}
		prev = next
		headID = nextID
	}

	tree, treeID := treeObject([]treeEntry{{mode: "100644", name: "delta.bin", id: headID}})
	commit, commitID := commitObject(commitSpec{tree: treeID, message: "delta commit\n"})
	pb.AddObject(tree)
	pb.AddObject(commit)

	data, index := pb.finish()
	pack, err := OpenPack(data, index)
	if err != nil {
		t.Fatalf("open delta pack: %v", err)
	}
	return pack, commitID
}

// TestDeltaDigestVerified ensures a multi-step delta chain is reconstructed and
// only returned once the reconstructed content matches its Git digest.
func TestDeltaDigestVerified(t *testing.T) {
	r := newRepo(t)
	pack, commitID := buildDeltaPack(t)
	r.db.RegisterPack(pack)
	r.svc.UpdateRef("refs/heads/packed", commitID)

	capability, err := r.svc.IssueCapability(context.Background(), "tenant-d", []string{"refs/heads/packed"})
	if err != nil {
		t.Fatalf("issue capability over delta pack: %v", err)
	}

	var blobID ObjectID
	for id := range capability.allowed {
		obj, err := r.db.LoadVerified(context.Background(), id)
		if err == nil && obj.Type == BlobObject {
			blobID = id
		}
	}
	if blobID.IsZero() {
		t.Fatal("no reachable blob discovered in delta pack")
	}

	obj, err := capability.ReadObject(context.Background(), blobID)
	if err != nil {
		t.Fatalf("read delta-reconstructed blob: %v", err)
	}
	if want := "delta payload line 003\n"; string(obj.Content) != want {
		t.Fatalf("reconstructed content = %q want %q", obj.Content, want)
	}
	if hashObject(obj.Type, obj.Content) != blobID {
		t.Fatal("returned object failed content digest check")
	}

	// Caller mutations must not leak into the verified capability cache.
	obj.Content[0] = 'X'
	fresh, err := capability.ReadObject(context.Background(), blobID)
	if err != nil {
		t.Fatalf("cached read: %v", err)
	}
	if fresh.Content[0] != 'd' {
		t.Fatal("caller mutation leaked into the capability cache")
	}
}
