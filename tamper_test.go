package blockindex

import (
	"fmt"
	"testing"
)

func cloneProof(p *BlockProof) *BlockProof {
	q := &BlockProof{
		Version:    p.Version,
		BlockIndex: p.BlockIndex,
		Block:      append([]byte(nil), p.Block...),
	}
	for _, pn := range p.Path {
		q.Path = append(q.Path, PathNode{
			Version:    pn.Version,
			Level:      pn.Level,
			Children:   append([]ChildInfo(nil), pn.Children...),
			ChildIndex: pn.ChildIndex,
		})
	}
	return q
}

// TestTamperPointProofs builds a small index and enumerates tamperings of a
// membership proof: path digests, aggregate counts (metadata only), ranges,
// versions, child order, block content and position. Every single one must
// be rejected.
func TestTamperPointProofs(t *testing.T) {
	idx, entries := buildSmall(t, 4, 5, 4, 4, 2) // 20 entries, fanout 2, height 4
	root := idx.Root
	key := entries[7].Key
	base := idx.Prove(key)
	if res, err := VerifyMembership(root, key, base); err != nil || !res.Member {
		t.Fatalf("base proof invalid: %v", err)
	}

	tampered := map[string]*BlockProof{}

	// Path: flip a digest byte at every level.
	for lvl := range base.Path {
		p := cloneProof(base)
		sib := (p.Path[lvl].ChildIndex + 1) % len(p.Path[lvl].Children)
		p.Path[lvl].Children[sib].Digest[0] ^= 0xff
		tampered[fmt.Sprintf("digest flip level %d", lvl)] = p
	}
	// Counts: metadata-only tampering of entry and block counts.
	for lvl := range base.Path {
		p := cloneProof(base)
		sib := (p.Path[lvl].ChildIndex + 1) % len(p.Path[lvl].Children)
		p.Path[lvl].Children[sib].Entries++
		tampered[fmt.Sprintf("sibling entries++ level %d", lvl)] = p

		p = cloneProof(base)
		p.Path[lvl].Children[sib].Blocks++
		tampered[fmt.Sprintf("sibling blocks++ level %d", lvl)] = p

		p = cloneProof(base)
		p.Path[lvl].Children[p.Path[lvl].ChildIndex].Entries--
		tampered[fmt.Sprintf("on-path entries-- level %d", lvl)] = p
	}
	// Ranges: extend a sibling's committed range.
	p := cloneProof(base)
	sib := (p.Path[0].ChildIndex + 1) % len(p.Path[0].Children)
	p.Path[0].Children[sib].MaxKey += 100
	tampered["sibling maxKey extended"] = p

	p = cloneProof(base)
	if p.Path[0].ChildIndex+1 < len(p.Path[0].Children) {
		p.Path[0].Children[p.Path[0].ChildIndex+1].MinKey = key
		tampered["successor minKey lowered onto key"] = p
	}
	// Order: swap two children of the root-most node.
	p = cloneProof(base)
	top := len(p.Path) - 1
	if len(p.Path[top].Children) >= 2 {
		p.Path[top].Children[0], p.Path[top].Children[1] = p.Path[top].Children[1], p.Path[top].Children[0]
		tampered["child order swapped"] = p
	}
	// Versions: proof, path node, and raw block version byte.
	p = cloneProof(base)
	p.Version++
	tampered["proof version bumped"] = p

	p = cloneProof(base)
	p.Path[0].Version++
	tampered["path node version bumped"] = p

	p = cloneProof(base)
	p.Block[4]++ // version varint of the block encoding
	tampered["block version byte flipped"] = p

	// Content: flip a value byte inside the block.
	p = cloneProof(base)
	p.Block[len(p.Block)-1] ^= 0x01
	tampered["block value byte flipped"] = p

	// Content: re-encode the block with one key changed.
	_, es, err := DecodeBlock(base.Block)
	if err != nil {
		t.Fatal(err)
	}
	es[1].Key = es[0].Key + 1
	if es[1].Key == es[2].Key {
		es[1].Key--
	}
	p = cloneProof(base)
	p.Block = EncodeBlock(root.Version, es)
	tampered["block re-encoded with shifted key"] = p

	// Counts: raw block count varint corrupted (metadata only).
	p = cloneProof(base)
	p.Block[5]++
	tampered["block count byte bumped"] = p

	// Ranges: on-path child min key moved (metadata only).
	p = cloneProof(base)
	p.Path[0].Children[p.Path[0].ChildIndex].MinKey++
	tampered["on-path minKey shifted"] = p

	// Position: claimed block index no longer matches the committed one.
	p = cloneProof(base)
	p.BlockIndex++
	tampered["block index incremented"] = p

	// Structure: truncated and extended paths.
	p = cloneProof(base)
	p.Path = p.Path[:len(p.Path)-1]
	tampered["path truncated"] = p

	p = cloneProof(base)
	p.Path = append(p.Path, p.Path[len(p.Path)-1])
	tampered["path extended"] = p

	// Structure: wrong child index on the path.
	p = cloneProof(base)
	p.Path[0].ChildIndex = (p.Path[0].ChildIndex + 1) % len(p.Path[0].Children)
	tampered["child index shifted"] = p

	for name, p := range tampered {
		if _, err := VerifyMembership(root, key, p); err == nil {
			t.Errorf("%s: tampered proof accepted", name)
		}
	}
	if len(tampered) < 25 {
		t.Fatalf("only %d tamper cases enumerated", len(tampered))
	}
}

// TestTamperVersionRoot checks that proofs generated under one committed
// version fail against a root committing to another version.
func TestTamperVersionRoot(t *testing.T) {
	idx, entries := buildSmall(t, 1, 3, 3, 4, 2)
	other, _ := buildSmall(t, 2, 3, 3, 4, 2)
	key := entries[0].Key
	if _, err := VerifyMembership(other.Root, key, idx.Prove(key)); err == nil {
		t.Error("proof of version 1 accepted under root of version 2")
	}
}

// TestTamperNonMembership ensures gap proofs cannot be bent to cover keys
// the committed ranges do not exclude.
func TestTamperNonMembership(t *testing.T) {
	idx, entries := buildSmall(t, 1, 4, 2, 8, 2)
	root := idx.Root
	gap := entries[1].Key + 1
	base := idx.Prove(gap)
	res, err := VerifyMembership(root, gap, base)
	if err != nil || res.Member {
		t.Fatalf("base gap proof invalid: %+v %v", res, err)
	}
	// Weaken the successor bound so it no longer excludes the gap key.
	p := cloneProof(base)
	lowered := false
	for lvl := range p.Path {
		for j := range p.Path[lvl].Children {
			if j > p.Path[lvl].ChildIndex && p.Path[lvl].Children[j].MinKey > gap {
				p.Path[lvl].Children[j].MinKey = gap
				lowered = true
			}
		}
	}
	if lowered {
		if _, err := VerifyMembership(root, gap, p); err == nil {
			t.Error("gap proof with weakened successor bound accepted")
		}
	}
	// A proof for a member key must not verify as non-membership.
	memberProof := idx.Prove(entries[2].Key)
	res, err = VerifyMembership(root, entries[2].Key, memberProof)
	if err != nil || !res.Member {
		t.Fatalf("member proof broken: %+v %v", res, err)
	}
}

// TestTamperRangeProofs checks that range certificates cannot omit blocks,
// reorder blocks, or drop the boundary proof.
func TestTamperRangeProofs(t *testing.T) {
	idx, entries := buildSmall(t, 1, 6, 2, 4, 2) // 12 entries
	root := idx.Root
	lo := entries[2].Key
	hi := entries[9].Key
	base := idx.ProveRange(lo, hi)
	if len(base.Items) < 3 {
		t.Fatalf("expected several blocks in range, got %d", len(base.Items))
	}
	if _, err := VerifyRange(root, lo, hi, base); err != nil {
		t.Fatalf("base range proof invalid: %v", err)
	}
	clone := func() *RangeProof {
		rp := &RangeProof{Version: base.Version}
		for i := range base.Items {
			rp.Items = append(rp.Items, *cloneProof(&base.Items[i]))
		}
		return rp
	}
	// Omission: drop a middle block.
	rp := clone()
	rp.Items = append(rp.Items[:1], rp.Items[2:]...)
	if _, err := VerifyRange(root, lo, hi, rp); err == nil {
		t.Error("range proof with omitted middle block accepted")
	}
	// Omission: drop the first block (extends before the certificate).
	rp = clone()
	rp.Items = rp.Items[1:]
	if _, err := VerifyRange(root, lo, hi, rp); err == nil {
		t.Error("range proof missing first block accepted")
	}
	// Omission: drop the last block (extends past the certificate).
	rp = clone()
	rp.Items = rp.Items[:len(rp.Items)-1]
	if _, err := VerifyRange(root, lo, hi, rp); err == nil {
		t.Error("range proof missing last block accepted")
	}
	// Reorder: swap two blocks.
	rp = clone()
	rp.Items[0], rp.Items[1] = rp.Items[1], rp.Items[0]
	if _, err := VerifyRange(root, lo, hi, rp); err == nil {
		t.Error("range proof with swapped blocks accepted")
	}
	// Empty-result certificate over a genuine inter-block gap
	// (block 0 ends at entries[1].Key, block 1 starts at entries[2].Key).
	gapLo := entries[1].Key + 1
	gapHi := entries[2].Key - 1
	empty := idx.ProveRange(gapLo, gapHi)
	if empty.Boundary == nil {
		t.Fatal("expected boundary proof for empty range")
	}
	if got, err := VerifyRange(root, gapLo, gapHi, empty); err != nil || got != nil {
		t.Fatalf("valid empty-range certificate rejected: got=%v err=%v", got, err)
	}
	stripped := &RangeProof{Version: empty.Version}
	if _, err := VerifyRange(root, gapLo, gapHi, stripped); err == nil {
		t.Error("empty range without boundary proof accepted")
	}
	// Boundary proof reused for a range it does not exclude.
	if _, err := VerifyRange(root, entries[1].Key, gapHi, empty); err == nil {
		t.Error("boundary proof accepted for a range it does not exclude")
	}
}
