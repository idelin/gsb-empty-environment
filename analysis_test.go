package netsec

import (
	"reflect"
	"testing"
)

func mustGuard(op Op, value int64) Guard {
	return Guard{Op: op, Value: value}
}

func TestSingleRecursionUnrollsEveryLiveFrame(t *testing.T) {
	p := NewProgram("f").
		Function("f", 100).
		CallWith("f", CallNormal, -1, mustGuard(OpGreater, 0)).
		Build()

	for _, test := range []struct {
		param  int64
		stack  int64
		frames int
	}{
		{param: 0, stack: 100, frames: 1},
		{param: 1, stack: 200, frames: 2},
		{param: 3, stack: 400, frames: 4},
		{param: 8, stack: 900, frames: 9},
	} {
		result := Analyze(p, test.param)
		if result.Status != StatusProven || !result.Known {
			t.Fatalf("param %d: status = %v, reason = %q", test.param, result.Status, result.Reason)
		}
		if result.MaxStack != test.stack {
			t.Fatalf("param %d: max stack = %d, want %d", test.param, result.MaxStack, test.stack)
		}
		if result.MaxLiveFrames != test.frames {
			t.Fatalf("param %d: live frames = %d, want %d", test.param, result.MaxLiveFrames, test.frames)
		}
		if result.MaxDepth != test.frames {
			t.Fatalf("param %d: depth = %d, want %d", test.param, result.MaxDepth, test.frames)
		}
		if got := len(result.Checks); got != test.frames-1 {
			t.Fatalf("param %d: checks = %d, want %d", test.param, got, test.frames-1)
		}
		for _, check := range result.Checks {
			if !check.Fits {
				t.Fatalf("param %d: call check does not fit: %+v", test.param, check)
			}
		}
		peak := int64(0)
		for _, step := range result.Path {
			if step.Live > peak {
				peak = step.Live
			}
		}
		if peak != result.MaxStack {
			t.Fatalf("param %d: witness peak = %d, want %d", test.param, peak, result.MaxStack)
		}
	}
}

func TestTailRecursionReleasesCallerFrame(t *testing.T) {
	p := NewProgram("f").
		Function("f", 100).
		CallWith("f", CallTail, -1, mustGuard(OpGreater, 0)).
		Build()

	result := Analyze(p, 20)
	if result.MaxStack != 100 {
		t.Fatalf("max stack = %d, want 100", result.MaxStack)
	}
	if result.MaxLiveFrames != 1 {
		t.Fatalf("live frames = %d, want 1", result.MaxLiveFrames)
	}
	if result.MaxDepth != 21 {
		t.Fatalf("witness depth = %d, want 21", result.MaxDepth)
	}
	for i, step := range result.Path {
		wantTail := i > 0
		if step.TailCall != wantTail {
			t.Fatalf("step %d tail = %v, want %v", i, step.TailCall, wantTail)
		}
		if step.Live != 100 {
			t.Fatalf("step %d live = %d, want 100", i, step.Live)
		}
	}
}

func TestMutualRecursionNormalAndTail(t *testing.T) {
	normal := NewProgram("f").
		Function("f", 100).
		CallWith("g", CallNormal, -1, mustGuard(OpGreater, 0)).
		Function("g", 50).
		CallWith("f", CallNormal, 0, mustGuard(OpGreater, 0)).
		Build()
	result := Analyze(normal, 3)
	if result.MaxStack != 450 || result.MaxLiveFrames != 6 || result.MaxDepth != 6 {
		t.Fatalf("normal mutual: stack=%d frames=%d depth=%d", result.MaxStack, result.MaxLiveFrames, result.MaxDepth)
	}
	wantPath := []string{"f", "g", "f", "g", "f", "g"}
	if !reflect.DeepEqual(result.PathNames, wantPath) {
		t.Fatalf("normal mutual path = %v, want %v", result.PathNames, wantPath)
	}

	tail := NewProgram("f").
		Function("f", 100).
		CallWith("g", CallTail, -1, mustGuard(OpGreater, 0)).
		Function("g", 50).
		CallWith("f", CallTail, 0, mustGuard(OpGreater, 0)).
		Build()
	result = Analyze(tail, 3)
	if result.MaxStack != 100 || result.MaxLiveFrames != 1 || result.MaxDepth != 6 {
		t.Fatalf("tail mutual: stack=%d frames=%d depth=%d", result.MaxStack, result.MaxLiveFrames, result.MaxDepth)
	}
	for i, step := range result.Path {
		if step.Live > 100 {
			t.Fatalf("tail mutual step %d live = %d", i, step.Live)
		}
	}
}

func TestOneGuardedEdgeTerminatesMutualCycle(t *testing.T) {
	p := NewProgram("f").
		Function("f", 100).
		CallWith("g", CallNormal, -1, mustGuard(OpGreater, 0)).
		Function("g", 50).
		Call("f", 0).
		Build()
	result := Analyze(p, 3)
	if result.Status != StatusProven {
		t.Fatalf("status = %v, reason = %q", result.Status, result.Reason)
	}
	if result.MaxStack != 550 {
		t.Fatalf("max stack = %d, want 550", result.MaxStack)
	}
}

func TestFramesReleaseBeforeSiblingCall(t *testing.T) {
	p := NewProgram("f").
		Function("f", 100).
		Call("g", 0).
		Call("h", 0).
		Function("g", 200).
		Call("g1", 0).
		Function("g1", 100).
		Function("h", 50).
		Call("h1", 0).
		Function("h1", 400).
		Build()

	result := Analyze(p, 0)
	if result.MaxStack != 550 {
		t.Fatalf("max stack = %d, want 550", result.MaxStack)
	}
	if !reflect.DeepEqual(result.PathNames, []string{"f", "h", "h1"}) {
		t.Fatalf("path = %v, want f -> h -> h1", result.PathNames)
	}
	if len(result.Checks) != 2 {
		t.Fatalf("checks = %d, want 2", len(result.Checks))
	}
	if result.Checks[0].Callee != "h" {
		t.Fatalf("first check = %q, want h", result.Checks[0].Callee)
	}
}

func TestTransitiveFrameRequirement(t *testing.T) {
	p := NewProgram("f").
		Function("f", 10).
		Call("g", 0).
		Function("g", 20).
		Call("h", 0).
		Function("h", 30).
		CallWith("h", CallNormal, -1, mustGuard(OpGreater, 0)).
		Build()
	result := Analyze(p, 2)
	if result.MaxStack != 120 {
		t.Fatalf("max stack = %d, want 120", result.MaxStack)
	}
	want := []string{"f", "g", "h", "h", "h"}
	if !reflect.DeepEqual(result.PathNames, want) {
		t.Fatalf("path = %v, want %v", result.PathNames, want)
	}
	if result.CallSites[0].Required != 120 {
		t.Fatalf("entry call requirement = %d, want 120", result.CallSites[0].Required)
	}
}

func TestUnknownForNonDecreasingCycles(t *testing.T) {
	for name, p := range map[string]Program{
		"zero mutual": NewProgram("f").
			Function("f", 100).Call("g", 0).
			Function("g", 100).Call("f", 0).Build(),
		"positive self": NewProgram("f").
			Function("f", 100).Call("f", 1).Build(),
		"zero self": NewProgram("f").
			Function("f", 100).Call("f", 0).Build(),
	} {
		result := Analyze(p, 0)
		if result.Status != StatusUnknown || result.Known {
			t.Fatalf("%s: status = %v, want unknown", name, result.Status)
		}
		if result.Reason == "" {
			t.Fatalf("%s: missing unknown reason", name)
		}
	}
}

func TestDecreasingCycleUsesNonNegativeBudget(t *testing.T) {
	p := NewProgram("f").
		Function("f", 100).
		Call("f", -1).
		Build()
	result := Analyze(p, 4)
	if result.Status != StatusProven || result.MaxStack != 500 || result.MaxDepth != 5 {
		t.Fatalf("status=%v stack=%d depth=%d", result.Status, result.MaxStack, result.MaxDepth)
	}
	negative := Analyze(p, 4, Options{AllowNegativeParameters: true})
	if negative.Status != StatusUnknown {
		t.Fatalf("allow-negative status = %v, want unknown", negative.Status)
	}

	guarded := NewProgram("f").
		Function("f", 100).
		CallWith("f", CallNormal, -1, mustGuard(OpGreater, 0)).
		Build()
	if got := Analyze(guarded, 4, Options{AllowNegativeParameters: true}).MaxStack; got != 500 {
		t.Fatalf("guarded allow-negative max stack = %d", got)
	}
}

func TestUnreachableCycleDoesNotMakeEntryUnknown(t *testing.T) {
	p := NewProgram("f").
		Function("f", 10).
		Function("unused", 20).
		Call("unused", 0).
		Build()
	result := Analyze(p, 0)
	if result.Status != StatusProven || result.MaxStack != 10 {
		t.Fatalf("status=%v stack=%d reason=%q", result.Status, result.MaxStack, result.Reason)
	}
}

func TestCycleWithPositiveEdgeButNegativeTotal(t *testing.T) {
	p := NewProgram("f").
		Function("f", 100).
		CallWith("g", CallNormal, 1, mustGuard(OpGreater, 0)).
		Function("g", 50).
		CallWith("f", CallNormal, -2, mustGuard(OpGreater, 2)).
		Build()
	result := Analyze(p, 5)
	if result.Status != StatusProven {
		t.Fatalf("status = %v, reason = %q", result.Status, result.Reason)
	}
	if result.MaxStack <= 0 {
		t.Fatalf("max stack = %d", result.MaxStack)
	}
	peak := int64(0)
	for _, step := range result.Path {
		if step.Live > peak {
			peak = step.Live
		}
	}
	if peak != result.MaxStack {
		t.Fatalf("witness peak = %d, analysis peak = %d", peak, result.MaxStack)
	}
}

func TestStackLimitAndCallChecks(t *testing.T) {
	p := NewProgram("f").
		Function("f", 100).
		CallWith("f", CallNormal, -1, mustGuard(OpGreater, 0)).
		Build()

	fits := Analyze(p, 5, Options{StackLimit: 600})
	if !fits.Fits || !fits.HasLimit {
		t.Fatalf("limit 600 should fit, got %+v", fits)
	}
	for i, check := range fits.Checks {
		if !check.Fits {
			t.Fatalf("check %d should fit: %+v", i, check)
		}
		wantAdditional := int64(500 - 100*i)
		if check.Additional != wantAdditional {
			t.Fatalf("check %d additional = %d, want %d", i, check.Additional, wantAdditional)
		}
	}
	overflow := Analyze(p, 5, Options{StackLimit: 599})
	if overflow.Fits {
		t.Fatalf("limit 599 should not fit")
	}
	if overflow.MaxStack != 600 {
		t.Fatalf("overflow max stack = %d", overflow.MaxStack)
	}
}

func TestGuardControlsFeasibility(t *testing.T) {
	p := NewProgram("f").
		Function("f", 100).
		CallWith("g", CallNormal, 0, mustGuard(OpGreater, 0)).
		Function("g", 1000).
		Build()
	if got := Analyze(p, 0).MaxStack; got != 100 {
		t.Fatalf("guarded call at zero: max stack = %d, want 100", got)
	}
	if got := Analyze(p, 3).MaxStack; got != 1100 {
		t.Fatalf("enabled call: max stack = %d, want 1100", got)
	}
}

func TestGuardComparisonOperators(t *testing.T) {
	tests := []struct {
		guard Guard
		value int64
		want  bool
	}{
		{Guard{}, -5, true},
		{mustGuard(OpLess, 0), -1, true},
		{mustGuard(OpLess, 0), 0, false},
		{mustGuard(OpLessEqual, 0), 0, true},
		{mustGuard(OpGreater, 0), 1, true},
		{mustGuard(OpGreaterEqual, 0), 0, true},
		{mustGuard(OpEqual, 2), 2, true},
		{mustGuard(OpNotEqual, 2), 2, false},
	}
	for i, test := range tests {
		if got := test.guard.Holds(test.value); got != test.want {
			t.Fatalf("case %d: Holds(%d) = %v, want %v", i, test.value, got, test.want)
		}
	}
}

func TestTailCallWithLargerCalleeFrame(t *testing.T) {
	p := NewProgram("f").
		Function("f", 100).
		CallWith("g", CallTail, 0, Guard{}).
		Function("g", 200).
		Build()
	result := Analyze(p, 0)
	if result.MaxStack != 200 || result.MaxLiveFrames != 1 || result.MaxDepth != 2 {
		t.Fatalf("stack=%d frames=%d depth=%d", result.MaxStack, result.MaxLiveFrames, result.MaxDepth)
	}
	if len(result.Path) != 2 || result.Path[1].Live != 200 || !result.Path[1].TailCall {
		t.Fatalf("path = %+v", result.Path)
	}
}

func TestNormalThenTailRelease(t *testing.T) {
	p := NewProgram("f").
		Function("f", 100).
		Call("g", 0).
		Function("g", 50).
		CallWith("h", CallTail, 0, Guard{}).
		Function("h", 300).
		Build()
	result := Analyze(p, 0)
	if result.MaxStack != 400 {
		t.Fatalf("max stack = %d, want 400", result.MaxStack)
	}
	if got := result.Checks[1].LiveBefore; got != 150 {
		t.Fatalf("live before tail call = %d, want 150", got)
	}
	if got := result.Checks[1].Peak; got != 400 {
		t.Fatalf("tail call peak = %d, want 400", got)
	}
}

func TestHostWrappers(t *testing.T) {
	p := NewProgram("f").
		Function("f", 32).
		Build()
	if got := Check(p, 0).MaxStack; got != 32 {
		t.Fatalf("Check max stack = %d", got)
	}
	if got := AnalyzeProgram(p, 0).MaxStack; got != 32 {
		t.Fatalf("AnalyzeProgram max stack = %d", got)
	}
	if got := AnalyzeWithOptions(p, 0, Options{StackLimit: 32}).MaxStack; got != 32 {
		t.Fatalf("AnalyzeWithOptions max stack = %d", got)
	}
}

func TestAlternateInputSpellings(t *testing.T) {
	p := Program{
		Entry: "f",
		Funcs: []Function{{
			Name:  "f",
			Frame: 100,
			Calls: []Edge{{
				To:        "f",
				Tail:      true,
				Decrement: 1,
				Cond:      mustGuard(OpGreater, 0),
			}},
		}},
	}
	result := Analyze(p, 4, Options{Limit: 100})
	if !result.Known || result.MaxStack != 100 || !result.Fits {
		t.Fatalf("result = %+v", result)
	}
	for _, step := range result.Path {
		if step.FrameSize != 100 {
			t.Fatalf("frame size = %d", step.FrameSize)
		}
	}
}

func TestAcyclicPositiveDeltaIsFinite(t *testing.T) {
	p := NewProgram("f").
		Function("f", 10).
		Call("g", 7).
		Function("g", 20).
		Build()
	result := Analyze(p, 0)
	if result.Status != StatusProven || result.MaxStack != 30 {
		t.Fatalf("status=%v stack=%d", result.Status, result.MaxStack)
	}
}

func TestAnalyzeStrictRejectsMalformedProgram(t *testing.T) {
	p := NewProgram("f").
		Function("f", -1).
		Build()
	result, err := AnalyzeStrict(p, 0)
	if err == nil {
		t.Fatalf("expected validation error, got %+v", result)
	}
	if result.Status != StatusUnknown {
		t.Fatalf("status = %v", result.Status)
	}
}
