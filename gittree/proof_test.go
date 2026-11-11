package gittree

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustIDBytes(t *testing.T, id ObjectID) []byte {
	t.Helper()
	raw, err := hex.DecodeString(string(id))
	if err != nil {
		t.Fatalf("decode id %s: %v", id, err)
	}
	return raw
}

func TestCorruptedProofDoesNotPoisonSiblingProof(t *testing.T) {
	f := newFixture(t, SHA1)
	ctx := context.Background()

	honest := NewVerifier(f.src)
	if _, err := honest.VerifyFile(ctx, f.proof("file1.txt")); err != nil {
		t.Fatalf("baseline proof: %v", err)
	}

	// Build a separate two-level tree whose parent the verifier will accept,
	// while the source returns forged bytes for the child's claimed ID.
	childID := HashObject(SHA1, TypeTree, []byte("genuine child tree bytes"))
	parent, err := MarshalTree(SHA1, []Entry{
		{Mode: ModeTree, Name: "child", ID: childID},
	})
	if err != nil {
		t.Fatal(err)
	}
	parentID := HashObject(SHA1, TypeTree, parent)
	tampered := &tamperSource{
		inner: f.src,
		override: map[ObjectID]RawObject{
			parentID: {Type: TypeTree, Content: parent},
			childID:  {Type: TypeTree, Content: []byte("forged")},
		},
	}
	evil := NewVerifier(tampered)
	evilProof := &MembershipProof{
		RootID: parentID,
		Path: []PathStep{
			{Name: "child", TreeID: parentID},
			{Name: "leaf", TreeID: childID},
		},
	}
	if _, err := evil.VerifyEntry(ctx, evilProof); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("evil proof error = %v, want ErrDigestMismatch", err)
	}
	if stats := evil.Stats(); stats.TreesCached != 1 {
		t.Fatalf("evil verifier cached %d trees, want 1 (verified parent only; forged child rejected)", stats.TreesCached)
	}

	if _, err := honest.VerifyFile(ctx, f.proof("a", "sub", "file2.txt")); err != nil {
		t.Fatalf("sibling proof poisoned: %v", err)
	}
	if stats := honest.Stats(); stats.TreeHits != 1 {
		t.Fatalf("honest root cache hit = %d, want 1", stats.TreeHits)
	}
}

func TestSymlinkCannotActAsRegularFile(t *testing.T) {
	f := newFixture(t, SHA1)
	v := NewVerifier(f.src)
	ctx := context.Background()

	entry, err := v.VerifyEntry(ctx, f.proof("link-to-file1"))
	if err != nil {
		t.Fatalf("VerifyEntry(symlink): %v", err)
	}
	if entry.Mode != ModeSymlink || string(entry.Content) != string(f.linkTarget) {
		t.Fatalf("symlink result = %+v", entry)
	}

	if _, err := v.VerifyFile(ctx, f.proof("link-to-file1")); !errors.Is(err, ErrSymlinkNotFile) {
		t.Fatalf("VerifyFile(symlink) error = %v, want ErrSymlinkNotFile", err)
	}

	linkMid := &MembershipProof{
		RootID: f.root,
		Path: []PathStep{
			{Name: "link-to-file1", TreeID: f.root},
			{Name: "child", TreeID: f.symlink},
		},
	}
	if _, err := v.VerifyEntry(ctx, linkMid); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("symlink-as-dir error = %v, want ErrNotDirectory", err)
	}
}

func TestGitlinkRejected(t *testing.T) {
	f := newFixture(t, SHA1)
	v := NewVerifier(f.src)
	ctx := context.Background()

	if _, err := v.VerifyEntry(ctx, f.proof("vendor-lib")); !errors.Is(err, ErrGitlink) {
		t.Fatalf("gitlink leaf error = %v, want ErrGitlink", err)
	}
	if _, err := v.VerifyFile(ctx, f.proof("vendor-lib")); !errors.Is(err, ErrGitlink) {
		t.Fatalf("gitlink VerifyFile error = %v, want ErrGitlink", err)
	}

	intoGitlink := &MembershipProof{
		RootID: f.root,
		Path: []PathStep{
			{Name: "vendor-lib", TreeID: f.root},
			{Name: "inside", TreeID: f.gitlink},
		},
	}
	if _, err := v.VerifyEntry(ctx, intoGitlink); !errors.Is(err, ErrGitlink) {
		t.Fatalf("traversal into gitlink error = %v, want ErrGitlink", err)
	}
}

func TestDuplicateNameRejected(t *testing.T) {
	blob := HashObject(SHA1, TypeBlob, []byte("x"))
	raw, err := MarshalTree(SHA1, []Entry{
		{Mode: ModeRegular, Name: "dup", ID: blob},
	})
	if err != nil {
		t.Fatal(err)
	}
	var dup bytes.Buffer
	fmt.Fprintf(&dup, "%s dup\x00", ModeRegular)
	dup.Write(mustIDBytes(t, blob))
	content := append(append([]byte(nil), raw...), dup.Bytes()...)
	id := HashObject(SHA1, TypeTree, content)

	src := NewMemorySource()
	src.Put(id, RawObject{Type: TypeTree, Content: content})
	v := NewVerifier(src)
	proof := &MembershipProof{
		RootID: id,
		Path:   []PathStep{{Name: "dup", TreeID: id}},
	}
	if _, err := v.VerifyEntry(context.Background(), proof); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("duplicate name error = %v, want ErrDuplicateName", err)
	}
}

func TestDirFileConflictRejected(t *testing.T) {
	blob := HashObject(SHA1, TypeBlob, []byte("x"))
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s foo\x00", ModeRegular)
	buf.Write(mustIDBytes(t, blob))
	fmt.Fprintf(&buf, "%s foo\x00", ModeTree)
	buf.Write(mustIDBytes(t, EmptyTreeID))
	content := buf.Bytes()
	id := HashObject(SHA1, TypeTree, content)

	src := NewMemorySource()
	src.Put(id, RawObject{Type: TypeTree, Content: content})
	src.Add(SHA1, TypeBlob, []byte("x"))
	v := NewVerifier(src)
	proof := &MembershipProof{
		RootID: id,
		Path:   []PathStep{{Name: "foo", TreeID: id}},
	}
	if _, err := v.VerifyEntry(context.Background(), proof); !errors.Is(err, ErrDirFileConflict) {
		t.Fatalf("dir/file conflict error = %v, want ErrDirFileConflict", err)
	}

	if _, err := MarshalTree(SHA1, []Entry{
		{Mode: ModeRegular, Name: "foo", ID: blob},
		{Mode: ModeTree, Name: "foo", ID: EmptyTreeID},
	}); !errors.Is(err, ErrDirFileConflict) {
		t.Fatalf("MarshalTree conflict error = %v, want ErrDirFileConflict", err)
	}
}

func TestConcurrentVerificationSharesSafeCache(t *testing.T) {
	f := newFixture(t, SHA1)
	v := NewVerifier(f.src)
	ctx := context.Background()

	proofs := []*MembershipProof{
		f.proof("a", "sub", "file2.txt"),
		f.proof("a", "run.sh"),
		f.proof("file1.txt"),
		f.proof("link-to-file1"),
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			p := proofs[n%len(proofs)]
			if _, err := v.VerifyEntry(ctx, p); err != nil {
				t.Errorf("concurrent VerifyEntry: %v", err)
			}
		}(i)
	}
	wg.Wait()

	stats := v.Stats()
	if stats.TreesCached != 3 {
		t.Fatalf("cached trees = %d, want 3 (root, a, a/sub)", stats.TreesCached)
	}
}
