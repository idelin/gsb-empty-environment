package comp

import (
	"bytes"
	"math/rand"
	"testing"

	"dictcomp/internal/wire"
)

func randSample(r *rand.Rand, n int, alphabet string) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.Intn(len(alphabet))]
	}
	return b
}

// repetitiveCorpus builds log-like samples with plenty of shared fragments.
func repetitiveCorpus(r *rand.Rand, nSamples, sampleLen int) [][]byte {
	words := []string{"GET", "POST", "/api/users", "/api/orders", "200", "404", "ms", "latency", "request id", "cache hit", "cache miss"}
	var out [][]byte
	for i := 0; i < nSamples; i++ {
		var buf bytes.Buffer
		for buf.Len() < sampleLen {
			buf.WriteString(words[r.Intn(len(words))])
			buf.WriteByte(' ')
			buf.WriteString(string(rune('0' + r.Intn(10))))
			buf.WriteByte(' ')
		}
		out = append(out, buf.Bytes())
	}
	return out
}

func TestParseCostMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for iter := 0; iter < 300; iter++ {
		s := randSample(r, 1+r.Intn(60), "ab")
		var dict [][]byte
		for j := 0; j < r.Intn(5) && len(s) >= 3; j++ {
			a := r.Intn(len(s) - 2)
			maxL := len(s) - a
			if maxL > 8 {
				maxL = 8
			}
			l := 2 + r.Intn(maxL-1)
			dict = append(dict, s[a:a+l])
		}
		toks, cost := OptimalParse(s, dict)
		if got := wire.TokensSize(toks); got != cost {
			t.Fatalf("iter %d: reported cost %d != token stream size %d", iter, cost, got)
		}
		if b := BruteParseCost(s, dict); b != cost {
			t.Fatalf("iter %d: OptimalParse cost %d != brute force %d (s=%q dict=%q)", iter, cost, b, s, dict)
		}
	}
}

func TestParseHandlesLongLiteralRuns(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	s := randSample(r, 1000, "abcdefghijklmnopqrstuvwxyz")
	toks, cost := OptimalParse(s, nil)
	if got := wire.TokensSize(toks); got != cost {
		t.Fatalf("cost %d != stream size %d", cost, got)
	}
	// 1000 literals need ceil(1000/254) control bytes.
	want := int64(1000 + 4)
	if cost != want {
		t.Fatalf("literal-only cost = %d, want %d", cost, want)
	}
	enc, err := wire.Encode(nil, toks, s)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	_, plain, err := wire.Decode(enc, 1<<20)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(plain, s) {
		t.Fatal("round trip mismatch")
	}
}

func TestExactSelectionMatchesBruteReference(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	tested := 0
	for iter := 0; iter < 4000 && tested < 60; iter++ {
		ns := 1 + r.Intn(3)
		var samples [][]byte
		for i := 0; i < ns; i++ {
			samples = append(samples, randSample(r, 4+r.Intn(10), "ab"))
		}
		cands := GenerateCandidates(samples, 3, 6)
		if len(cands) == 0 || len(cands) > maxExactCandidates {
			continue
		}
		_, total := exactSelect(samples, cands)
		_, bruteTotal := BruteBest(samples, 3, 6)
		if total != bruteTotal {
			t.Fatalf("exact total %d != brute reference %d (samples=%q)", total, bruteTotal, samples)
		}
		tested++
	}
	if tested == 0 {
		t.Fatal("no feasible exact cases generated")
	}
	t.Logf("compared %d exact cases against the enumeration reference", tested)
}

func TestSelectDictionaryExactModeEndToEnd(t *testing.T) {
	samples := [][]byte{
		[]byte("abcabcabc"),
		[]byte("abcabc"),
	}
	sel, err := SelectDictionary(samples, 3, 6, ModeExact)
	if err != nil {
		t.Fatalf("SelectDictionary: %v", err)
	}
	if !sel.Exact {
		t.Fatal("expected exact mode")
	}
	if sel.TrainTotal > sel.EmptyTotal {
		t.Fatalf("train total %d worse than no dictionary %d", sel.TrainTotal, sel.EmptyTotal)
	}
	_, bruteTotal := BruteBest(samples, 3, 6)
	if len(sel.Dict) == 0 {
		t.Fatal("expected a non-empty optimal dictionary on repetitive samples")
	}
	if sel.TrainTotal != bruteTotal {
		t.Fatalf("exact total %d != brute reference %d", sel.TrainTotal, bruteTotal)
	}
}

func TestExactModeRefusesLargeInput(t *testing.T) {
	samples := repetitiveCorpus(rand.New(rand.NewSource(3)), 4, 2000)
	if _, err := SelectDictionary(samples, 4, 16, ModeExact); err == nil {
		t.Fatal("expected exact mode to refuse a large input")
	}
}

func TestDeterministicAcrossSampleOrder(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	samples := repetitiveCorpus(r, 8, 400)
	sel1, err := SelectDictionary(samples, 4, 24, ModeGreedy)
	if err != nil {
		t.Fatalf("SelectDictionary: %v", err)
	}
	// Deterministic permutation (reversal + rotation), not input order.
	shuffled := make([][]byte, len(samples))
	for i := range samples {
		shuffled[i] = samples[(len(samples)-1-i+3)%len(samples)]
	}
	sel2, err := SelectDictionary(shuffled, 4, 24, ModeGreedy)
	if err != nil {
		t.Fatalf("SelectDictionary: %v", err)
	}
	if len(sel1.Dict) != len(sel2.Dict) {
		t.Fatalf("dictionary size differs across sample order: %d vs %d", len(sel1.Dict), len(sel2.Dict))
	}
	for i := range sel1.Dict {
		if !bytes.Equal(sel1.Dict[i], sel2.Dict[i]) {
			t.Fatalf("entry %d differs across sample order: %q vs %q", i, sel1.Dict[i], sel2.Dict[i])
		}
	}
	if sel1.TrainTotal != sel2.TrainTotal {
		t.Fatalf("train total differs across sample order: %d vs %d", sel1.TrainTotal, sel2.TrainTotal)
	}
}

func TestGreedyFeasibleAndWithinBound(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	samples := repetitiveCorpus(r, 12, 3000)
	sel, err := SelectDictionary(samples, 4, 32, ModeGreedy)
	if err != nil {
		t.Fatalf("SelectDictionary: %v", err)
	}
	if sel.Exact {
		t.Fatal("greedy mode must not claim optimality")
	}
	if len(sel.Dict) == 0 {
		t.Fatal("expected a non-empty dictionary on repetitive data")
	}
	if sel.TrainTotal >= sel.EmptyTotal {
		t.Fatalf("dictionary did not help: %d >= %d", sel.TrainTotal, sel.EmptyTotal)
	}
	if sel.TrainTotal < sel.OptimalFloor() {
		t.Fatalf("train total %d below the certified floor %d", sel.TrainTotal, sel.OptimalFloor())
	}
	// Every sample must round-trip through the real format.
	for i, s := range samples {
		toks, _ := OptimalParse(s, sel.Dict)
		enc, err := wire.Encode(sel.Dict, toks, s)
		if err != nil {
			t.Fatalf("sample %d: Encode: %v", i, err)
		}
		_, plain, err := wire.Decode(enc, 1<<30)
		if err != nil {
			t.Fatalf("sample %d: Decode: %v", i, err)
		}
		if !bytes.Equal(plain, s) {
			t.Fatalf("sample %d: round trip mismatch", i)
		}
	}
}

func TestNoGainReportedHonestly(t *testing.T) {
	r := rand.New(rand.NewSource(6))
	var samples [][]byte
	for i := 0; i < 4; i++ {
		samples = append(samples, randSample(r, 2000, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"))
	}
	sel, err := SelectDictionary(samples, 4, 32, ModeGreedy)
	if err != nil {
		t.Fatalf("SelectDictionary: %v", err)
	}
	if len(sel.Dict) != 0 {
		t.Fatalf("expected empty dictionary on incompressible data, got %d entries", len(sel.Dict))
	}
	if sel.TrainTotal != sel.EmptyTotal {
		t.Fatalf("train total %d should equal no-dictionary total %d", sel.TrainTotal, sel.EmptyTotal)
	}
}

func TestEmptyAndTinySamples(t *testing.T) {
	samples := [][]byte{{}, []byte("a"), []byte("ab")}
	sel, err := SelectDictionary(samples, 3, 8, ModeAuto)
	if err != nil {
		t.Fatalf("SelectDictionary: %v", err)
	}
	for _, s := range samples {
		toks, _ := OptimalParse(s, sel.Dict)
		enc, err := wire.Encode(sel.Dict, toks, s)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		_, plain, err := wire.Decode(enc, 1<<20)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if !bytes.Equal(plain, s) {
			t.Fatalf("round trip mismatch for %q", s)
		}
	}
}
