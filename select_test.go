package main

import (
	"bytes"
	"math/rand"
	"testing"
)

// referenceBruteForce independently enumerates all candidate subsets and
// scores them with the reference parser. It is the enumeration reference for
// selectExact.
func referenceBruteForce(samples [][]byte, cands []candidate, budget int) int {
	best := 0
	for _, s := range samples {
		best += refCost(s, nil)
	}
	n := len(cands)
	for mask := uint64(1); mask < uint64(1)<<uint(n); mask++ {
		var dict [][]byte
		for i := 0; i < n; i++ {
			if mask&(uint64(1)<<uint(i)) != 0 {
				dict = append(dict, cands[i].phrase)
			}
		}
		if dictSerializedLen(dict) > budget {
			continue
		}
		cost := dictSerializedLen(dict)
		for _, s := range samples {
			cost += refCost(s, dict)
		}
		if cost < best {
			best = cost
		}
	}
	return best
}

func TestSelectExactMatchesBruteForce(t *testing.T) {
	sampleSets := [][][]byte{
		{[]byte("abcabcabcabd"), []byte("abcabcxyz")},
		{[]byte("the cat sat"), []byte("the cat ran"), []byte("the dog sat")},
		{[]byte("aaaaaabaaaaaa"), []byte("baaaaaab")},
	}
	for _, samples := range sampleSets {
		sortSamplesCanonical(samples)
		cands, _ := generateCandidates(samples, 2, 6)
		if len(cands) > 18 {
			t.Fatalf("test set too large: %d candidates", len(cands))
		}
		sel, err := selectExact(samples, cands, 64, 20)
		if err != nil {
			t.Fatalf("selectExact: %v", err)
		}
		want := referenceBruteForce(samples, cands, 64)
		if sel.trainCost != want {
			t.Fatalf("selectExact cost=%d, brute force=%d (samples %q)", sel.trainCost, want, samples)
		}
	}
}

func TestSelectExactFindsObviousPhrase(t *testing.T) {
	samples := [][]byte{
		[]byte("abcabcabcabcabcabcabcabcabcabc"),
		[]byte("abcabcabcabcabc"),
	}
	sortSamplesCanonical(samples)
	cands, _ := generateCandidates(samples, 3, 6)
	sel, err := selectExact(samples, cands, 256, 20)
	if err != nil {
		t.Fatalf("selectExact: %v", err)
	}
	if len(sel.dict) == 0 {
		t.Fatalf("expected a non-empty dictionary for highly repetitive input")
	}
	for _, p := range sel.dict {
		if !bytes.HasPrefix(p, []byte("abc")) && !bytes.HasPrefix(p, []byte("bca")) && !bytes.HasPrefix(p, []byte("cab")) {
			t.Fatalf("unexpected dictionary entry %q", p)
		}
	}
	if realized := trainCostOf(samples, nil, true) - sel.trainCost; realized <= 0 {
		t.Fatalf("expected positive savings, got %d", realized)
	}
	// The joint cost must equal an actual re-encoding with the exact parser.
	di := buildDictIndex(sel.dict)
	actual := sel.dictBytes
	for _, s := range samples {
		actual += exactParseCost(s, di)
	}
	if actual != sel.trainCost {
		t.Fatalf("reported cost %d != actual %d", sel.trainCost, actual)
	}
}

func TestSelectionDeterministicUnderSampleOrder(t *testing.T) {
	base := [][]byte{
		[]byte("xyabxyabxyabxyabxyabxyab"),
		[]byte("abxyabxyabxyabxyabxy"),
		[]byte("yabxyabxyabxyabx"),
	}
	reference := trainForOrder(t, base)
	perm := rand.New(rand.NewSource(1)).Perm(len(base))
	shuffled := make([][]byte, len(base))
	for i, j := range perm {
		shuffled[i] = base[j]
	}
	got := trainForOrder(t, shuffled)
	if !bytes.Equal(reference, got) {
		t.Fatalf("dictionary depends on sample order:\n%q\nvs\n%q", reference, got)
	}
}

func trainForOrder(t *testing.T, samples [][]byte) []byte {
	t.Helper()
	cp := make([][]byte, len(samples))
	copy(cp, samples)
	sortSamplesCanonical(cp)
	cands, _ := generateCandidates(cp, 3, 4)
	sel, err := selectExact(cp, cands, 128, 20)
	if err != nil {
		t.Fatalf("selectExact: %v", err)
	}
	return marshalDict(sel.dict)
}

func TestHeuristicFeasibleWithUpperBound(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	// Build compressible training data from a small vocabulary.
	vocab := [][]byte{[]byte("alpha "), []byte("beta "), []byte("gamma delta "), []byte("epsilon zeta ")}
	var samples [][]byte
	for i := 0; i < 30; i++ {
		var buf bytes.Buffer
		for j := 0; j < 40; j++ {
			buf.Write(vocab[rng.Intn(len(vocab))])
		}
		samples = append(samples, buf.Bytes())
	}
	sortSamplesCanonical(samples)
	const budget = 200
	cands, _ := generateCandidates(samples, 3, 16)
	sel := selectHeuristic(samples, cands, budget)
	if sel.dictBytes > budget {
		t.Fatalf("dictionary %d bytes exceeds budget %d", sel.dictBytes, budget)
	}
	if len(sel.dict) == 0 {
		t.Fatalf("expected a non-empty dictionary for repetitive data")
	}
	// Feasible: every stream round-trips.
	di := buildDictIndex(sel.dict)
	crc := dictFileCRC(marshalDict(sel.dict))
	orig := 0
	for _, s := range samples {
		orig += len(s)
		stream := encodeStream(crc, s, fastParse(s, di))
		dec, err := decodeStream(stream, sel.dict, 1<<20)
		if err != nil || !bytes.Equal(dec, s) {
			t.Fatalf("roundtrip failed: %v", err)
		}
	}
	savings := orig - sel.trainCost
	ub := savingsUpperBound(samples, cands, budget)
	if savings > ub {
		t.Fatalf("heuristic savings %d exceed upper bound %d", savings, ub)
	}
	if savings <= 0 {
		t.Fatalf("expected positive savings on repetitive data, got %d", savings)
	}
	t.Logf("savings=%d upperBound=%d dict=%d entries", savings, ub, len(sel.dict))
}

func TestHeuristicReportsNoBenefit(t *testing.T) {
	// Incompressible deterministic data: no repeated substring can pay for
	// its dictionary entry, so the dictionary must come back empty.
	rng := rand.New(rand.NewSource(9))
	var samples [][]byte
	for i := 0; i < 8; i++ {
		b := make([]byte, 256)
		rng.Read(b)
		samples = append(samples, b)
	}
	sortSamplesCanonical(samples)
	cands, _ := generateCandidates(samples, 3, 16)
	sel := selectHeuristic(samples, cands, 512)
	if len(sel.dict) != 0 {
		t.Fatalf("expected empty dictionary for random data, got %d entries", len(sel.dict))
	}
	empty := trainCostOf(samples, nil, false)
	if sel.trainCost != empty {
		t.Fatalf("no-benefit case should fall back to empty-dictionary cost")
	}
}

func TestOverlappingCandidatesNotDoubleCounted(t *testing.T) {
	// "ab", "abab", "ababab" all overlap heavily; their standalone gains sum
	// to far more than any compatible parse can realize. The joint score must
	// match an actual parse, never the sum of standalone gains.
	samples := [][]byte{bytes.Repeat([]byte("ab"), 40)}
	sortSamplesCanonical(samples)
	cands, _ := generateCandidates(samples, 2, 8)
	sel, err := selectExact(samples, cands, 64, 20)
	if err != nil {
		t.Fatalf("selectExact: %v", err)
	}
	di := buildDictIndex(sel.dict)
	actual := sel.dictBytes + exactParseCost(samples[0], di)
	if actual != sel.trainCost {
		t.Fatalf("joint cost %d != actual parse %d", sel.trainCost, actual)
	}
	standaloneSum := 0
	for _, c := range cands {
		if c.gain > 0 {
			standaloneSum += c.gain
		}
	}
	realized := trainCostOf(samples, nil, true) - sel.trainCost
	if realized > standaloneSum {
		t.Fatalf("realized savings %d exceed sum of standalone gains %d", realized, standaloneSum)
	}
	t.Logf("realized=%d standaloneSum=%d dict=%q", realized, standaloneSum, sel.dict)
}

func TestHeuristicDeterministicUnderSampleOrder(t *testing.T) {
	base := [][]byte{
		[]byte("alpha beta gamma alpha beta gamma alpha"),
		[]byte("beta gamma delta beta gamma delta beta"),
		[]byte("alpha alpha alpha beta beta gamma"),
		[]byte("gamma gamma beta alpha delta alpha beta"),
		[]byte("delta delta gamma beta gamma alpha"),
	}
	train := func(samples [][]byte) []byte {
		cp := make([][]byte, len(samples))
		copy(cp, samples)
		sortSamplesCanonical(cp)
		cands, _ := generateCandidates(cp, 3, 12)
		return marshalDict(selectHeuristic(cp, cands, 128).dict)
	}
	reference := train(base)
	perm := rand.New(rand.NewSource(5)).Perm(len(base))
	shuffled := make([][]byte, len(base))
	for i, j := range perm {
		shuffled[i] = base[j]
	}
	if got := train(shuffled); !bytes.Equal(reference, got) {
		t.Fatalf("heuristic dictionary depends on sample order:\n%q\nvs\n%q", reference, got)
	}
}
