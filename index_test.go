package blockindex

import (
	"fmt"
	"testing"
)

// buildSmall constructs an index with numBlocks*perBlock entries; keys are
// spaced by keyStep so every key+delta (0<delta<keyStep) is a gap.
func buildSmall(t *testing.T, version uint64, numBlocks, perBlock, keyStep, fanout int) (*Index, []Entry) {
	t.Helper()
	var entries []Entry
	for i := 0; i < numBlocks*perBlock; i++ {
		entries = append(entries, Entry{
			Key:   uint64(100 + keyStep*i),
			Value: []byte(fmt.Sprintf("val-%d", i)),
		})
	}
	idx, err := Build(version, entries, perBlock, fanout)
	if err != nil {
		t.Fatal(err)
	}
	return idx, entries
}

// TestEnumerateSmallStructures enumerates many small index shapes and, for
// each, verifies membership of every key, non-membership of every gap key,
// and select for every rank.
func TestEnumerateSmallStructures(t *testing.T) {
	const keyStep = 4
	for numBlocks := 1; numBlocks <= 6; numBlocks++ {
		for perBlock := 1; perBlock <= 3; perBlock++ {
			for fanout := 2; fanout <= 3; fanout++ {
				idx, entries := buildSmall(t, 3, numBlocks, perBlock, keyStep, fanout)
				root := idx.Root
				if root.TotalEntries != uint64(len(entries)) || root.TotalBlocks != uint64(numBlocks) {
					t.Fatalf("shape %d/%d/%d: root totals wrong", numBlocks, perBlock, fanout)
				}
				if root.Height != Height(numBlocks, fanout) {
					t.Fatalf("shape %d/%d/%d: height %d != %d", numBlocks, perBlock, fanout, root.Height, Height(numBlocks, fanout))
				}
				// Every member key, with value and rank binding.
				for rank, e := range entries {
					res, err := VerifyMembership(root, e.Key, idx.Prove(e.Key))
					if err != nil {
						t.Fatalf("shape %d/%d/%d key %d: %v", numBlocks, perBlock, fanout, e.Key, err)
					}
					if !res.Member || string(res.Value) != string(e.Value) || res.Rank != uint64(rank) {
						t.Fatalf("shape %d/%d/%d key %d: got member=%v rank=%d value=%q",
							numBlocks, perBlock, fanout, e.Key, res.Member, res.Rank, res.Value)
					}
					// Select by rank must return the same entry.
					se, err := VerifySelect(root, uint64(rank), idx.ProveSelect(uint64(rank)))
					if err != nil {
						t.Fatalf("shape %d/%d/%d select %d: %v", numBlocks, perBlock, fanout, rank, err)
					}
					if se.Key != e.Key || string(se.Value) != string(e.Value) {
						t.Fatalf("shape %d/%d/%d select %d: got key %d", numBlocks, perBlock, fanout, rank, se.Key)
					}
				}
				// Every gap key in [first-1, last+1] must be provably absent.
				first := entries[0].Key
				last := entries[len(entries)-1].Key
				for k := first - 1; k <= last+1; k++ {
					isMember := (k-100)%keyStep == 0 && k >= 100 && k <= last
					if isMember {
						continue
					}
					res, err := VerifyMembership(root, k, idx.Prove(k))
					if err != nil {
						t.Fatalf("shape %d/%d/%d gap %d: %v", numBlocks, perBlock, fanout, k, err)
					}
					if res.Member {
						t.Fatalf("shape %d/%d/%d gap %d: reported member", numBlocks, perBlock, fanout, k)
					}
				}
			}
		}
	}
}

// TestRangeExhaustive enumerates every query range over a small key domain
// and checks the certificate result against a naive scan.
func TestRangeExhaustive(t *testing.T) {
	idx, entries := buildSmall(t, 1, 4, 3, 5, 2) // 12 entries, keys 100,105,...,155
	root := idx.Root
	naive := func(lo, hi uint64) []Entry {
		var out []Entry
		for _, e := range entries {
			if e.Key >= lo && e.Key <= hi {
				out = append(out, e)
			}
		}
		return out
	}
	for lo := uint64(95); lo <= 160; lo++ {
		for hi := lo; hi <= 160; hi++ {
			got, err := VerifyRange(root, lo, hi, idx.ProveRange(lo, hi))
			if err != nil {
				t.Fatalf("range [%d,%d]: %v", lo, hi, err)
			}
			want := naive(lo, hi)
			if len(got) != len(want) {
				t.Fatalf("range [%d,%d]: got %d entries, want %d", lo, hi, len(got), len(want))
			}
			for i := range want {
				if got[i].Key != want[i].Key || string(got[i].Value) != string(want[i].Value) {
					t.Fatalf("range [%d,%d] entry %d: got %v want %v", lo, hi, i, got[i], want[i])
				}
			}
		}
	}
}

func TestEmptyIndex(t *testing.T) {
	idx, err := Build(9, nil, 8, 4)
	if err != nil {
		t.Fatal(err)
	}
	root := idx.Root
	if root.TotalBlocks != 0 || root.TotalEntries != 0 || root.Height != 0 {
		t.Fatalf("empty root wrong: %+v", root)
	}
	res, err := VerifyMembership(root, 42, idx.Prove(42))
	if err != nil || res.Member {
		t.Fatalf("empty membership: res=%+v err=%v", res, err)
	}
	got, err := VerifyRange(root, 0, 1000, idx.ProveRange(0, 1000))
	if err != nil || got != nil {
		t.Fatalf("empty range: got=%v err=%v", got, err)
	}
	if idx.ProveSelect(0) != nil {
		t.Fatal("empty select should be nil")
	}
}

// TestLargerIndexSpotChecks builds a bigger index and checks proofs at many
// positions, including block and node boundaries.
func TestLargerIndexSpotChecks(t *testing.T) {
	var entries []Entry
	for i := 0; i < 5000; i++ {
		entries = append(entries, Entry{Key: uint64(i * 3), Value: []byte{byte(i), byte(i >> 8)}})
	}
	idx, err := Build(1, entries, 7, 3)
	if err != nil {
		t.Fatal(err)
	}
	root := idx.Root
	for rank := 0; rank < len(entries); rank += 13 {
		e := entries[rank]
		res, err := VerifyMembership(root, e.Key, idx.Prove(e.Key))
		if err != nil || !res.Member || res.Rank != uint64(rank) {
			t.Fatalf("rank %d: res=%+v err=%v", rank, res, err)
		}
		se, err := VerifySelect(root, uint64(rank), idx.ProveSelect(uint64(rank)))
		if err != nil || se.Key != e.Key {
			t.Fatalf("select %d: got %v err=%v", rank, se, err)
		}
		if k := e.Key + 1; k < entries[len(entries)-1].Key {
			res, err := VerifyMembership(root, k, idx.Prove(k))
			if err != nil || res.Member {
				t.Fatalf("gap after rank %d: res=%+v err=%v", rank, res, err)
			}
		}
	}
}
