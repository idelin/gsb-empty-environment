package netsec

import (
	"math/big"
	"strings"
	"testing"
)

func TestVerifyIdentityCipher(t *testing.T) {
	c := mustCipher(t, identitySBox(), identityPerm(), 2)
	keys := []uint16{0xBEEF, 0x0042}
	count, prob, err := c.CountDifferential(keys, 0x0001, 0x0001)
	if err != nil {
		t.Fatalf("CountDifferential: %v", err)
	}
	if count != StateSize || prob.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("identity: count=%d prob=%s, want 65536 and 1", count, prob)
	}
	count, _, err = c.CountDifferential(keys, 0x0001, 0x0002)
	if err != nil {
		t.Fatalf("CountDifferential: %v", err)
	}
	if count != 0 {
		t.Fatalf("identity: count=%d, want 0 for unreachable output difference", count)
	}
	if _, _, err := c.CountDifferential(keys[:1], 1, 1); err == nil {
		t.Fatalf("wrong round-key count must be rejected")
	}
}

func TestVerifyOneRoundMatchesDDT(t *testing.T) {
	c := mustCipher(t, presentSBox(), presentPerm16(), 1)
	ddt := c.DDT()
	inDiff := uint16(0x00A5)
	outDiff := uint16(0x1234)
	u := c.unpermTab[outDiff]
	want := uint32(1)
	for nib := 0; nib < NibbleCount; nib++ {
		a := (inDiff >> (4 * nib)) & 0xF
		b := (u >> (4 * nib)) & 0xF
		want *= uint32(ddt[a][b])
	}
	// For a single round the count is key-independent: the key XOR only
	// re-indexes the plaintext enumeration.
	for _, key := range []uint16{0x0000, 0xFFFF, 0x5A5A} {
		count, prob, err := c.CountDifferential([]uint16{key}, inDiff, outDiff)
		if err != nil {
			t.Fatalf("CountDifferential: %v", err)
		}
		if count != want {
			t.Fatalf("key=%#04x: count=%d, want DDT product %d", key, count, want)
		}
		if prob.Cmp(big.NewRat(int64(want), StateSize)) != 0 {
			t.Fatalf("key=%#04x: prob=%s", key, prob)
		}
	}
}

func TestVerifyMatchesInlineEnumeration(t *testing.T) {
	c := mustCipher(t, presentSBox(), presentPerm16(), 3)
	keys := []uint16{0x1111, 0x2222, 0x3333}
	inDiff := uint16(0x0009)
	hist := make(map[uint16]uint32)
	for x := 0; x < StateSize; x++ {
		d := c.encrypt(uint16(x), keys) ^ c.encrypt(uint16(x)^inDiff, keys)
		hist[d]++
	}
	sampled := 0
	for outDiff, want := range hist {
		count, _, err := c.CountDifferential(keys, inDiff, outDiff)
		if err != nil {
			t.Fatalf("CountDifferential: %v", err)
		}
		if count != want {
			t.Fatalf("outDiff=%#04x: count=%d, want %d", outDiff, count, want)
		}
		sampled++
		if sampled >= 8 {
			break
		}
	}
	var total uint32
	for _, v := range hist {
		total += v
	}
	if total != StateSize {
		t.Fatalf("histogram sums to %d, want %d", total, StateSize)
	}
}

func TestAnalyzeReportSeparatesTrailAndTrueProbability(t *testing.T) {
	c := mustCipher(t, presentSBox(), presentPerm16(), 2)
	req := SearchRequest{
		Starts:  []uint16{0x0007},
		Targets: []uint16{0x0001, 0x0011, 0x0101},
	}
	keys := []uint16{0x0F0F, 0xF0F0}
	rep, err := Analyze(c, req, keys)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !rep.Verified || rep.Search == nil || rep.Search.Best == nil {
		t.Fatalf("report incomplete: %+v", rep)
	}
	if rep.InDiff != rep.Search.Best.Differences[0] ||
		rep.OutDiff != rep.Search.Best.Differences[len(rep.Search.Best.Differences)-1] {
		t.Fatalf("verified pair must be the best trail endpoints")
	}
	if rep.TrueProb.Cmp(big.NewRat(int64(rep.TrueCount), StateSize)) != 0 {
		t.Fatalf("true probability inconsistent with count")
	}
	// The combined true probability merges every trail, so over the
	// key average it is at least any single-trail probability; for the
	// enumerated fixed key we only require consistency with a direct
	// recount.
	count, _, err := c.CountDifferential(keys, rep.InDiff, rep.OutDiff)
	if err != nil {
		t.Fatalf("CountDifferential: %v", err)
	}
	if count != rep.TrueCount {
		t.Fatalf("report count %d != recount %d", rep.TrueCount, count)
	}
	text := rep.String()
	for _, want := range []string{"单条轨迹", "真实差分概率", "逐明文枚举", "不可混为一谈"} {
		if !strings.Contains(text, want) {
			t.Fatalf("report text missing %q:\n%s", want, text)
		}
	}
}

func TestAnalyzeOneRoundTrueEqualsSingleTrail(t *testing.T) {
	// With exactly one round there is precisely one trail between any
	// reachable (input, output) pair, so the combined true probability
	// must equal the single-trail score exactly.
	c := mustCipher(t, presentSBox(), presentPerm16(), 1)
	req := SearchRequest{Starts: []uint16{0x0007}, Targets: []uint16{0x0001}}
	rep, err := Analyze(c, req, []uint16{0x1234})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if rep.Search.Best == nil {
		t.Fatalf("expected a trail")
	}
	if rep.TrueProb.Cmp(rep.Search.Best.Score) != 0 {
		t.Fatalf("1-round: true %s != single trail %s", rep.TrueProb, rep.Search.Best.Score)
	}
}

func TestAnalyzeUnreachableReport(t *testing.T) {
	c := mustCipher(t, identitySBox(), identityPerm(), 2)
	rep, err := Analyze(c, SearchRequest{Starts: []uint16{1}, Targets: []uint16{2}}, []uint16{0, 0})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if rep.Search.Best != nil || rep.TrueCount != 0 {
		t.Fatalf("unreachable target: best=%v trueCount=%d", rep.Search.Best, rep.TrueCount)
	}
	if !strings.Contains(rep.String(), "不存在") {
		t.Fatalf("report must state that no trail exists")
	}
}
