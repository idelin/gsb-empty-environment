package blkidx

import (
	"encoding/binary"
	"hash/crc32"
	"testing"
)

func cloneChildren(c []ChildDesc) []ChildDesc {
	out := make([]ChildDesc, len(c))
	copy(out, c)
	return out
}

func cloneMembershipProof(p *MembershipProof) *MembershipProof {
	q := *p
	q.Value = append([]byte(nil), p.Value...)
	q.Leaf = append([]byte(nil), p.Leaf...)
	q.Path = make([]PathLevel, len(p.Path))
	for i, lvl := range p.Path {
		q.Path[i] = lvl
		q.Path[i].Children = cloneChildren(lvl.Children)
	}
	return &q
}

// TestProofTampering enumerates small mutations of a valid membership
// proof; every one must be rejected.
func TestProofTampering(t *testing.T) {
	entries := makeEntries(2000)
	ix, err := Build(entries, Config{Version: 5, LeafCap: 16, Fanout: 8})
	if err != nil {
		t.Fatal(err)
	}
	root := ix.RootHash()
	key := entries[1000].Key
	valid := ix.ProveMembership(key)
	if _, verr := VerifyMembership(root, valid, nil); verr != nil {
		t.Fatalf("valid proof rejected: %v", verr)
	}

	type mutation struct {
		name string
		fn   func(p *MembershipProof)
	}
	mutations := []mutation{
		{"value byte flipped", func(p *MembershipProof) { p.Value[0] ^= 0x01 }},
		{"rank incremented", func(p *MembershipProof) { p.Rank++ }},
		{"leaf entries region flipped", func(p *MembershipProof) { p.Leaf[LeafHeaderSize+1] ^= 0x40 }},
		{"leaf header version flipped (crc fixed)", func(p *MembershipProof) {
			binary.LittleEndian.PutUint32(p.Leaf[2:6], 6)
			binary.LittleEndian.PutUint32(p.Leaf[30:34], crc32.ChecksumIEEE(p.Leaf[0:30]))
		}},
		{"leaf header count flipped (crc fixed)", func(p *MembershipProof) {
			c := binary.LittleEndian.Uint32(p.Leaf[10:14])
			binary.LittleEndian.PutUint32(p.Leaf[10:14], c+1)
			binary.LittleEndian.PutUint32(p.Leaf[30:34], crc32.ChecksumIEEE(p.Leaf[0:30]))
		}},
		{"leaf header base offset flipped (crc fixed)", func(p *MembershipProof) {
			binary.LittleEndian.PutUint64(p.Leaf[14:22], 999)
			binary.LittleEndian.PutUint32(p.Leaf[30:34], crc32.ChecksumIEEE(p.Leaf[0:30]))
		}},
		{"internal child count tampered", func(p *MembershipProof) {
			lvl := &p.Path[0]
			i := (lvl.ChildIdx + 1) % len(lvl.Children)
			lvl.Children[i].Count += 3
		}},
		{"internal child max key tampered", func(p *MembershipProof) {
			lvl := &p.Path[0]
			i := (lvl.ChildIdx + 1) % len(lvl.Children)
			lvl.Children[i].MaxKey += "z"
		}},
		{"internal child base offset tampered", func(p *MembershipProof) {
			lvl := &p.Path[0]
			i := (lvl.ChildIdx + 1) % len(lvl.Children)
			lvl.Children[i].BaseOff++
		}},
		{"internal version tampered", func(p *MembershipProof) { p.Path[0].Version++ }},
		{"internal node id tampered", func(p *MembershipProof) { p.Path[0].NodeID += 77 }},
		{"child order swapped", func(p *MembershipProof) {
			lvl := &p.Path[len(p.Path)-1]
			n := len(lvl.Children)
			lvl.Children[0], lvl.Children[n-1] = lvl.Children[n-1], lvl.Children[0]
		}},
		{"child hash flipped", func(p *MembershipProof) {
			lvl := &p.Path[0]
			i := (lvl.ChildIdx + 1) % len(lvl.Children)
			lvl.Children[i].Hash[0] ^= 0x01
		}},
		{"child index rerouted", func(p *MembershipProof) {
			lvl := &p.Path[0]
			lvl.ChildIdx = (lvl.ChildIdx + 1) % len(lvl.Children)
		}},
		{"path level dropped", func(p *MembershipProof) {
			if len(p.Path) > 1 {
				p.Path = append(p.Path[:1], p.Path[2:]...)
			} else {
				p.Path = nil
			}
		}},
		{"leaf swapped for another block", func(p *MembershipProof) {
			other := ix.ProveMembership(entries[1500].Key)
			p.Leaf = other.Leaf
		}},
		{"found flag negated", func(p *MembershipProof) { p.Found = false }},
	}
	for _, m := range mutations {
		p := cloneMembershipProof(valid)
		m.fn(p)
		if _, verr := VerifyMembership(root, p, nil); verr == nil {
			t.Fatalf("tamper %q accepted", m.name)
		} else {
			t.Logf("tamper %-42q -> %v", m.name, verr)
		}
	}
}

// TestMetadataOnlyTamper: corrupting only metadata (counts, offsets,
// versions) inside the prover's index must still produce proofs that fail
// against the originally committed root, localized to the tampered node.
func TestMetadataOnlyTamper(t *testing.T) {
	entries := makeEntries(2000)
	cfg := Config{Version: 9, LeafCap: 16, Fanout: 8}
	ix, err := Build(entries, cfg)
	if err != nil {
		t.Fatal(err)
	}
	root := ix.RootHash()
	key := entries[1000].Key

	// 1. Tamper a count inside an internal summary node.
	leafIdx, _, _ := ix.locate(key)
	parentID := ix.leafParent[leafIdx]
	slot := ix.leafSlot[leafIdx]
	other := (slot + 1) % len(ix.nodes[parentID].Children)
	ix.nodes[parentID].Children[other].Count++
	p := ix.ProveMembership(key)
	_, verr := VerifyMembership(root, p, nil)
	if verr == nil {
		t.Fatal("internal count tamper accepted")
	}
	if verr.IsLeaf || verr.NodeID != parentID {
		t.Fatalf("count tamper localized to %+v, want internal node %d", verr, parentID)
	}
	t.Logf("internal count tamper -> %v", verr)

	// 2. Tamper only the version of an internal node.
	ix2, _ := Build(entries, cfg)
	nid := ix2.leafParent[leafIdx]
	ix2.nodes[nid].Version++
	p2 := ix2.ProveMembership(key)
	if _, verr := VerifyMembership(root, p2, nil); verr == nil {
		t.Fatal("internal version tamper accepted")
	} else if verr.IsLeaf || verr.NodeID != nid {
		t.Fatalf("version tamper localized to %+v, want internal node %d", verr, nid)
	}

	// 3. Tamper only the base offset inside a leaf header (crc fixed).
	ix3, _ := Build(entries, cfg)
	blk := append([]byte(nil), ix3.leaves[leafIdx]...)
	binary.LittleEndian.PutUint64(blk[14:22], 123456)
	binary.LittleEndian.PutUint32(blk[30:34], crc32.ChecksumIEEE(blk[0:30]))
	ix3.leaves[leafIdx] = blk
	p3 := ix3.ProveMembership(key)
	if _, verr := VerifyMembership(root, p3, nil); verr == nil {
		t.Fatal("leaf base-offset tamper accepted")
	} else if !verr.IsLeaf || verr.NodeID != uint32(leafIdx) {
		t.Fatalf("leaf metadata tamper localized to %+v, want leaf %d", verr, leafIdx)
	}
}

// TestErrorLocalization: corrupt a specific leaf's content after commit;
// the verifier must point at exactly that block, the smallest bad unit.
func TestErrorLocalization(t *testing.T) {
	entries := makeEntries(2000)
	cfg := Config{Version: 1, LeafCap: 16, Fanout: 8}
	ix, err := Build(entries, cfg)
	if err != nil {
		t.Fatal(err)
	}
	root := ix.RootHash()

	// Corrupt one value inside leaf 40, keeping a valid encoding.
	target := 40
	hdr, ents, err := decodeLeaf(ix.leaves[target])
	if err != nil {
		t.Fatal(err)
	}
	ents[3].Value = append([]byte(nil), ents[3].Value...)
	ents[3].Value[0] ^= 0xFF
	ix.leaves[target] = encodeLeaf(hdr.Version, hdr.BlockID, hdr.BaseOff, ents)

	// A proof for a key in the corrupted block must fail at that block.
	badKey := entries[int(hdr.BaseOff)].Key
	p := ix.ProveMembership(badKey)
	_, verr := VerifyMembership(root, p, nil)
	if verr == nil {
		t.Fatal("corrupted block accepted")
	}
	if !verr.IsLeaf || verr.NodeID != uint32(target) {
		t.Fatalf("localized to %+v, want leaf block %d", verr, target)
	}
	t.Logf("corrupted leaf content -> %v", verr)

	// Proofs for keys in untouched blocks still verify.
	other := entries[0]
	if int(ix.leafDesc[0].BaseOff/16) == target {
		other = entries[100]
	}
	if _, verr := VerifyMembership(root, ix.ProveMembership(other.Key), nil); verr != nil {
		t.Fatalf("honest block rejected: %v", verr)
	}
}

// TestRangeOmissionAttacks: a query certificate must exclude omitted
// blocks, not merely prove the returned items exist.
func TestRangeOmissionAttacks(t *testing.T) {
	entries := makeEntries(1000)
	ix, err := Build(entries, Config{Version: 1, LeafCap: 16, Fanout: 8})
	if err != nil {
		t.Fatal(err)
	}
	root := ix.RootHash()
	lo, hi := entries[200].Key, entries[399].Key
	valid := ix.ProveRange(lo, hi)
	if len(valid.Blocks) < 5 {
		t.Fatalf("test wants a multi-block certificate, got %d blocks", len(valid.Blocks))
	}
	if _, verr := VerifyRange(root, valid, nil); verr != nil {
		t.Fatalf("valid certificate rejected: %v", verr)
	}

	clone := func() *RangeProof {
		q := *valid
		q.Blocks = append([][]byte(nil), valid.Blocks...)
		q.Paths = make([][]PathLevel, len(valid.Paths))
		copy(q.Paths, valid.Paths)
		return &q
	}

	// Drop a middle block: the verifier must notice the gap.
	mid := len(valid.Blocks) / 2
	p := clone()
	p.Blocks = append(p.Blocks[:mid], p.Blocks[mid+1:]...)
	p.Paths = append(p.Paths[:mid], p.Paths[mid+1:]...)
	if _, verr := VerifyRange(root, p, nil); verr == nil {
		t.Fatal("omitted middle block accepted")
	} else {
		t.Logf("middle block omitted -> %v", verr)
	}

	// Drop the first (predecessor) block.
	p = clone()
	p.Blocks = p.Blocks[1:]
	p.Paths = p.Paths[1:]
	if _, verr := VerifyRange(root, p, nil); verr == nil {
		t.Fatal("omitted predecessor block accepted")
	} else {
		t.Logf("predecessor block omitted -> %v", verr)
	}

	// Drop the last (successor) block.
	p = clone()
	p.Blocks = p.Blocks[:len(p.Blocks)-1]
	p.Paths = p.Paths[:len(p.Paths)-1]
	if _, verr := VerifyRange(root, p, nil); verr == nil {
		t.Fatal("omitted successor block accepted")
	} else {
		t.Logf("successor block omitted -> %v", verr)
	}

	// Duplicate a block.
	p = clone()
	p.Blocks = append(p.Blocks[:mid], append([][]byte{p.Blocks[mid]}, p.Blocks[mid:]...)...)
	p.Paths = append(p.Paths[:mid], append([][]PathLevel{p.Paths[mid]}, p.Paths[mid:]...)...)
	if _, verr := VerifyRange(root, p, nil); verr == nil {
		t.Fatal("duplicated block accepted")
	}

	// Swap two adjacent blocks.
	p = clone()
	p.Blocks[mid], p.Blocks[mid+1] = p.Blocks[mid+1], p.Blocks[mid]
	p.Paths[mid], p.Paths[mid+1] = p.Paths[mid+1], p.Paths[mid]
	if _, verr := VerifyRange(root, p, nil); verr == nil {
		t.Fatal("swapped blocks accepted")
	}

	// Replace a block with a valid block from elsewhere in the index.
	p = clone()
	other := ix.ProveMembership(entries[900].Key)
	p.Blocks[mid] = other.Leaf
	if _, verr := VerifyRange(root, p, nil); verr == nil {
		t.Fatal("substituted block accepted")
	}
}

// TestVerifierReadsOnlyProofBlocks: the verifier must not need data blocks
// beyond those carried in the proof.
func TestVerifierReadsOnlyProofBlocks(t *testing.T) {
	entries := makeEntries(5000)
	ix, err := Build(entries, Config{Version: 1, LeafCap: 16, Fanout: 8})
	if err != nil {
		t.Fatal(err)
	}
	root := ix.RootHash()

	// Membership: exactly the one involved leaf is read.
	p := ix.ProveMembership(entries[2500].Key)
	var stats VerifyStats
	if _, verr := VerifyMembership(root, p, &stats); verr != nil {
		t.Fatal(verr)
	}
	wantLeaf, _, _ := ix.locate(entries[2500].Key)
	if len(stats.LeavesRead) != 1 || stats.LeavesRead[0] != uint32(wantLeaf) {
		t.Fatalf("verifier read leaves %v, want exactly [%d]", stats.LeavesRead, wantLeaf)
	}

	// Range: exactly the blocks of the certificate, nothing else.
	rp := ix.ProveRange(entries[1000].Key, entries[1099].Key)
	stats.reset()
	if _, verr := VerifyRange(root, rp, &stats); verr != nil {
		t.Fatal(verr)
	}
	if len(stats.LeavesRead) != len(rp.Blocks) {
		t.Fatalf("verifier read %d leaves for a %d-block certificate", len(stats.LeavesRead), len(rp.Blocks))
	}
	seen := map[uint32]bool{}
	for _, id := range stats.LeavesRead {
		if seen[id] {
			t.Fatalf("leaf %d read twice", id)
		}
		seen[id] = true
	}
	t.Logf("membership: 1 leaf + %d nodes; range: %d leaves + %d nodes (index has %d leaves)",
		ix.Depth(), len(stats.LeavesRead), len(stats.NodesRead), len(ix.leaves))
}
