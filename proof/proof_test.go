package proof

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// memFetcher serves package payload chunks in memory; tests can tamper with
// data or hide chunks.
type memFetcher struct {
	m       *Manifest
	payload []byte
	hidden  map[uint32]bool
}

func newMemFetcher(p *Package) *memFetcher {
	return &memFetcher{m: p.Manifest, payload: p.Payload, hidden: map[uint32]bool{}}
}

func (f *memFetcher) FetchChunk(index uint32) ([]byte, error) {
	if f.hidden[index] {
		return nil, fmt.Errorf("chunk %d unavailable", index)
	}
	if int(index) >= len(f.m.Chunks) {
		return nil, ErrChunkIndex
	}
	var off uint64
	for _, c := range f.m.Chunks[:index] {
		off += uint64(c.Bytes)
	}
	n := f.m.Chunks[index].Bytes
	out := make([]byte, n)
	copy(out, f.payload[off:off+uint64(n)])
	return out, nil
}

func samplePackage(t *testing.T) *Package {
	t.Helper()
	objs := []Object{
		{Kind: "blob", Name: "alpha", Payload: bytes.Repeat([]byte("A"), 100)},
		{Kind: "blob", Name: "beta", Refs: []uint32{0}, Payload: bytes.Repeat([]byte("B"), 200)},
		{Kind: "tree", Name: "gamma", Refs: []uint32{0, 1}, Payload: []byte("ROOT-REFS")},
	}
	p, err := Build("pkg-1", 7, 64, objs) // small chunks -> many pieces
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return p
}

func feedAll(t *testing.T, v *Verifier, f ChunkFetcher, cache *ChunkCache) {
	t.Helper()
	for i := 0; i < v.ChunkCount(); i++ {
		if _, _, err := ObtainChunk(v, f, cache, uint32(i)); err != nil {
			t.Fatalf("chunk %d: %v", i, err)
		}
	}
}

func TestHappyPathFinishComplete(t *testing.T) {
	p := samplePackage(t)
	v, err := NewVerifier(p.Manifest, p.Root)
	if err != nil {
		t.Fatal(err)
	}
	if v.Status() != StatusLocal {
		t.Fatalf("new verifier must be local, got %s", v.Status())
	}
	if err := v.MustBeComplete(); !errors.Is(err, ErrNotComplete) {
		t.Fatalf("before finish expected ErrNotComplete, got %v", err)
	}
	feedAll(t, v, newMemFetcher(p), nil)
	rep, err := v.Finish()
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	if rep.Status != StatusComplete || rep.ChunksSeen != rep.ChunksTotal ||
		rep.ObjectSeen != rep.ObjectsTotal {
		t.Fatalf("bad report %+v", rep)
	}
	if err := v.MustBeComplete(); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteLastChunkFails(t *testing.T) {
	p := samplePackage(t)
	f := newMemFetcher(p)
	f.hidden[uint32(len(p.Manifest.Chunks)-1)] = true
	v, _ := NewVerifier(p.Manifest, p.Root)
	for i := 0; i < v.ChunkCount()-1; i++ {
		if _, _, err := ObtainChunk(v, f, nil, uint32(i)); err != nil {
			t.Fatal(err)
		}
	}
	if v.Status() != StatusLocal {
		t.Fatal("must remain local")
	}
	if _, err := v.Finish(); !errors.Is(err, ErrNotAllChunks) {
		t.Fatalf("expected ErrNotAllChunks, got %v", err)
	}
}

func TestChunkSwapFails(t *testing.T) {
	p := samplePackage(t)
	if len(p.Manifest.Chunks) < 3 {
		t.Fatal("need >=3 chunks")
	}
	f := newMemFetcher(p)
	swap := &swapFetcher{inner: f, a: 1, b: 2}
	v, _ := NewVerifier(p.Manifest, p.Root)
	// swap.FetchChunk(1) returns the bytes of chunk 2.
	if err := v.AcceptChunk(1, mustChunk(swap, 1)); !errors.Is(err, ErrChunkHash) {
		t.Fatalf("swapped chunk must fail hash, got %v", err)
	}
	// Reordering content is not allowed: bytes of chunk 2 under index 1 fail.
	b2, _ := f.FetchChunk(2)
	if err := v.AcceptChunk(1, b2); !errors.Is(err, ErrChunkHash) {
		t.Fatalf("cross-index bytes must fail, got %v", err)
	}
	// Genuine chunk 1 still verifies after the failed attempts.
	b1, _ := f.FetchChunk(1)
	if err := v.AcceptChunk(1, b1); err != nil {
		t.Fatalf("genuine chunk 1 must verify, got %v", err)
	}
}

type swapFetcher struct {
	inner *memFetcher
	a, b  uint32
}

func (s *swapFetcher) FetchChunk(index uint32) ([]byte, error) {
	if index == s.a {
		index = s.b
	} else if index == s.b {
		index = s.a
	}
	return s.inner.FetchChunk(index)
}

func mustChunk(f ChunkFetcher, i uint32) []byte {
	b, err := f.FetchChunk(i)
	if err != nil {
		panic(err)
	}
	return b
}

func TestTamperedChunkBytesFail(t *testing.T) {
	p := samplePackage(t)
	f := newMemFetcher(p)
	b, _ := f.FetchChunk(0)
	b[0] ^= 0xFF
	v, _ := NewVerifier(p.Manifest, p.Root)
	if err := v.AcceptChunk(0, b); !errors.Is(err, ErrChunkHash) {
		t.Fatalf("expected hash error, got %v", err)
	}
}

func TestRootSummaryCommitsOrderAndGraph(t *testing.T) {
	p := samplePackage(t)
	// Tamper manifest: swap two object descriptors but keep payload; recompute
	// geometry-consistent manifest. Root must differ, verifier must reject.
	m2 := cloneManifest(p.Manifest)
	m2.Objects[0], m2.Objects[1] = m2.Objects[1], m2.Objects[0]
	// Geometry validation should catch order break; even root alone differs.
	if RootSummaryOf(m2) == p.Root {
		t.Fatal("root must change when object order changes")
	}
	if _, err := NewVerifier(m2, p.Root); err == nil {
		t.Fatal("verifier accepted reordered manifest under old root")
	}

	// Drop an object that is still referenced -> graph not closed.
	m3 := cloneManifest(p.Manifest)
	m3.Objects = m3.Objects[:2] // gamma(2) removed; beta refs 0, gamma refs...
	if _, err := NewVerifier(m3, p.Root); err == nil {
		t.Fatal("verifier accepted manifest with dangling refs")
	}
}

func TestMissingNecessaryObjectCannotBeMasked(t *testing.T) {
	p := samplePackage(t)
	// Claim fewer objects in manifest but keep full payload: geometry fails
	// because object coverage disagrees with chunk payload length.
	m2 := cloneManifest(p.Manifest)
	m2.Objects = m2.Objects[:2]
	if err := m2.Validate(); err == nil {
		t.Fatal("partial object list over full payload must not validate")
	}
}

func TestTamperedManifestWrongRoot(t *testing.T) {
	p := samplePackage(t)
	tampered := cloneManifest(p.Manifest)
	tampered.PkgID = "pkg-EVIL"
	// encode roundtrip must differ; verifier with the ORIGINAL trusted root
	// rejects it.
	if _, err := NewVerifier(tampered, p.Root); !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("expected root mismatch, got %v", err)
	}
}

func TestOldProofCannotSpliceNewPackage(t *testing.T) {
	p := samplePackage(t)
	newPkg, err := Build("pkg-1", 8, 64, []Object{
		{Kind: "blob", Name: "alpha", Payload: bytes.Repeat([]byte("A"), 100)},
		{Kind: "blob", Name: "beta", Refs: []uint32{0}, Payload: bytes.Repeat([]byte("B"), 200)},
		{Kind: "tree", Name: "gamma", Refs: []uint32{0, 1}, Payload: []byte("ROOT-REFS")},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Old root (version 7) must not verify the new manifest (version 8), even
	// with identical content.
	if _, err := NewVerifier(newPkg.Manifest, p.Root); !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("old root on new version: expected mismatch, got %v", err)
	}
	if p.Root == newPkg.Root {
		t.Fatal("roots for different versions must differ")
	}
}

func TestArbitrarySubsetRangeRead(t *testing.T) {
	p := samplePackage(t)
	v, _ := NewVerifier(p.Manifest, p.Root)
	f := newMemFetcher(p)
	off := uint64(50)
	n := uint64(130)
	got, ev, err := ReadRange(v, f, nil, off, n)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, p.Payload[off:off+n]) {
		t.Fatal("range bytes mismatch")
	}
	if len(ev.Chunks) == 0 {
		t.Fatal("expected chunk evidence")
	}
	if v.Status() != StatusLocal {
		t.Fatal("subset read must stay local")
	}
	if err := v.MustBeComplete(); !errors.Is(err, ErrNotComplete) {
		t.Fatalf("expected not complete, got %v", err)
	}
	// Reading object 0 should succeed from the already-seen first chunks.
	obj0, _, err := ReadObject(v, f, nil, 0)
	if err != nil {
		t.Fatalf("read object 0: %v", err)
	}
	if string(obj0.Payload) != strings.Repeat("A", 100) {
		t.Fatal("object 0 payload wrong")
	}
	// Whole package not declared complete yet.
	if _, err := v.Finish(); !errors.Is(err, ErrNotAllChunks) {
		t.Fatalf("expected missing chunks, got %v", err)
	}
}

func TestLocalObjectEvidencePartial(t *testing.T) {
	p := samplePackage(t)
	v, _ := NewVerifier(p.Manifest, p.Root)
	f := newMemFetcher(p)
	// Verify only object 0's chunks explicitly.
	o0 := p.Manifest.Objects[0]
	cs := uint64(p.Manifest.ChunkSize)
	last := (o0.Offset + uint64(o0.Length) - 1) / cs
	for i := uint64(0); i <= last; i++ {
		if _, _, err := ObtainChunk(v, f, nil, uint32(i)); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := v.VerifiedObjectIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != 0 {
		t.Fatalf("only object 0 should be locally verified, got %v", ids)
	}
	if v.Status() != StatusLocal {
		t.Fatal("must remain local")
	}
}

func cloneManifest(m *Manifest) *Manifest {
	b := m.EncodeCanonical()
	m2, err := DecodeManifest(ScopeManifest, b)
	if err != nil {
		panic(err)
	}
	return m2
}
