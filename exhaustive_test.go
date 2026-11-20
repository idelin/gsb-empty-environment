package bootpolicy

import (
	"math/rand"
	"testing"
)

func TestExhaustiveSmallConfigurations(t *testing.T) {
	signers := []string{"s1", "s2"}
	labels := []string{"ab", "abc", "x"}
	gens := []uint32{0, 5, 6, 20, maxUint32}
	locations := []string{"", "spi", "emmc"}
	digests := []Digest{digestOf(1), digestOf(2)}
	roots := []string{"r1", "r2"}
	times := []uint64{0, 50, 51, 200}

	original := &Policy{Entries: []Entry{
		{Kind: KindDeny, SignerID: "s1", LabelPrefix: "ab", GenLo: 5, GenHi: 20},
		{Kind: KindDeny, SignerID: "s1", LabelPrefix: "x", GenLo: 0, GenHi: maxUint32, Location: "spi"},
		{
			Kind: KindExempt, SignerID: "s1", LabelPrefix: "ab", GenLo: 5, GenHi: 20,
			RootID: "r1", Digest: digestOf(1), NotAfter: 50,
			Parent: &ParentScope{SignerID: "s1", Label: "abc"},
		},
	}}

	candidates := []Entry{
		{Kind: KindDeny, SignerID: "s1", LabelPrefix: "ab", GenLo: 5, GenHi: 20},
		{Kind: KindDeny, SignerID: "s1", LabelPrefix: "ab", GenLo: 5, GenHi: 10},
		{Kind: KindDeny, SignerID: "s1", LabelPrefix: "ab", GenLo: 11, GenHi: 20},
		{Kind: KindDeny, SignerID: "s1", LabelPrefix: "x", GenLo: 0, GenHi: maxUint32, Location: "spi"},
		original.Entries[2],
	}
	res, err := Compress(original, candidates)
	if err != nil {
		t.Fatal(err)
	}
	compressed := &Policy{Entries: res.Selected}
	if err := VerifyCertificate(original, compressed, res.Certificate); err != nil {
		t.Fatal(err)
	}

	domain := componentDomain(signers, labels, gens, locations, roots, digests)
	events := enumerateEvents(domain, times)
	for _, ev := range events {
		if original.EventAdmit(ev) != compressed.EventAdmit(ev) {
			t.Fatalf("event mismatch: %+v", ev)
		}
	}

	smallDomain := domain[:minInt(12, len(domain))]
	boots := enumerateBootInputs(smallDomain, times, 3)
	for _, in := range boots {
		gotA, errA := original.Admit(in)
		gotB, errB := compressed.Admit(in)
		if errA != nil || errB != nil {
			t.Fatalf("unexpected error: %v %v", errA, errB)
		}
		if gotA != gotB {
			t.Fatalf("boot mismatch: %+v", in)
		}
	}

	// Explicit scenario: the same component loaded by two different parents.
	child := component("s1", "ab", 7, "", "r1", digestOf(1))
	namedParent := Component{SignerID: "s1", Label: "abc", Digest: digestOf(3)}
	otherParent := Component{SignerID: "s2", Label: "abc", Digest: digestOf(4)}
	rescued := original.EventAdmit(LoadEvent{BootTime: 50, Child: child, Parent: &namedParent})
	rejected := original.EventAdmit(LoadEvent{BootTime: 50, Child: child, Parent: &otherParent})
	if !rescued || rejected {
		t.Fatalf("parent isolation broken: rescued=%v rejected=%v", rescued, rejected)
	}
	expired := original.EventAdmit(LoadEvent{BootTime: 51, Child: child, Parent: &namedParent})
	if expired {
		t.Fatal("expired recovery image must be rejected")
	}
}

func TestRandomizedCompressionEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 25; iter++ {
		original, candidates := randomPolicyAndCandidates(rng)
		res, err := Compress(original, candidates)
		if err == ErrNoEquivalentSet || err == ErrPartitionTooLarge {
			continue
		}
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		compressed := &Policy{Entries: res.Selected}
		if err := VerifyCertificate(original, compressed, res.Certificate); err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		union := append(append([]Entry(nil), original.Entries...), res.Selected...)
		part, err := BuildPartition(union, len(original.Entries), 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range part.groups {
			if original.EventAdmit(g.event) != compressed.EventAdmit(g.event) {
				t.Fatalf("iter %d group mismatch", iter)
			}
		}
	}
}

func componentDomain(signers, labels []string, gens []uint32, locations, roots []string, digests []Digest) []Component {
	out := make([]Component, 0)
	for _, s := range signers {
		for _, l := range labels {
			for _, g := range gens {
				for _, loc := range locations {
					for _, r := range roots {
						for _, d := range digests {
							out = append(out, Component{
								SignerID: s, Label: l, Generation: g,
								Location: loc, RootID: r, Digest: d,
							})
						}
					}
				}
			}
		}
	}
	return out
}

func enumerateEvents(domain []Component, times []uint64) []LoadEvent {
	out := make([]LoadEvent, 0)
	for _, c := range domain {
		for _, tm := range times {
			out = append(out, LoadEvent{BootTime: tm, Child: c})
			for _, parent := range domain {
				p := parent
				out = append(out, LoadEvent{BootTime: tm, Child: c, Parent: &p})
			}
		}
	}
	return out
}

// enumerateBootInputs enumerates every component tuple up to maxComp and every
// DAG edge subset (parent index strictly below child index, so cycles cannot
// form), crossing every boot time in times.
func enumerateBootInputs(domain []Component, times []uint64, maxComp int) []BootInput {
	out := make([]BootInput, 0)
	for n := 1; n <= maxComp && n <= len(domain); n++ {
		idx := make([]int, n)
		for i := range idx {
			idx[i] = i
		}
		for {
			edgeCount := n * (n - 1) / 2
			for em := 0; em < 1<<edgeCount; em++ {
				edges := make([]LoadEdge, 0)
				bit := 0
				for parent := 0; parent < n; parent++ {
					for child := parent + 1; child < n; child++ {
						if em&(1<<bit) != 0 {
							edges = append(edges, LoadEdge{ParentIndex: parent, ChildIndex: child})
						}
						bit++
					}
				}
				comps := make([]Component, n)
				for i, j := range idx {
					comps[i] = domain[j]
				}
				for _, tm := range times {
					out = append(out, BootInput{BootTime: tm, Components: comps, Edges: edges})
				}
			}
			if !nextCombination(idx, len(domain)) {
				break
			}
		}
	}
	return out
}

func nextCombination(idx []int, n int) bool {
	i := len(idx) - 1
	for i >= 0 && idx[i] == n-len(idx)+i {
		i--
	}
	if i < 0 {
		return false
	}
	idx[i]++
	for j := i + 1; j < len(idx); j++ {
		idx[j] = idx[j-1] + 1
	}
	return true
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func randomPolicyAndCandidates(rng *rand.Rand) (*Policy, []Entry) {
	signers := []string{"s1", "s2", "s3"}
	prefixes := []string{"", "a", "ab", "b"}
	locs := []string{"", "spi", "emmc"}
	scopes := []*ParentScope{
		nil,
		{SignerID: "s1", Label: "ab"},
		{SignerID: "s2", Label: "a"},
	}

	randomEntry := func() Entry {
		lo := uint32(rng.Intn(8))
		hi := lo + uint32(rng.Intn(8))
		e := Entry{
			Kind:        KindDeny,
			SignerID:    signers[rng.Intn(len(signers))],
			LabelPrefix: prefixes[rng.Intn(len(prefixes))],
			GenLo:       lo,
			GenHi:       hi,
			Location:    locs[rng.Intn(len(locs))],
			Parent:      scopes[rng.Intn(len(scopes))],
		}
		if rng.Intn(4) == 0 {
			e.Kind = KindExempt
			e.Digest = digestOf(byte(rng.Intn(3) + 1))
			if rng.Intn(2) == 0 {
				e.RootID = "r1"
			}
			if rng.Intn(2) == 0 {
				e.NotAfter = uint64(rng.Intn(20))
			}
		}
		return e
	}

	nOrig := 2 + rng.Intn(3)
	entries := make([]Entry, 0, nOrig)
	for i := 0; i < nOrig; i++ {
		entries = append(entries, randomEntry())
	}
	nCand := 3 + rng.Intn(6)
	candidates := make([]Entry, 0, nCand)
	// Ensure feasibility is possible: include every original deny as a
	// candidate, and original exemptions too.
	for _, e := range entries {
		candidates = append(candidates, e)
	}
	for len(candidates) < nCand {
		e := randomEntry()
		if e.Kind == KindExempt {
			// Only original exemptions may appear as candidates.
			continue
		}
		candidates = append(candidates, e)
	}
	return &Policy{Entries: entries}, candidates
}
