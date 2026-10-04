package blkidx

import (
	"bytes"
	"fmt"
	"sort"
	"testing"
)

func makeEntries(n int) []Entry {
	entries := make([]Entry, n)
	for i := 0; i < n; i++ {
		entries[i] = Entry{
			Key:   fmt.Sprintf("user:%08d", i*7+13),
			Value: []byte(fmt.Sprintf("payload-%08d-%s", i, "abcdefgh"[:i%8+1])),
		}
	}
	return entries
}

func naiveRank(entries []Entry, key string) uint64 {
	var n uint64
	for _, e := range entries {
		if e.Key < key {
			n++
		}
	}
	return n
}

func naiveRange(entries []Entry, lo, hi string) []Entry {
	var out []Entry
	for _, e := range entries {
		if e.Key >= lo && e.Key <= hi {
			out = append(out, e)
		}
	}
	return out
}

func TestLeafEncodingLayoutExact(t *testing.T) {
	entries := []Entry{{Key: "aa", Value: []byte("x")}, {Key: "ab", Value: []byte("y")}}
	block := encodeLeaf(7, 3, 41, entries)

	// header 40 + entries region 11 + restart array 4 = 55 bytes.
	// entry 0: shared(1) unshared(1) "aa"(2) vlen(1) "x"(1) = 6
	// entry 1: shared(1) unshared(1) "b"(1)  vlen(1) "y"(1) = 5
	if len(block) != 55 {
		t.Fatalf("encoded block = %d bytes, want 55", len(block))
	}
	hdr, got, err := decodeLeaf(block)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if hdr.Version != 7 || hdr.BlockID != 3 || hdr.Count != 2 || hdr.BaseOff != 41 {
		t.Fatalf("header mismatch: %+v", hdr)
	}
	if got[0].Key != "aa" || got[1].Key != "ab" || !bytes.Equal(got[1].Value, []byte("y")) {
		t.Fatalf("entries mismatch: %+v", got)
	}

	// Corruptions must be rejected at decode time.
	bad := append([]byte(nil), block...)
	bad[0] ^= 0xFF // magic
	if _, _, err := decodeLeaf(bad); err == nil {
		t.Fatal("bad magic accepted")
	}
	bad = append([]byte(nil), block...)
	bad[10] ^= 0x01 // count field, crc now stale
	if _, _, err := decodeLeaf(bad); err == nil {
		t.Fatal("stale crc accepted")
	}
	bad = append([]byte(nil), block...)
	bad = append(bad, 0) // trailing byte
	if _, _, err := decodeLeaf(bad); err == nil {
		t.Fatal("trailing byte accepted")
	}
}

func TestInternalSummaryBindsOrderRangeCount(t *testing.T) {
	mk := func(key string, b byte) ChildDesc {
		var h [32]byte
		h[0] = b
		return ChildDesc{Hash: h, BaseOff: 0, Count: 10, MaxKey: key}
	}
	c0 := mk("k1", 1)
	c1 := mk("k2", 2)
	c1.BaseOff = 10

	base := internalHash(1, 100, []ChildDesc{c0, c1})

	if h := internalHash(1, 100, []ChildDesc{c1, c0}); h == base {
		t.Fatal("child order not bound")
	}
	c1c := c1
	c1c.Count++
	if h := internalHash(1, 100, []ChildDesc{c0, c1c}); h == base {
		t.Fatal("child count not bound")
	}
	c1m := c1
	c1m.MaxKey = "k3"
	if h := internalHash(1, 100, []ChildDesc{c0, c1m}); h == base {
		t.Fatal("child max key not bound")
	}
	c1b := c1
	c1b.BaseOff++
	if h := internalHash(1, 100, []ChildDesc{c0, c1b}); h == base {
		t.Fatal("child base offset not bound")
	}
	if h := internalHash(2, 100, []ChildDesc{c0, c1}); h == base {
		t.Fatal("version not bound")
	}
	if h := internalHash(1, 101, []ChildDesc{c0, c1}); h == base {
		t.Fatal("node id not bound")
	}
}

func TestIndexLayoutAndCompression(t *testing.T) {
	entries := makeEntries(5000)
	ix, err := Build(entries, Config{Version: 1, LeafCap: 32, Fanout: 8})
	if err != nil {
		t.Fatal(err)
	}
	st := ix.Layout()
	if st.TotalBytes != st.HeaderBytes+st.EntriesBytes+st.RestartBytes+st.InternalBytes {
		t.Fatalf("layout does not add up: %+v", st)
	}
	raw := st.RawKeyBytes + st.RawValueBytes
	if st.EntriesBytes >= raw {
		t.Fatalf("prefix compression ineffective: entries region %d >= raw %d", st.EntriesBytes, raw)
	}
	t.Logf("blocks=%d internalNodes=%d depth=%d", st.Blocks, st.InternalNodes, ix.Depth())
	t.Logf("header=%dB entries=%dB restarts=%dB internal=%dB total=%dB (raw keys+values=%dB)",
		st.HeaderBytes, st.EntriesBytes, st.RestartBytes, st.InternalBytes, st.TotalBytes, raw)
}

func TestMembershipAndAbsenceProofs(t *testing.T) {
	entries := makeEntries(2000)
	ix, err := Build(entries, Config{Version: 3, LeafCap: 16, Fanout: 8})
	if err != nil {
		t.Fatal(err)
	}
	root := ix.RootHash()

	check := func(key string, wantFound bool) {
		p := ix.ProveMembership(key)
		var stats VerifyStats
		res, verr := VerifyMembership(root, p, &stats)
		if verr != nil {
			t.Fatalf("key %q: %v", key, verr)
		}
		if res.Found != wantFound {
			t.Fatalf("key %q: found=%v want %v", key, res.Found, wantFound)
		}
		if res.Rank != naiveRank(entries, key) {
			t.Fatalf("key %q: rank %d, want %d", key, res.Rank, naiveRank(entries, key))
		}
		if wantFound {
			idx := sort.Search(len(entries), func(i int) bool { return entries[i].Key >= key })
			if !bytes.Equal(res.Value, entries[idx].Value) {
				t.Fatalf("key %q: wrong value", key)
			}
		}
		if len(stats.LeavesRead) != 1 {
			t.Fatalf("key %q: verifier read %d leaves, want 1", key, len(stats.LeavesRead))
		}
		if len(stats.NodesRead) != ix.Depth() {
			t.Fatalf("key %q: verifier read %d nodes, want depth %d", key, len(stats.NodesRead), ix.Depth())
		}
	}

	for _, i := range []int{0, 1, 15, 16, 17, 999, 1000, 1998, 1999} {
		check(entries[i].Key, true)
	}
	for _, key := range []string{"user:00000012", "user:00000014", "aaa", "user:00014008", "zzz"} {
		check(key, false)
	}
}

func TestSingleLeafIndex(t *testing.T) {
	entries := makeEntries(5)
	ix, err := Build(entries, Config{Version: 1, LeafCap: 32, Fanout: 8})
	if err != nil {
		t.Fatal(err)
	}
	if ix.Depth() != 0 {
		t.Fatalf("depth = %d, want 0", ix.Depth())
	}
	p := ix.ProveMembership(entries[2].Key)
	res, verr := VerifyMembership(ix.RootHash(), p, nil)
	if verr != nil || !res.Found || res.Rank != 2 {
		t.Fatalf("res=%+v verr=%v", res, verr)
	}
	p2 := ix.ProveMembership("user:00000012")
	res2, verr2 := VerifyMembership(ix.RootHash(), p2, nil)
	if verr2 != nil || res2.Found {
		t.Fatalf("absence: res=%+v verr=%v", res2, verr2)
	}
}

func TestRankAndSelect(t *testing.T) {
	entries := makeEntries(3000)
	ix, err := Build(entries, Config{Version: 1, LeafCap: 16, Fanout: 8})
	if err != nil {
		t.Fatal(err)
	}
	root := ix.RootHash()

	for _, j := range []uint64{0, 1, 15, 16, 100, 1024, 2999} {
		p := ix.ProveSelect(j)
		if p == nil {
			t.Fatalf("select(%d): no proof", j)
		}
		res, verr := VerifySelect(root, j, p, nil)
		if verr != nil {
			t.Fatalf("select(%d): %v", j, verr)
		}
		if res.Value == nil || res.Rank != j || res.Value[0] != entries[j].Value[0] {
			t.Fatalf("select(%d): wrong result", j)
		}
		// The same proof must not verify for a different position.
		if _, verr := VerifySelect(root, j+1, p, nil); verr == nil {
			t.Fatalf("select proof for %d accepted at position %d", j, j+1)
		}
	}
	if p := ix.ProveSelect(3000); p != nil {
		t.Fatal("select out of range returned a proof")
	}
}

func TestRangeCertificates(t *testing.T) {
	entries := makeEntries(2000)
	ix, err := Build(entries, Config{Version: 1, LeafCap: 16, Fanout: 8})
	if err != nil {
		t.Fatal(err)
	}
	root := ix.RootHash()

	cases := [][2]string{
		{entries[0].Key, entries[1999].Key},        // everything
		{entries[100].Key, entries[299].Key},       // aligned subrange
		{entries[100].Key + "x", entries[301].Key}, // open left edge
		{"a", entries[0].Key},                      // from before the first key
		{entries[1999].Key, "z"},                   // past the last key
		{"user:00000713A", "user:00000713B"},       // empty: between two keys
		{"a", "b"},                                 // empty: before everything
		{"y", "z"},                                 // empty: after everything
	}
	for _, c := range cases {
		p := ix.ProveRange(c[0], c[1])
		var stats VerifyStats
		answer, verr := VerifyRange(root, p, &stats)
		if verr != nil {
			t.Fatalf("range [%q,%q]: %v", c[0], c[1], verr)
		}
		want := naiveRange(entries, c[0], c[1])
		if len(answer) != len(want) {
			t.Fatalf("range [%q,%q]: %d answers, want %d", c[0], c[1], len(answer), len(want))
		}
		for i := range want {
			if answer[i].Key != want[i].Key || !bytes.Equal(answer[i].Value, want[i].Value) {
				t.Fatalf("range [%q,%q]: answer %d = %q, want %q", c[0], c[1], i, answer[i].Key, want[i].Key)
			}
		}
		if len(stats.LeavesRead) != len(p.Blocks) {
			t.Fatalf("range [%q,%q]: verifier read %d leaves, proof carries %d",
				c[0], c[1], len(stats.LeavesRead), len(p.Blocks))
		}
	}
}

func TestProofSizeAndAccessBounds(t *testing.T) {
	entries := makeEntries(20000)
	ix, err := Build(entries, Config{Version: 1, LeafCap: 16, Fanout: 8})
	if err != nil {
		t.Fatal(err)
	}
	root := ix.RootHash()

	maxBlock, maxKey := 0, 0
	for _, b := range ix.leaves {
		if len(b) > maxBlock {
			maxBlock = len(b)
		}
	}
	for _, e := range entries {
		if len(e.Key) > maxKey {
			maxKey = len(e.Key)
		}
	}
	bound := ix.MembershipProofBound(maxBlock, maxKey)

	worst := 0
	for _, i := range []int{0, 5000, 10000, 19999} {
		p := ix.ProveMembership(entries[i].Key)
		if p.Size() > bound {
			t.Fatalf("proof size %d exceeds bound %d", p.Size(), bound)
		}
		if p.Size() > worst {
			worst = p.Size()
		}
		var stats VerifyStats
		if _, verr := VerifyMembership(root, p, &stats); verr != nil {
			t.Fatal(verr)
		}
		leaves, nodes := ix.AccessBound()
		if len(stats.LeavesRead) > leaves || len(stats.NodesRead) > nodes {
			t.Fatalf("access %d leaves/%d nodes exceeds bound %d/%d",
				len(stats.LeavesRead), len(stats.NodesRead), leaves, nodes)
		}
	}
	t.Logf("entries=%d depth=%d membership proof <= %d bytes (worst observed %d), verifier touches 1 leaf + %d nodes",
		len(entries), ix.Depth(), bound, worst, ix.Depth())

	// A range certificate spanning several blocks.
	p := ix.ProveRange(entries[4000].Key, entries[4300].Key)
	var stats VerifyStats
	answer, verr := VerifyRange(root, p, &stats)
	if verr != nil {
		t.Fatal(verr)
	}
	t.Logf("range certificate: %d answers, %d blocks, %d bytes, verifier read %d leaves + %d nodes",
		len(answer), len(p.Blocks), p.Size(), len(stats.LeavesRead), len(stats.NodesRead))
}
