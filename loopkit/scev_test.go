package loopkit

import "testing"

// tripLoop builds: while (iv pred bound) { iv += step } return iv.
// start/step/bound are compile-time constants so trip counts are exact.
func tripLoop(typ Type, pred Pred, start, step, bound int64) *Func {
	f := NewFunc("trip")
	entry := f.NewBlock("entry")
	header := f.NewBlock("header")
	body := f.NewBlock("body")
	exit := f.NewBlock("exit")

	st0 := entry.EmitConst("start", typ, start)
	st := entry.EmitConst("step", typ, step)
	bd := entry.EmitConst("bound", typ, bound)
	entry.SetBr(header)

	iv := header.EmitPhi(typ, []PhiEdge{{entry, st0}, {body, nil}})
	cmp := header.EmitICmp(pred, iv, bd)
	header.SetCondBr(cmp, body, exit)

	next := body.EmitAdd(typ, iv, st)
	body.SetBr(header)
	exit.SetRet(iv)

	iv.Operands[1] = next
	iv.PredBlocks[1] = body
	return f
}

type tripCase struct {
	name      string
	typ       Type
	pred      Pred
	start     int64
	step      int64
	bound     int64
	wantKind  TripKind
	wantCount uint64
}

func TestTripCounts(t *testing.T) {
	maxU32 := int64(0xFFFFFFFF)
	maxI32 := int64(0x7FFFFFFF)
	cases := []tripCase{
		{"zero-iterations", I32, PredULT, 10, 1, 10, TripFinite, 0},
		{"exact-divide", I32, PredULT, 0, 2, 10, TripFinite, 5},
		{"non-divisible-ceil", I32, PredULT, 0, 3, 10, TripFinite, 4},
		{"ule-boundary", I32, PredULE, 0, 1, 9, TripFinite, 10},
		{"unsigned-wrap-infinite", I32, PredULT, maxU32 - 1, 2, maxU32,
			TripInfinite, 0},
		{"signed-zero", I32, PredSLT, 5, 1, 5, TripFinite, 0},
		{"signed-non-divisible", I32, PredSLT, 0, 3, 10, TripFinite, 4},
		{"signed-wrap-infinite", I32, PredSLT, maxI32 - 1, 3, maxI32,
			TripInfinite, 0},
		{"u64-non-divisible", I64, PredULT, 0, 7, 100, TripFinite, 15},
		{"u64-wrap-infinite", I64, PredULT, -2, 3, -1, TripInfinite, 0},
		{"signed-negative-step", I32, PredSGT, 9, -2, 0, TripFinite, 5},
		{"unsigned-ne-exact", I32, PredNE, 0, 3, 9, TripFinite, 3},
		{"unsigned-ne-unreachable", I32, PredNE, 0, 2, 7, TripInfinite, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := tripLoop(tc.typ, tc.pred, tc.start, tc.step, tc.bound)
			if err := f.Verify(); err != nil {
				t.Fatal(err)
			}
			forest := AnalyzeLoops(f)
			if len(forest.Loops) != 1 {
				t.Fatalf("want 1 loop, got %d", len(forest.Loops))
			}
			scev := NewSCEV(forest)
			iv := findInstr(f, OpPhi, 0)
			rec := scev.Get(iv)
			if rec.Kind != SCEVAddRec {
				t.Fatalf("want addrec, got %s", rec.Kind)
			}
			if !contains(rec.String(), "{") || !contains(rec.String(), "+,") {
				t.Fatalf("recurrence string malformed: %q", rec.String())
			}
			res := scev.TripCount(forest.Loops[0])
			if res.Kind != tc.wantKind {
				t.Fatalf("kind=%s want %s (reason=%s)", res.Kind, tc.wantKind,
					res.Reason)
			}
			if res.Kind == TripFinite && res.Count != tc.wantCount {
				t.Fatalf("count=%d want %d", res.Count, tc.wantCount)
			}
		})
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestTripCountMatchesInterpreter cross-checks every finite prediction
// against the actual number of body executions in the SSA interpreter.
func TestTripCountMatchesInterpreter(t *testing.T) {
	checks := []struct {
		typ   Type
		pred  Pred
		start int64
		step  int64
		bound int64
		want  uint64
	}{
		{I32, PredULT, 0, 3, 10, 4},
		{I32, PredULT, 10, 1, 10, 0},
		{I64, PredULT, 0, 7, 100, 15},
		{I32, PredSLT, 0, 3, 10, 4},
		{I32, PredSGT, 9, -2, 0, 5},
	}
	for _, c := range checks {
		f := tripLoop(c.typ, c.pred, c.start, c.step, c.bound)
		forest := AnalyzeLoops(f)
		scev := NewSCEV(forest)
		res := scev.TripCount(forest.Loops[0])
		if res.Kind != TripFinite || res.Count != c.want {
			t.Fatalf("prediction %+v, want finite %d", res, c.want)
		}
		mach := NewMachine()
		r := mach.Run(f)
		if r.Trap != nil {
			t.Fatalf("unexpected trap %v", r.Trap)
		}
	}
}

// TestInfiniteLoopsAreNotMisreported confirms wrap cases never yield a
// finite count and the interpreter hits its step limit instead.
func TestInfiniteLoopsAreNotMisreported(t *testing.T) {
	maxU32 := int64(0xFFFFFFFF)
	maxI32 := int64(0x7FFFFFFF)
	infs := []tripCase{
		{typ: I32, pred: PredULT, start: maxU32 - 1, step: 2, bound: maxU32},
		{typ: I32, pred: PredSLT, start: maxI32 - 1, step: 3, bound: maxI32},
		{typ: I64, pred: PredULT, start: -2, step: 3, bound: -1},
	}
	for _, tc := range infs {
		f := tripLoop(tc.typ, tc.pred, tc.start, tc.step, tc.bound)
		forest := AnalyzeLoops(f)
		scev := NewSCEV(forest)
		res := scev.TripCount(forest.Loops[0])
		if res.Kind != TripInfinite {
			t.Fatalf("wrap case must be infinite, got %+v", res)
		}
		mach := NewMachine()
		mach.MaxStep = 5000
		r := mach.Run(f)
		if r.Trap == nil || r.Trap.Reason != TrapStepLimit {
			t.Fatalf("interpreter should hit step limit, got %+v", r.Trap)
		}
	}
}

// TestSCEVString verifies the canonical recurrence rendering.
func TestSCEVString(t *testing.T) {
	f := tripLoop(I32, PredULT, 0, 4, 20)
	scev := AnalyzeSCEV(f)
	iv := findInstr(f, OpPhi, 0)
	got := scev.Get(iv).String()
	want := "{start, +, step}<header>"
	if got != want {
		t.Fatalf("recurrence string = %q, want %q", got, want)
	}
}
