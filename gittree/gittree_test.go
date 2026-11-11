package gittree

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// fixture is a small synthetic repository used by the proof tests.
type fixture struct {
	algo Algorithm
	src  *MemorySource

	root       ObjectID
	dirA       ObjectID
	dirSub     ObjectID
	file1      ObjectID
	file2      ObjectID
	executable ObjectID
	symlink    ObjectID
	gitlink    ObjectID

	file1Content []byte
	file2Content []byte
	linkTarget   []byte
}

func newFixture(t *testing.T, algo Algorithm) *fixture {
	t.Helper()
	f := &fixture{
		algo:         algo,
		src:          NewMemorySource(),
		file1Content: []byte("release artifact one\n"),
		file2Content: []byte("nested artifact contents\n"),
		linkTarget:   []byte("file1.txt"),
	}

	f.file1 = f.src.Add(algo, TypeBlob, f.file1Content)
	f.file2 = f.src.Add(algo, TypeBlob, f.file2Content)
	f.executable = f.src.Add(algo, TypeBlob, []byte("#!/bin/sh\necho hi\n"))
	f.symlink = f.src.Add(algo, TypeBlob, f.linkTarget)
	f.gitlink = HashObject(algo, TypeCommit, []byte("synthetic submodule pointer"))

	sub := mustTree(t, f.src, algo, []Entry{
		{Mode: ModeRegular, Name: "file2.txt", ID: f.file2},
	})
	f.dirSub = sub

	dirA := mustTree(t, f.src, algo, []Entry{
		{Mode: ModeExecutable, Name: "run.sh", ID: f.executable},
		{Mode: ModeTree, Name: "sub", ID: sub},
	})
	f.dirA = dirA

	root := mustTree(t, f.src, algo, []Entry{
		{Mode: ModeTree, Name: "a", ID: dirA},
		{Mode: ModeRegular, Name: "file1.txt", ID: f.file1},
		{Mode: ModeSymlink, Name: "link-to-file1", ID: f.symlink},
		{Mode: ModeGitlink, Name: "vendor-lib", ID: f.gitlink},
	})
	f.root = root
	return f
}

func mustTree(t *testing.T, src *MemorySource, algo Algorithm, entries []Entry) ObjectID {
	t.Helper()
	id, err := src.AddTree(algo, entries)
	if err != nil {
		t.Fatalf("AddTree: %v", err)
	}
	return id
}

func (f *fixture) proof(components ...string) *MembershipProof {
	ids := map[string]ObjectID{
		"a":   f.dirA,
		"sub": f.dirSub,
	}
	current := f.root
	steps := make([]PathStep, 0, len(components))
	for _, name := range components {
		steps = append(steps, PathStep{Name: name, TreeID: current})
		if next, ok := ids[name]; ok {
			current = next
		}
	}
	return &MembershipProof{RootID: f.root, Path: steps}
}

// countingSource wraps a source and counts Object calls.
type countingSource struct {
	inner ObjectSource
	mu    sync.Mutex
	n     uint64
}

func (c *countingSource) Object(ctx context.Context, id ObjectID) (RawObject, error) {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return c.inner.Object(ctx, id)
}

func (c *countingSource) calls() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// tamperSource serves overridden content for selected IDs, delegating all
// other lookups to the inner source.
type tamperSource struct {
	inner    ObjectSource
	override map[ObjectID]RawObject
}

func (t *tamperSource) Object(ctx context.Context, id ObjectID) (RawObject, error) {
	if obj, ok := t.override[id]; ok {
		return RawObject{Type: obj.Type, Content: append([]byte(nil), obj.Content...)}, nil
	}
	return t.inner.Object(ctx, id)
}

func TestKnownGitObjectVectors(t *testing.T) {
	if got := HashObject(SHA1, TypeBlob, nil); got != EmptyBlobID {
		t.Fatalf("empty blob = %s, want %s", got, EmptyBlobID)
	}
	if got := HashObject(SHA1, TypeTree, nil); got != EmptyTreeID {
		t.Fatalf("empty tree = %s, want %s", got, EmptyTreeID)
	}

	h := sha1.New()
	fmt.Fprintf(h, "blob 0\x00")
	if got := ObjectID(hex.EncodeToString(h.Sum(nil))); got != EmptyBlobID {
		t.Fatalf("manual blob digest = %s", got)
	}

	h256 := sha256.New()
	fmt.Fprintf(h256, "blob 0\x00")
	want256 := ObjectID(hex.EncodeToString(h256.Sum(nil)))
	if got := HashObject(SHA256, TypeBlob, nil); got != want256 {
		t.Fatalf("sha256 empty blob = %s, want %s", got, want256)
	}
}

func TestVerifyFileHappyPaths(t *testing.T) {
	for _, algo := range []Algorithm{SHA1, SHA256} {
		t.Run(algo.String(), func(t *testing.T) {
			f := newFixture(t, algo)
			v := NewVerifier(f.src)
			ctx := context.Background()

			result, err := v.VerifyFile(ctx, f.proof("file1.txt"))
			if err != nil {
				t.Fatalf("VerifyFile(file1): %v", err)
			}
			if result.ID != f.file1 || string(result.Content) != string(f.file1Content) {
				t.Fatalf("unexpected result %+v", result)
			}
			if !result.Mode.IsRegularFile() || result.IsTree {
				t.Fatalf("file1 not reported as regular file: %+v", result)
			}

			nested, err := v.VerifyFile(ctx, f.proof("a", "sub", "file2.txt"))
			if err != nil {
				t.Fatalf("VerifyFile(nested): %v", err)
			}
			if nested.ID != f.file2 || string(nested.Content) != string(f.file2Content) {
				t.Fatalf("unexpected nested result %+v", nested)
			}

			run, err := v.VerifyFile(ctx, f.proof("a", "run.sh"))
			if err != nil {
				t.Fatalf("VerifyFile(run.sh): %v", err)
			}
			if run.Mode != ModeExecutable {
				t.Fatalf("run.sh mode = %s, want %s", run.Mode, ModeExecutable)
			}
		})
	}
}

func TestSharedPrefixUsesVerifiedCache(t *testing.T) {
	f := newFixture(t, SHA1)
	counter := &countingSource{inner: f.src}
	v := NewVerifier(counter)
	ctx := context.Background()

	if _, err := v.VerifyFile(ctx, f.proof("a", "sub", "file2.txt")); err != nil {
		t.Fatalf("first proof: %v", err)
	}
	statsAfterFirst := v.Stats()
	if statsAfterFirst.TreeMisses != 3 || statsAfterFirst.TreeHits != 0 {
		t.Fatalf("stats after first proof = %+v, want 3 misses", statsAfterFirst)
	}

	fetchesBefore := counter.calls()
	if _, err := v.VerifyFile(ctx, f.proof("a", "run.sh")); err != nil {
		t.Fatalf("second proof: %v", err)
	}
	stats := v.Stats()
	if stats.TreeHits != 2 {
		t.Fatalf("tree hits = %d, want 2; stats %+v", stats.TreeHits, stats)
	}
	if got := counter.calls() - fetchesBefore; got != 1 {
		t.Fatalf("source calls for second proof = %d, want 1 (the blob only)", got)
	}

	if _, err := v.VerifyFile(ctx, f.proof("file1.txt")); err != nil {
		t.Fatalf("third proof: %v", err)
	}
	if stats = v.Stats(); stats.TreeHits != 3 {
		t.Fatalf("tree hits = %d, want 3; stats %+v", stats.TreeHits, stats)
	}
}

func TestCorruptedTreeIsNeverCached(t *testing.T) {
	f := newFixture(t, SHA1)
	tampered := &tamperSource{
		inner:    f.src,
		override: map[ObjectID]RawObject{},
	}
	tampered.override[f.dirA] = RawObject{Type: TypeTree, Content: []byte("not a real tree")}

	v := NewVerifier(tampered)
	ctx := context.Background()

	if _, err := v.VerifyFile(ctx, f.proof("a", "run.sh")); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("corrupted proof error = %v, want ErrDigestMismatch", err)
	}
	if _, err := v.VerifyFile(ctx, f.proof("a", "sub", "file2.txt")); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("second corrupted proof error = %v, want ErrDigestMismatch", err)
	}
	stats := v.Stats()
	if stats.TreesCached != 1 || stats.TreeHits != 1 || stats.TreeMisses != 3 {
		t.Fatalf("cache after corrupted proofs = %+v, want only the verified root cached (reused, never the corrupted tree)", stats)
	}

	good := NewVerifier(f.src)
	if _, err := good.VerifyFile(ctx, f.proof("a", "run.sh")); err != nil {
		t.Fatalf("verification with clean source failed: %v", err)
	}
	if _, err := good.VerifyFile(ctx, f.proof("a", "sub", "file2.txt")); err != nil {
		t.Fatalf("nested verification with clean source failed: %v", err)
	}
}
