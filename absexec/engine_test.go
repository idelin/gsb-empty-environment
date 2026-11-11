package absexec

import "testing"

func writeNode(id, lo, hi int, succ ...int) Node {
	return Node{ID: id, Op: OpWrite, Lo: lo, Hi: hi, Succ: succ}
}

func dynNode(id, loMin, loMax, hiMin, hiMax int, succ ...int) Node {
	return Node{
		ID: id, Op: OpDynWrite,
		LoMin: loMin, LoMax: loMax, HiMin: hiMin, HiMax: hiMax,
		Succ: succ,
	}
}

func sendNode(id int, succ ...int) Node {
	return Node{ID: id, Op: OpSend, Succ: succ}
}

// TestBranchMerge checks that at a merge only bytes written on BOTH branches
// survive into the must-written set, with an accurate witness path.
func TestBranchMerge(t *testing.T) {
	cfg := &CFG{
		BufferSize: 8,
		Entry:      0,
		Nodes: []Node{
			{ID: 0, Op: OpBranch, Succ: []int{1, 2}},
			writeNode(1, 0, 4, 3),
			writeNode(2, 2, 8, 3),
			sendNode(3),
		},
	}
	report, err := Analyze(Request{CFG: cfg})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !report.Complete {
		t.Fatalf("expected complete report, got unknown")
	}
	f := report.Sends[0]
	if f.Status != SendUninitialized {
		t.Fatalf("status = %v, want SendUninitialized", f.Status)
	}
	got := gapSet(f)
	want := map[int]bool{0: true, 1: true, 4: true, 5: true, 6: true, 7: true}
	if len(got) != len(want) {
		t.Fatalf("gaps = %v, want %v", got, want)
	}
	for b := range want {
		if !got[b] {
			t.Fatalf("byte %d missing from gaps %v", b, got)
		}
	}
	for _, u := range f.Uninitialized {
		verifyWitness(t, cfg, u, 3)
	}

	paths, err := enumerateBrute(cfg)
	if err != nil {
		t.Fatal(err)
	}
	must, reached := mustSetAt(cfg, paths, 3)
	if !reached {
		t.Fatal("brute oracle never reached send")
	}
	assertSameGaps(t, must, 0, 8, f)
}

// TestLoopZeroAndPartial checks the zero-iteration path: even when every
// iteration covers bytes, a send after a loop that may run zero times cannot
// rely on those bytes.
func TestLoopZeroAndPartial(t *testing.T) {
	cfg := &CFG{
		BufferSize: 4,
		Entry:      0,
		Nodes: []Node{
			{ID: 0, Op: OpBranch, Succ: []int{1, 2}},
			writeNode(1, 0, 4, 0),
			sendNode(2),
		},
		BackEdges: []BackEdge{{From: 1, To: 0, MaxCount: 2}},
	}
	report, err := Analyze(Request{CFG: cfg})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := report.Sends[0]
	if f.Status != SendUninitialized {
		t.Fatalf("status = %v, want SendUninitialized (zero-iteration path)", f.Status)
	}
	if len(f.Uninitialized) != 1 || f.Uninitialized[0].Interval != (Interval{0, 4}) {
		t.Fatalf("unexpected findings: %+v", f.Uninitialized)
	}
	ce := f.Uninitialized[0].Counterexample
	if len(ce.Path) != 2 || ce.Path[0] != 0 || ce.Path[1] != 2 {
		t.Fatalf("witness path = %v, want [0 2]", ce.Path)
	}
	verifyWitness(t, cfg, f.Uninitialized[0], 2)

	paths, _ := enumerateBrute(cfg)
	must, reached := mustSetAt(cfg, paths, 2)
	if !reached {
		t.Fatal("brute oracle never reached send")
	}
	assertSameGaps(t, must, 0, 4, f)
}

// TestLoopPartialCoverage checks per-iteration partial writes over a bounded
// loop: when the loop runs at least once but at most twice, the first half is
// guaranteed while the second half depends on taking the second iteration.
func TestLoopPartialCoverage(t *testing.T) {
	cfg := &CFG{
		BufferSize: 4,
		Entry:      1,
		Nodes: []Node{
			writeNode(1, 0, 2, 0), // body: bytes 0..1 each iteration
			{ID: 0, Op: OpBranch, Succ: []int{1, 2}},
			sendNode(2),
		},
		BackEdges: []BackEdge{{From: 1, To: 0, MaxCount: 1}},
	}
	report, err := Analyze(Request{CFG: cfg})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := report.Sends[0]
	if f.Status != SendUninitialized {
		t.Fatalf("status = %v", f.Status)
	}
	got := gapSet(f)
	for b := 0; b < 4; b++ {
		wantGap := b >= 2
		if got[b] != wantGap {
			t.Fatalf("byte %d gap = %v, want %v", b, got[b], wantGap)
		}
	}
	for _, u := range f.Uninitialized {
		verifyWitness(t, cfg, u, 2)
	}
}

// TestDynamicWriteBounds checks that only [LoMax, HiMin) is guaranteed and
// the flexible margins stay uninitialized.
func TestDynamicWriteBounds(t *testing.T) {
	cfg := &CFG{
		BufferSize: 8,
		Entry:      0,
		Nodes: []Node{
			dynNode(0, 0, 2, 5, 8, 1),
			sendNode(1),
		},
	}
	report, err := Analyze(Request{CFG: cfg})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := report.Sends[0]
	if f.Status != SendUninitialized {
		t.Fatalf("status = %v", f.Status)
	}
	got := gapSet(f)
	for b := 0; b < 8; b++ {
		wantGap := b < 2 || b >= 5
		if got[b] != wantGap {
			t.Fatalf("byte %d gap = %v, want %v", b, got[b], wantGap)
		}
	}
	verifyWitness(t, cfg, f.Uninitialized[0], 1)
}

// TestFullCoverageSafe checks a fully covered send is proven safe and that a
// later send after a partial write is not.
func TestFullCoverageSafe(t *testing.T) {
	cfg := &CFG{
		BufferSize: 4,
		Entry:      0,
		Nodes: []Node{
			writeNode(0, 0, 4, 1), // full init
			sendNode(1, 2),        // safe
			{ID: 2, Op: OpBranch, Succ: []int{3, 4}},
			writeNode(3, 0, 1, 5), // only this branch rewrites byte 0
			{ID: 4, Op: OpNoop, Succ: []int{5}},
			sendNode(5),
		},
	}
	report, err := Analyze(Request{CFG: cfg})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if report.Sends[0].Status != SendSafe {
		t.Fatalf("first send = %v, want Safe", report.Sends[0].Status)
	}
	if report.Sends[1].NodeID != 5 || report.Sends[1].Status != SendSafe {
		t.Fatalf("second send = %+v, want node 5 Safe (writes accumulate, never unwrite)", report.Sends[1])
	}

	partial := &CFG{
		BufferSize: 4,
		Entry:      0,
		Nodes: []Node{
			{ID: 0, Op: OpBranch, Succ: []int{1, 2}},
			writeNode(1, 0, 1, 3), // branch A writes byte 0 only
			{ID: 2, Op: OpNoop, Succ: []int{3}},
			sendNode(3),
		},
	}
	preport, err := Analyze(Request{CFG: partial})
	if err != nil {
		t.Fatalf("Analyze partial: %v", err)
	}
	pf := preport.Sends[0]
	if pf.Status != SendUninitialized {
		t.Fatalf("partial send = %v, want Uninitialized", pf.Status)
	}
	got := gapSet(pf)
	for b := 0; b < 4; b++ {
		if !got[b] {
			t.Fatalf("byte %d gap = %v", b, got[b])
		}
	}
	for _, u := range pf.Uninitialized {
		verifyWitness(t, partial, u, 3)
	}
}

// TestBudgetUnknown checks that an exhausted budget yields unknown rather than
// an unsound uninitialized claim, while a sufficient budget completes.
func TestBudgetUnknown(t *testing.T) {
	cfg := &CFG{
		BufferSize: 2,
		Entry:      0,
		Nodes: []Node{
			{ID: 0, Op: OpBranch, Succ: []int{1, 2}},
			{ID: 1, Op: OpNoop, Succ: []int{0}},
			sendNode(2),
		},
		BackEdges: []BackEdge{{From: 1, To: 0, MaxCount: 3}},
	}

	full, err := Analyze(Request{CFG: cfg})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !full.Complete || full.Sends[0].Status != SendUninitialized {
		t.Fatalf("full run: %+v", full)
	}

	limited, err := Analyze(Request{CFG: cfg, Budget: 2})
	if err != nil {
		t.Fatalf("Analyze limited: %v", err)
	}
	if limited.Complete {
		t.Fatalf("limited run should be incomplete")
	}
	if limited.Sends[0].Status != SendUnknown {
		t.Fatalf("limited status = %v, want Unknown", limited.Sends[0].Status)
	}
}

// TestValidationErrors checks malformed inputs are rejected.
func TestValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		cfg  *CFG
	}{
		{"nil", nil},
		{"bad size", &CFG{BufferSize: 0, Entry: 0, Nodes: []Node{sendNode(0)}}},
		{"missing entry", &CFG{BufferSize: 2, Entry: 9, Nodes: []Node{sendNode(0)}}},
		{"dup id", &CFG{BufferSize: 2, Entry: 0, Nodes: []Node{sendNode(0), sendNode(0)}}},
		{"write range", &CFG{BufferSize: 2, Entry: 0, Nodes: []Node{writeNode(0, 1, 3, 1), sendNode(1)}}},
		{"branch arity", &CFG{BufferSize: 2, Entry: 0, Nodes: []Node{{ID: 0, Op: OpBranch, Succ: []int{1}}, sendNode(1)}}},
		{"dyn bounds", &CFG{BufferSize: 8, Entry: 0, Nodes: []Node{dynNode(0, 2, 4, 3, 8, 1), sendNode(1)}}},
		{"uncapped cycle", &CFG{
			BufferSize: 2, Entry: 0,
			Nodes: []Node{{ID: 0, Op: OpBranch, Succ: []int{0, 1}}, sendNode(1)},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Analyze(Request{CFG: tc.cfg}); err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func gapSet(f SendFinding) map[int]bool {
	out := map[int]bool{}
	for _, u := range f.Uninitialized {
		for b := u.Interval.Lo; b < u.Interval.Hi; b++ {
			out[b] = true
		}
	}
	return out
}

func assertSameGaps(t *testing.T, must bitset, lo, hi int, f SendFinding) {
	t.Helper()
	oracleGap := map[int]bool{}
	for b := lo; b < hi; b++ {
		if !must.has(b) {
			oracleGap[b] = true
		}
	}
	if len(oracleGap) == 0 {
		if f.Status != SendSafe {
			t.Fatalf("status = %v, want Safe", f.Status)
		}
		return
	}
	if f.Status != SendUninitialized {
		t.Fatalf("status = %v, want Uninitialized", f.Status)
	}
	got := gapSet(f)
	for b := lo; b < hi; b++ {
		if oracleGap[b] != got[b] {
			t.Fatalf("byte %d: oracle gap=%v reported gap=%v", b, oracleGap[b], got[b])
		}
	}
}

// verifyWitness replays the reported path independently and checks that it
// really reaches the send and really misses the claimed interval.
func verifyWitness(t *testing.T, cfg *CFG, u UninitializedInterval, sendID int) {
	t.Helper()
	nodeByID := map[int]Node{}
	for _, n := range cfg.Nodes {
		nodeByID[n.ID] = n
	}
	path := u.Counterexample.Path
	if len(path) == 0 || path[0] != cfg.Entry || path[len(path)-1] != sendID {
		t.Fatalf("witness %v does not run entry -> send %d", path, sendID)
	}

	backSeen := map[[2]int]int{}
	written := newBitset(cfg.BufferSize)
	for i, id := range path {
		n := nodeByID[id]
		switch n.Op {
		case OpWrite:
			written.set(n.Lo, n.Hi)
		case OpDynWrite:
			if n.HiMin > n.LoMax {
				written.set(n.LoMax, n.HiMin)
			}
		}
		if i+1 < len(path) {
			next := path[i+1]
			if !contains(n.Succ, next) {
				t.Fatalf("witness step %d -> %d is not a CFG edge", id, next)
			}
			for _, be := range cfg.BackEdges {
				if be.From == id && be.To == next {
					backSeen[[2]int{id, next}]++
					if backSeen[[2]int{id, next}] > be.MaxCount {
						t.Fatalf("witness exceeds back-edge cap %d -> %d", id, next)
					}
				}
			}
		}
	}
	for b := u.Interval.Lo; b < u.Interval.Hi; b++ {
		if written.has(b) {
			t.Fatalf("witness writes byte %d inside claimed gap %+v", b, u.Interval)
		}
	}
	missing := written.gapIntervals(0, cfg.BufferSize)
	if len(missing) != len(u.Counterexample.MissingOnPath) {
		t.Fatalf("MissingOnPath = %v, replayed gaps = %v", u.Counterexample.MissingOnPath, missing)
	}
	for i := range missing {
		if missing[i] != u.Counterexample.MissingOnPath[i] {
			t.Fatalf("MissingOnPath[%d] = %v, want %v", i, u.Counterexample.MissingOnPath[i], missing[i])
		}
	}
}

func contains(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// TestExhaustiveSmallBuffers enumerates every path for several small CFGs and
// checks the analyzer must-set against the brute-force intersection.
func TestExhaustiveSmallBuffers(t *testing.T) {
	cfgs := map[string]*CFG{
		"merge": {
			BufferSize: 3, Entry: 0,
			Nodes: []Node{
				{ID: 0, Op: OpBranch, Succ: []int{1, 2}},
				writeNode(1, 0, 2, 3),
				writeNode(2, 1, 3, 3),
				sendNode(3),
			},
		},
		"loop": {
			BufferSize: 3, Entry: 0,
			Nodes: []Node{
				{ID: 0, Op: OpBranch, Succ: []int{1, 2}},
				writeNode(1, 0, 1, 0),
				sendNode(2),
			},
			BackEdges: []BackEdge{{From: 1, To: 0, MaxCount: 2}},
		},
		"dynamic": {
			BufferSize: 4, Entry: 0,
			Nodes: []Node{
				{ID: 0, Op: OpBranch, Succ: []int{1, 2}},
				dynNode(1, 0, 1, 2, 4, 3),
				writeNode(2, 2, 4, 3),
				sendNode(3),
			},
		},
	}
	for name, cfg := range cfgs {
		t.Run(name, func(t *testing.T) {
			report, err := Analyze(Request{CFG: cfg})
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			paths, err := enumerateBrute(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if len(paths) < 2 {
				t.Fatalf("oracle enumerated %d paths, expected branching", len(paths))
			}
			for _, sf := range report.Sends {
				must, reached := mustSetAt(cfg, paths, sf.NodeID)
				if !reached {
					if sf.Status != SendUnreachable {
						t.Fatalf("send %d unreachable in oracle but status %v", sf.NodeID, sf.Status)
					}
					continue
				}
				lo, hi := cfg.node(sf.NodeID).sendRange(cfg.BufferSize)
				assertSameGaps(t, must, lo, hi, sf)
				for _, u := range sf.Uninitialized {
					verifyWitness(t, cfg, u, sf.NodeID)
				}
			}
		})
	}
}
