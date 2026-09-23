package blockpack

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func sampleObjects() []Object {
	return []Object{
		{Type: "meta", Data: []byte("root object"), Refs: []uint32{1, 2}},
		{Type: "blob", Data: []byte("hello"), Refs: []uint32{3}},
		{Type: "blob", Data: []byte("world"), Refs: nil},
		{Type: "tree", Data: bytes.Repeat([]byte{0xAB}, 100), Refs: []uint32{0}},
		{Type: "blob", Data: []byte("tail"), Refs: []uint32{2}},
	}
}

func buildSample(t *testing.T) *Package {
	t.Helper()
	p, err := Build(1, 2, sampleObjects())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return p
}

func verifyStream(t *testing.T, p *Package, blocks []Block) *Verifier {
	t.Helper()
	v := NewVerifier(p.Header, p.Header.Root)
	for _, b := range blocks {
		if err := v.AddBlock(b); err != nil {
			t.Fatalf("AddBlock(%d): %v", b.Index, err)
		}
	}
	return v
}

func TestRoundTripStreamVerify(t *testing.T) {
	p := buildSample(t)
	wire := Serialize(p)
	q, err := Parse(wire)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if q.Header != p.Header {
		t.Fatalf("header mismatch after parse")
	}

	v := NewVerifier(q.Header, q.Header.Root)
	for i, b := range q.Blocks {
		if v.Complete() {
			t.Fatalf("verifier declared completeness before finish")
		}
		if got := v.LocallyVerified(); got != i {
			t.Fatalf("LocallyVerified=%d, want %d", got, i)
		}
		if err := v.AddBlock(b); err != nil {
			t.Fatalf("AddBlock(%d): %v", i, err)
		}
	}
	if got := v.LocallyVerified(); got != len(q.Blocks) {
		t.Fatalf("LocallyVerified=%d, want %d", got, len(q.Blocks))
	}
	if v.Complete() {
		t.Fatalf("completeness declared before Finish")
	}
	if err := v.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if !v.Complete() {
		t.Fatalf("not complete after successful Finish")
	}
}

func TestDeleteLastBlockFails(t *testing.T) {
	p := buildSample(t)
	// All blocks but the last one verify locally...
	v := verifyStream(t, p, p.Blocks[:len(p.Blocks)-1])
	if got := v.LocallyVerified(); got != len(p.Blocks)-1 {
		t.Fatalf("LocallyVerified=%d, want %d", got, len(p.Blocks)-1)
	}
	// ...but completeness must fail: the last block is missing.
	if err := v.Finish(); err == nil {
		t.Fatalf("Finish succeeded with last block deleted")
	}
	if v.Complete() {
		t.Fatalf("completeness declared despite missing last block")
	}
}

func TestReorderBlocksFails(t *testing.T) {
	p := buildSample(t)
	blocks := append([]Block(nil), p.Blocks...)
	blocks[0], blocks[1] = blocks[1], blocks[0]
	v := NewVerifier(p.Header, p.Header.Root)
	if err := v.AddBlock(blocks[0]); err == nil {
		t.Fatalf("reordered block accepted")
	}
}

func TestTamperedObjectFails(t *testing.T) {
	p := buildSample(t)
	b := p.Blocks[0]
	b.Objects = append([]Object(nil), b.Objects...)
	b.Objects[0].Data = []byte("forged")
	v := NewVerifier(p.Header, p.Header.Root)
	if err := v.AddBlock(b); err == nil {
		t.Fatalf("tampered block accepted")
	}
}

func TestMissingObjectNotMasked(t *testing.T) {
	p := buildSample(t)
	// Attacker drops an object from the last block; the block digest no
	// longer matches, so local evidence cannot mask the missing object.
	b := p.Blocks[len(p.Blocks)-1]
	b.Objects = b.Objects[:len(b.Objects)-1]
	v := verifyStream(t, p, p.Blocks[:len(p.Blocks)-1])
	if err := v.AddBlock(b); err == nil {
		t.Fatalf("block with missing object accepted")
	}
	if err := v.Finish(); err == nil {
		t.Fatalf("Finish succeeded with a missing object")
	}
}

func TestForeignSummaryRootRejected(t *testing.T) {
	p := buildSample(t)
	q, err := Build(2, 2, sampleObjects())
	if err != nil {
		t.Fatalf("Build v2: %v", err)
	}
	// A block from version 2 must not verify under version 1's root.
	v := NewVerifier(p.Header, p.Header.Root)
	if err := v.AddBlock(q.Blocks[0]); err == nil {
		t.Fatalf("block from another version accepted")
	}
}

func TestRangeProof(t *testing.T) {
	p := buildSample(t)
	proof, err := p.ProveRange(1, 3)
	if err != nil {
		t.Fatalf("ProveRange: %v", err)
	}
	if err := proof.Verify(p.Header.Root); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestRangeProofRejectsTamperedBlock(t *testing.T) {
	p := buildSample(t)
	proof, err := p.ProveRange(0, 2)
	if err != nil {
		t.Fatalf("ProveRange: %v", err)
	}
	proof.Blocks[1].Objects[0].Data = []byte("forged")
	if err := proof.Verify(p.Header.Root); err == nil {
		t.Fatalf("tampered range proof accepted")
	}
}

func TestOldProofCannotSpliceIntoNewPackage(t *testing.T) {
	oldPkg, err := Build(1, 2, sampleObjects())
	if err != nil {
		t.Fatalf("Build v1: %v", err)
	}
	objs := sampleObjects()
	objs[4].Data = []byte("tail v2")
	newPkg, err := Build(2, 2, objs)
	if err != nil {
		t.Fatalf("Build v2: %v", err)
	}
	oldProof, err := oldPkg.ProveRange(0, 2)
	if err != nil {
		t.Fatalf("ProveRange: %v", err)
	}
	// Splicing the old proof into the new package must fail.
	if err := oldProof.Verify(newPkg.Header.Root); err == nil {
		t.Fatalf("old proof accepted for new package")
	}
	// And the new package's own proof verifies fine.
	newProof, err := newPkg.ProveRange(0, 2)
	if err != nil {
		t.Fatalf("ProveRange v2: %v", err)
	}
	if err := newProof.Verify(newPkg.Header.Root); err != nil {
		t.Fatalf("new proof rejected: %v", err)
	}
}

func TestCacheVersionIsolation(t *testing.T) {
	c := NewCache()
	v1, err := Build(1, 2, sampleObjects())
	if err != nil {
		t.Fatalf("Build v1: %v", err)
	}
	for _, b := range v1.Blocks {
		c.Store(1, b.Index, b.Digest)
	}
	// Same version, same committed digest: cache hit.
	if !c.Lookup(1, 0, v1.Blocks[0].Digest) {
		t.Fatalf("cache miss for verified v1 block")
	}
	// A different committed digest under the same key: miss, forcing
	// re-verification instead of blind reuse.
	forged := v1.Blocks[0].Digest
	forged[0] ^= 0xFF
	if c.Lookup(1, 0, forged) {
		t.Fatalf("cache honored a digest the package does not commit to")
	}
	// Old-version entries are isolated from a new version of the package.
	objs := sampleObjects()
	objs[0].Data = []byte("root object v2")
	v2, err := Build(2, 2, objs)
	if err != nil {
		t.Fatalf("Build v2: %v", err)
	}
	for _, b := range v2.Blocks {
		if c.Lookup(2, b.Index, b.Digest) {
			t.Fatalf("old-version cache entry reused for v2 block %d", b.Index)
		}
	}
}

func TestTamperManifestOnSubset(t *testing.T) {
	p := buildSample(t)
	// Corrupt blocks 1 and 2, leave 0 intact.
	blocks := append([]Block(nil), p.Blocks...)
	blocks[1].Objects = append([]Object(nil), blocks[1].Objects...)
	blocks[1].Objects[0].Data = []byte("forged-1")
	blocks[2].Objects = append([]Object(nil), blocks[2].Objects...)
	blocks[2].Objects[0].Refs = []uint32{99}

	failed := VerifySubset(p.Header.Root, []Block{blocks[0], blocks[1], blocks[2]})
	if !MatchTamperManifest(failed, []int{1, 2}) {
		t.Fatalf("failed=%v, want manifest [1 2]", failed)
	}
	if MatchTamperManifest(failed, []int{1}) {
		t.Fatalf("partial manifest accepted")
	}
	// Arbitrary subset: only block 0, which is intact.
	if failed := VerifySubset(p.Header.Root, []Block{blocks[0]}); len(failed) != 0 {
		t.Fatalf("intact subset reported failures: %v", failed)
	}
}

func TestLengthCheckedBeforeAllocation(t *testing.T) {
	// A tiny buffer claiming a gigantic data length must be rejected from
	// the length prefix alone, without allocating the claimed size.
	var buf [8]byte
	binary.LittleEndian.PutUint32(buf[0:4], 3)          // type len
	binary.LittleEndian.PutUint32(buf[4:8], 0xFFFFFFFF) // absurd data len
	copy(buf[4:8], u32(0xFFFFFFFF))
	if _, _, err := DecodeObject(buf[:]); err == nil {
		t.Fatalf("oversized length prefix accepted")
	}

	// Same at the package level: valid header, then a block whose first
	// object claims MaxObjectEnc+1 bytes.
	p := buildSample(t)
	wire := Serialize(p)
	// Truncate everything after the first block header and corrupt the
	// first object length to be huge.
	hdrLen := len(magic) + 16 + 32
	blkHdr := hdrLen + 8 + 64
	trunc := append([]byte(nil), wire[:blkHdr+4]...)
	binary.LittleEndian.PutUint32(trunc[blkHdr:blkHdr+4], 0xFFFFFFFF)
	if _, err := Parse(trunc); err == nil {
		t.Fatalf("oversized object length accepted by Parse")
	}
}

func TestParseRejectsTrailingAndTruncated(t *testing.T) {
	p := buildSample(t)
	wire := Serialize(p)
	if _, err := Parse(wire[:len(wire)-3]); err == nil {
		t.Fatalf("truncated package accepted")
	}
	if _, err := Parse(append(wire, 0x00)); err == nil {
		t.Fatalf("package with trailing garbage accepted")
	}
}

func TestRefGraphCommitted(t *testing.T) {
	objs := sampleObjects()
	p1, err := Build(1, 2, objs)
	if err != nil {
		t.Fatalf("Build p1: %v", err)
	}
	objs2 := sampleObjects()
	objs2[0].Refs = []uint32{2, 1} // same objects, different reference graph
	p2, err := Build(1, 2, objs2)
	if err != nil {
		t.Fatalf("Build p2: %v", err)
	}
	if p1.Header.Root == p2.Header.Root {
		t.Fatalf("root does not commit to the reference graph")
	}
}
