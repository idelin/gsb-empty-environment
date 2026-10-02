package blockindex

import "testing"

// TestProofSizeAndAccessBounds checks, for growing index sizes, that point
// proofs stay within the announced byte bound and that verification touches
// only the announced number of blocks and child summaries.
func TestProofSizeAndAccessBounds(t *testing.T) {
	for _, numBlocks := range []int{1, 2, 7, 64, 500, 5000} {
		var entries []Entry
		for i := 0; i < numBlocks*5; i++ {
			entries = append(entries, Entry{Key: uint64(10 * i), Value: []byte{1, 2, 3}})
		}
		const fanout = 8
		idx, err := Build(1, entries, 5, fanout)
		if err != nil {
			t.Fatal(err)
		}
		bound := MaxProofBytes(idx.NumBlocks(), fanout, idx.MaxBlockBytes())
		accessBound := MaxAccessStats(idx.NumBlocks(), fanout)
		worstSize, worstChildren := 0, 0
		// Probe the first, middle and last blocks plus a gap key.
		probes := []uint64{entries[0].Key, entries[len(entries)/2].Key, entries[len(entries)-1].Key, entries[len(entries)-1].Key + 5}
		for _, key := range probes {
			p := idx.Prove(key)
			if _, err := VerifyMembership(idx.Root, key, p); err != nil {
				t.Fatalf("blocks=%d key=%d: %v", numBlocks, key, err)
			}
			st := AccessStatsOf(p)
			if st.ProofBytes > bound {
				t.Fatalf("blocks=%d: proof %d bytes exceeds bound %d", numBlocks, st.ProofBytes, bound)
			}
			if st.BlocksRead > accessBound.BlocksRead {
				t.Fatalf("blocks=%d: verifier read %d blocks, bound %d", numBlocks, st.BlocksRead, accessBound.BlocksRead)
			}
			if st.ChildInfosRead > accessBound.ChildInfosRead {
				t.Fatalf("blocks=%d: verifier read %d child summaries, bound %d", numBlocks, st.ChildInfosRead, accessBound.ChildInfosRead)
			}
			if st.ProofBytes > worstSize {
				worstSize = st.ProofBytes
			}
			if st.ChildInfosRead > worstChildren {
				worstChildren = st.ChildInfosRead
			}
		}
		t.Logf("blocks=%d height=%d proof<=%dB (worst %dB) children<=%d (worst %d) blocks-read=%d",
			numBlocks, Height(numBlocks, fanout), bound, worstSize, accessBound.ChildInfosRead, worstChildren, accessBound.BlocksRead)
	}
}

// TestRangeProofAccessScalesWithResult checks that a range certificate only
// carries the blocks intersecting the range, independent of index size.
func TestRangeProofAccessScalesWithResult(t *testing.T) {
	var entries []Entry
	for i := 0; i < 100000; i++ {
		entries = append(entries, Entry{Key: uint64(4 * i), Value: []byte{9}})
	}
	idx, err := Build(1, entries, 16, 8)
	if err != nil {
		t.Fatal(err)
	}
	// A narrow range deep inside a large index.
	lo := uint64(4*54321 - 1)
	hi := lo + 4*40
	rp := idx.ProveRange(lo, hi)
	got, err := VerifyRange(idx.Root, lo, hi, rp)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 40 {
		t.Fatalf("got %d entries, want 40", len(got))
	}
	// 40 entries at 16 per block touch at most 4 blocks; the index has 6250.
	if len(rp.Items) > 4 {
		t.Fatalf("range certificate carries %d blocks, want <= 4", len(rp.Items))
	}
	t.Logf("index blocks=%d range blocks accessed=%d proof=%dB", idx.NumBlocks(), len(rp.Items), rp.Size())
}

// TestHeightFormula pins the height function.
func TestHeightFormula(t *testing.T) {
	cases := []struct{ blocks, fanout, want int }{
		{0, 4, 0}, {1, 4, 1}, {4, 4, 1}, {5, 4, 2},
		{16, 4, 2}, {17, 4, 3}, {2, 2, 1}, {3, 2, 2}, {8, 2, 3}, {9, 2, 4},
	}
	for _, c := range cases {
		if got := Height(c.blocks, c.fanout); got != c.want {
			t.Errorf("Height(%d,%d)=%d, want %d", c.blocks, c.fanout, got, c.want)
		}
	}
}
