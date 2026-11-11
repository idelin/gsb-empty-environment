package absexec

import "testing"

// TestNestedLoops checks two nested back edges: the inner write is only
// guaranteed when both loops are entered, so the zero-iteration path leaves
// the whole buffer uninitialized. The brute oracle must agree.
func TestNestedLoops(t *testing.T) {
	cfg := &CFG{
		BufferSize: 2,
		Entry:      0,
		Nodes: []Node{
			{ID: 0, Op: OpBranch, Succ: []int{1, 4}}, // outer gate: enter or skip
			{ID: 1, Op: OpBranch, Succ: []int{2, 3}}, // inner gate: body or inner latch
			writeNode(2, 0, 2, 3),                    // inner body -> inner latch
			{ID: 3, Op: OpBranch, Succ: []int{1, 0}}, // inner latch: repeat inner or outer latch
			sendNode(4),
		},
		BackEdges: []BackEdge{
			{From: 3, To: 1, MaxCount: 1}, // inner loop
			{From: 3, To: 0, MaxCount: 1}, // outer loop
		},
	}
	report, err := Analyze(Request{CFG: cfg})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !report.Complete {
		t.Fatalf("nested loop run should complete")
	}
	f := report.Sends[0]
	if f.Status != SendUninitialized {
		t.Fatalf("status = %v, want Uninitialized", f.Status)
	}
	for _, u := range f.Uninitialized {
		verifyWitness(t, cfg, u, 4)
	}
	paths, _ := enumerateBrute(cfg)
	must, reached := mustSetAt(cfg, paths, 4)
	if !reached {
		t.Fatal("oracle never reached send")
	}
	assertSameGaps(t, must, 0, 2, f)
}

// TestSendSubrange checks a send that only requires part of the buffer.
func TestSendSubrange(t *testing.T) {
	cfg := &CFG{
		BufferSize: 8,
		Entry:      0,
		Nodes: []Node{
			writeNode(0, 2, 6, 1),
			{ID: 1, Op: OpSend, Lo: 2, Hi: 6},
		},
	}
	report, err := Analyze(Request{CFG: cfg})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if report.Sends[0].Status != SendSafe {
		t.Fatalf("status = %v, want Safe for covered subrange", report.Sends[0].Status)
	}
}

// TestUnreachableSend reports a send that no bounded path can reach.
func TestUnreachableSend(t *testing.T) {
	cfg := &CFG{
		BufferSize: 2,
		Entry:      0,
		Nodes: []Node{
			sendNode(0),
			sendNode(1),
		},
	}
	report, err := Analyze(Request{CFG: cfg})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if report.Sends[0].Status != SendUninitialized {
		t.Fatalf("entry send = %v, want Uninitialized (nothing was written)", report.Sends[0].Status)
	}
	if report.Sends[1].Status != SendUnreachable {
		t.Fatalf("second send = %v, want Unreachable", report.Sends[1].Status)
	}
}

// TestDynWriteNoGuarantee checks a dynamic write whose guaranteed interval is
// empty (LoMax == HiMin): nothing may be claimed as must-written.
func TestDynWriteNoGuarantee(t *testing.T) {
	cfg := &CFG{
		BufferSize: 4,
		Entry:      0,
		Nodes: []Node{
			dynNode(0, 0, 2, 2, 4, 1),
			sendNode(1),
		},
	}
	report, err := Analyze(Request{CFG: cfg})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	f := report.Sends[0]
	if f.Status != SendUninitialized || len(f.Uninitialized) != 1 {
		t.Fatalf("finding = %+v, want one whole-buffer gap", f)
	}
	if f.Uninitialized[0].Interval != (Interval{0, 4}) {
		t.Fatalf("gap = %+v, want [0,4)", f.Uninitialized[0].Interval)
	}
}
