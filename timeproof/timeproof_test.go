package timeproof

import (
	"math/big"
	"testing"
)

// floydInterval is an independent reference solver: it builds the same
// difference-constraint system and runs Floyd-Warshall over exact rationals,
// returning the feasible [lo, hi] of the query node and whether the system is
// consistent. nil bounds denote unboundedness.
func floydInterval(t *testing.T, q *Query, kept []bool) (lo, hi *Rat, feasible bool) {
	t.Helper()
	nodes, edges := buildGraph(q, kept)
	index := map[NodeLabel]int{}
	for i, n := range nodes {
		index[n] = i
	}
	n := len(nodes)
	bigEnough := RatInt(1 << 30)
	mat := make([][]*Rat, n)
	for i := range mat {
		mat[i] = make([]*Rat, n)
		for j := range mat[i] {
			if i == j {
				mat[i][j] = RatInt(0)
			} else {
				mat[i][j] = ratCopy(bigEnough)
			}
		}
	}
	for _, e := range edges {
		u, v := index[e.From], index[e.To]
		if mat[u][v].Cmp(e.Weight) > 0 {
			mat[u][v] = ratCopy(e.Weight)
		}
	}
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				cand := new(Rat).Add(mat[i][k], mat[k][j])
				if cand.Cmp(mat[i][j]) < 0 {
					mat[i][j] = cand
				}
			}
		}
	}
	for i := 0; i < n; i++ {
		if mat[i][i].Sign() < 0 {
			return nil, nil, false
		}
	}
	o, qn := index[originNode()], index[queryNode()]
	hi = mat[o][qn]
	lo = negRat(mat[qn][o])
	if hi.Cmp(bigEnough) >= 0 {
		hi = nil
	}
	if mat[qn][o].Cmp(bigEnough) >= 0 {
		lo = nil
	}
	return lo, hi, true
}

func clock1to2() *Clock {
	return &Clock{Segments: []DriftSeg{
		{From: RatInt(0), To: RatInt(100), RateLo: NewRat(1, 4), RateHi: RatInt(1)},
	}}
}

func baseReceipt() *Receipt {
	return &Receipt{
		Nonce:       "n1",
		ServerID:    "srv-a",
		Domain:      "A",
		Upstream:    "up1",
		SendMono:    RatInt(10),
		RecvMono:    RatInt(14),
		ProcessLo:   RatInt(0),
		ProcessHi:   RatInt(0),
		OneWayDelay: RatInt(1),
		SignedLo:    RatInt(100),
		SignedHi:    RatInt(100),
	}
}

func TestSingleDomainTightInterval(t *testing.T) {
	q := &Query{
		Nonce:     "n1",
		Clock:     clock1to2(),
		Receipts:  []*Receipt{baseReceipt()},
		QueryMono: RatInt(20),
		MaxFaulty: 0,
	}
	sol, err := Analyze(q)
	if err != nil {
		t.Fatal(err)
	}
	if len(sol.Union) != 1 {
		t.Fatalf("want 1 interval, got %d", len(sol.Union))
	}
	it := sol.Union[0]
	if err := sol.Verify(q); err != nil {
		t.Fatalf("verify: %v", err)
	}
	lo, hi, ok := floydInterval(t, q, []bool{true})
	if !ok || lo.Cmp(it.Lo) != 0 || hi.Cmp(it.Hi) != 0 {
		t.Fatalf("floyd cross-check mismatch: %v %v %v", lo, hi, ok)
	}
	if it.Lo.Cmp(it.Hi) >= 0 {
		t.Fatalf("expected a non-degenerate interval, got [%v,%v]", it.Lo, it.Hi)
	}
}

func TestCrossSegmentDrift(t *testing.T) {
	clock := &Clock{Segments: []DriftSeg{
		{From: RatInt(0), To: RatInt(12), RateLo: NewRat(1, 2), RateHi: RatInt(1)},
		{From: RatInt(12), To: RatInt(40), RateLo: NewRat(3, 4), RateHi: RatInt(1)},
	}}
	r := baseReceipt()
	r.SendMono = RatInt(4)
	r.RecvMono = RatInt(20)
	r.OneWayDelay = NewRat(1, 2)
	r.ProcessHi = RatInt(30)
	r.SignedLo = RatInt(50)
	r.SignedHi = RatInt(60)
	q := &Query{
		Nonce:     "n1",
		Clock:     clock,
		Receipts:  []*Receipt{r},
		QueryMono: RatInt(30),
		MaxFaulty: 0,
	}
	sol, err := Analyze(q)
	if err != nil {
		t.Fatal(err)
	}
	it := sol.Union[0]
	lo, hi, ok := floydInterval(t, q, []bool{true})
	if !ok {
		t.Fatal("reference solver reports infeasible")
	}
	if it.Lo.Cmp(lo) != 0 || it.Hi.Cmp(hi) != 0 {
		t.Fatalf("segment drift mismatch: got [%v,%v] floyd [%v,%v]", it.Lo, it.Hi, lo, hi)
	}
	if it.Lo.Cmp(it.Hi) == 0 {
		t.Fatalf("expected a non-degenerate interval, got point %v", it.Lo)
	}
	if err := sol.Verify(q); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestSameUpstreamMultipleSignersAtomicity(t *testing.T) {
	good := baseReceipt()
	bad := &Receipt{
		Nonce:       "n1",
		ServerID:    "srv-b",
		Domain:      "A",
		Upstream:    "up1",
		SendMono:    RatInt(10),
		RecvMono:    RatInt(14),
		ProcessLo:   RatInt(0),
		ProcessHi:   RatInt(0),
		OneWayDelay: RatInt(1),
		SignedLo:    RatInt(900),
		SignedHi:    RatInt(900),
	}
	q := &Query{
		Nonce:     "n1",
		Clock:     clock1to2(),
		Receipts:  []*Receipt{good, bad},
		QueryMono: RatInt(20),
		MaxFaulty: 1,
	}
	sol, err := Analyze(q)
	if err != nil {
		t.Fatal(err)
	}
	if len(sol.Conflicts) == 0 {
		t.Fatal("expected a recorded conflict (negative cycle)")
	}
	for _, c := range sol.Conflicts {
		if err := VerifyCycle(c); err != nil {
			t.Fatalf("bad conflict cycle: %v", err)
		}
	}
	if len(sol.Union) != 1 || sol.Union[0].Lo != nil || sol.Union[0].Hi != nil {
		t.Fatalf("want single unbounded interval, got %+v", sol.Union)
	}
	if _, _, ok := floydInterval(t, q, []bool{true, true}); ok {
		t.Fatal("reference solver should reject the jointly contradictory domain")
	}
	if err := sol.Verify(q); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestEndpointExactlyAtExpiry(t *testing.T) {
	q := &Query{
		Nonce:     "n1",
		Clock:     clock1to2(),
		Receipts:  []*Receipt{baseReceipt()},
		QueryMono: RatInt(20),
		MaxFaulty: 0,
	}
	sol, err := Analyze(q)
	if err != nil {
		t.Fatal(err)
	}
	point := sol.Union[0].Lo

	atNotAfter := Classify(sol, RatInt(0), point)
	if atNotAfter.Verdict != VerdictExpired {
		t.Fatalf("time == notAfter must be expired, got %s", atNotAfter.Verdict)
	}
	atNotBefore := Classify(sol, point, RatInt(100000))
	if atNotBefore.Verdict != VerdictValid {
		t.Fatalf("time == notBefore must be valid, got %s", atNotBefore.Verdict)
	}
	lo := sol.Union[0].Lo
	hi := sol.Union[0].Hi
	covering := Classify(sol, new(big.Rat).Sub(lo, RatInt(1)), new(big.Rat).Add(hi, RatInt(1)))
	if covering.Verdict != VerdictValid {
		t.Fatalf("window covering the whole interval must be valid, got %s", covering.Verdict)
	}
	_ = point
}

func TestIncompatibleCycleConflictNotExpiry(t *testing.T) {
	mk := func(domain string, signed int64) *Receipt {
		r := baseReceipt()
		r.Domain = domain
		r.ServerID = "srv-" + domain
		r.SignedLo = RatInt(signed)
		r.SignedHi = RatInt(signed)
		return r
	}
	a1 := mk("A", 100)
	a2 := mk("A", 9999)
	b1 := mk("B", 100)
	b2 := mk("B", 9999)
	q := &Query{
		Nonce:     "n1",
		Clock:     clock1to2(),
		Receipts:  []*Receipt{a1, a2, b1, b2},
		QueryMono: RatInt(20),
		MaxFaulty: 1,
	}
	sol, err := Analyze(q)
	if err != nil {
		t.Fatal(err)
	}
	if len(sol.Outcomes) != 0 {
		t.Fatalf("want no consistent outcome, got %d", len(sol.Outcomes))
	}
	if len(sol.Conflicts) == 0 {
		t.Fatal("want conflict evidence")
	}
	cls := Classify(sol, RatInt(0), RatInt(100000))
	if cls.Verdict != VerdictConflict {
		t.Fatalf("want conflict verdict, got %s", cls.Verdict)
	}
	if err := sol.Verify(q); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func widthOf(it Interval) *Rat {
	if it.Lo == nil || it.Hi == nil {
		return nil
	}
	return new(Rat).Sub(it.Hi, it.Lo)
}

func TestLateReceiptNarrowsOlderConclusion(t *testing.T) {
	first := baseReceipt()
	q1 := &Query{
		Nonce:     "n1",
		Clock:     clock1to2(),
		Receipts:  []*Receipt{first},
		QueryMono: RatInt(20),
		MaxFaulty: 0,
	}
	sol1, err := Analyze(q1)
	if err != nil {
		t.Fatal(err)
	}
	wide := sol1.Union[0]

	second := &Receipt{
		Nonce:       "n1",
		ServerID:    "srv-a2",
		Domain:      "A",
		Upstream:    "up1",
		SendMono:    RatInt(16),
		RecvMono:    RatInt(19),
		ProcessLo:   RatInt(0),
		ProcessHi:   RatInt(1),
		OneWayDelay: RatInt(1),
		SignedLo:    RatInt(104),
		SignedHi:    RatInt(104),
	}
	q2 := &Query{
		Nonce:     "n1",
		Clock:     clock1to2(),
		Receipts:  []*Receipt{first, second},
		QueryMono: RatInt(20),
		MaxFaulty: 0,
	}
	sol2, err := Analyze(q2)
	if err != nil {
		t.Fatal(err)
	}
	narrow := sol2.Union[0]

	if narrow.Lo == nil || narrow.Hi == nil || wide.Lo == nil || wide.Hi == nil {
		t.Fatal("expected bounded intervals for narrowing comparison")
	}
	if narrow.Lo.Cmp(wide.Lo) < 0 || narrow.Hi.Cmp(wide.Hi) > 0 {
		t.Fatalf("late receipt widened: wide [%v,%v] narrow [%v,%v]",
			wide.Lo, wide.Hi, narrow.Lo, narrow.Hi)
	}
	if widthOf(narrow).Cmp(widthOf(wide)) >= 0 {
		t.Fatalf("late receipt did not shrink width: wide %v narrow %v",
			widthOf(wide), widthOf(narrow))
	}
	lo, hi, ok := floydInterval(t, q2, []bool{true, true})
	if !ok || lo.Cmp(narrow.Lo) != 0 || hi.Cmp(narrow.Hi) != 0 {
		t.Fatalf("floyd cross-check mismatch: %v %v %v", lo, hi, ok)
	}
	if err := sol2.Verify(q2); err != nil {
		t.Fatalf("verify: %v", err)
	}
}
