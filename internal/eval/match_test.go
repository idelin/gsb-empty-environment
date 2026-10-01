package eval

import "testing"

// spanFixture builds a validated fixture from a rule tree and events.
func spanFixture(root *Node, events ...Event) *Fixture {
	f := &Fixture{Name: "test", Expected: root, Events: events}
	if err := f.Validate(); err != nil {
		panic(err)
	}
	return f
}

func starts(pairs ...string) []Event {
	var evs []Event
	for i := 0; i+1 < len(pairs); i += 2 {
		evs = append(evs, Event{Type: "start", ID: pairs[i], Name: pairs[i+1]})
	}
	return evs
}

func withParent(evs []Event, id, parent string) []Event {
	for i := range evs {
		if evs[i].ID == id {
			evs[i].Parent = parent
		}
	}
	return evs
}

func withEnds(evs []Event, ids ...string) []Event {
	for _, id := range ids {
		evs = append(evs, Event{Type: "end", ID: id})
	}
	return evs
}

func evalAll(t *testing.T, f *Fixture) Report {
	t.Helper()
	r := Evaluate(f, len(f.Events))
	if r.Indeterminate {
		t.Fatalf("unexpected indeterminate verdict")
	}
	return r
}

func TestExactMatchScores100(t *testing.T) {
	root := &Node{Kind: KindSpan, Name: "req", Children: []*Node{
		{Kind: KindSpan, Name: "a"},
		{Kind: KindSpan, Name: "b"},
	}}
	evs := starts("s1", "req", "s2", "a", "s3", "b")
	evs = withParent(evs, "s2", "s1")
	evs = withParent(evs, "s3", "s1")
	evs = withEnds(evs, "s1", "s2", "s3")
	r := evalAll(t, spanFixture(root, evs...))
	if r.Score != 100 {
		t.Fatalf("score = %v, want 100 (%+v)", r.Score, r.Counts)
	}
	if r.Counts != (Counts{RequiredTotal: 3, Matched: 3}) {
		t.Fatalf("counts = %+v", r.Counts)
	}
}

func TestMissingChild(t *testing.T) {
	root := &Node{Kind: KindSpan, Name: "req", Children: []*Node{
		{Kind: KindSpan, Name: "a"},
		{Kind: KindSpan, Name: "b"},
	}}
	evs := starts("s1", "req", "s2", "a")
	evs = withParent(evs, "s2", "s1")
	evs = withEnds(evs, "s1", "s2")
	r := evalAll(t, spanFixture(root, evs...))
	if r.Counts.Missing != 1 || r.Counts.Matched != 2 || r.Counts.RequiredTotal != 3 {
		t.Fatalf("counts = %+v", r.Counts)
	}
}

func TestDuplicatesDoNotRaiseCompleteness(t *testing.T) {
	root := &Node{Kind: KindSpan, Name: "job", Children: []*Node{
		{Kind: KindSpan, Name: "worker"},
		{Kind: KindSpan, Name: "cleanup"},
	}}
	evs := starts("s1", "job", "s2", "worker", "s3", "worker", "s4", "worker")
	for _, id := range []string{"s2", "s3", "s4"} {
		evs = withParent(evs, id, "s1")
	}
	evs = withEnds(evs, "s1", "s2", "s3", "s4")
	r := evalAll(t, spanFixture(root, evs...))
	c := r.Counts
	if c.Matched != 2 || c.Missing != 1 || c.Duplicates != 2 {
		t.Fatalf("counts = %+v", c)
	}
	// score = 100*(2/3 - 0.5*2/3) = 33.33: quantity cannot offset the gap.
	want := 100.0 * (2.0/3.0 - 1.0/3.0)
	if !approx(r.Score, want) {
		t.Fatalf("score = %v, want %v", r.Score, want)
	}
}

func TestMisplacedSpanWrongParent(t *testing.T) {
	root := &Node{Kind: KindSpan, Name: "req", Children: []*Node{
		{Kind: KindSpan, Name: "sub", Children: []*Node{
			{Kind: KindSpan, Name: "leaf"},
		}},
	}}
	// leaf exists but hangs directly under root, not under sub.
	evs := starts("s1", "req", "s2", "sub", "s3", "leaf")
	evs = withParent(evs, "s2", "s1")
	evs = withParent(evs, "s3", "s1")
	evs = withEnds(evs, "s1", "s2", "s3")
	r := evalAll(t, spanFixture(root, evs...))
	c := r.Counts
	if c.Missing != 1 || c.Misplaced != 1 || c.Matched != 2 {
		t.Fatalf("counts = %+v", c)
	}
}

func TestChoicePicksPresentBranch(t *testing.T) {
	root := &Node{Kind: KindSpan, Name: "pay", Children: []*Node{
		{Kind: KindChoice, Children: []*Node{
			{Kind: KindSpan, Name: "card"},
			{Kind: KindSpan, Name: "paypal"},
		}},
	}}
	evs := starts("s1", "pay", "s2", "paypal")
	evs = withParent(evs, "s2", "s1")
	evs = withEnds(evs, "s1", "s2")
	r := evalAll(t, spanFixture(root, evs...))
	if r.Score != 100 || r.Counts.Missing != 0 {
		t.Fatalf("score=%v counts=%+v", r.Score, r.Counts)
	}
}

func TestChoiceBothBranchesIsViolation(t *testing.T) {
	root := &Node{Kind: KindSpan, Name: "pay", Children: []*Node{
		{Kind: KindChoice, Children: []*Node{
			{Kind: KindSpan, Name: "card"},
			{Kind: KindSpan, Name: "paypal"},
		}},
	}}
	evs := starts("s1", "pay", "s2", "card", "s3", "paypal")
	evs = withParent(evs, "s2", "s1")
	evs = withParent(evs, "s3", "s1")
	evs = withEnds(evs, "s1", "s2", "s3")
	r := evalAll(t, spanFixture(root, evs...))
	if r.Counts.Misplaced != 1 || r.Counts.Matched != 2 {
		t.Fatalf("counts = %+v", r.Counts)
	}
}

func TestRepeatRange(t *testing.T) {
	root := &Node{Kind: KindSpan, Name: "req", Children: []*Node{
		{Kind: KindSpan, Name: "db", Min: 1, Max: 3},
	}}
	mk := func(n int) *Fixture {
		evs := starts("s1", "req")
		var ids []string
		for i := 0; i < n; i++ {
			id := string(rune('a' + i))
			evs = append(evs, Event{Type: "start", ID: id, Name: "db", Parent: "s1"})
			ids = append(ids, id)
		}
		evs = withEnds(append(evs, Event{Type: "end", ID: "s1"}), ids...)
		return spanFixture(root, evs...)
	}
	r2 := evalAll(t, mk(2)) // within [1,3]: no penalty
	if r2.Counts.Duplicates != 0 || r2.Counts.Missing != 0 || r2.Score != 100 {
		t.Fatalf("n=2: score=%v counts=%+v", r2.Score, r2.Counts)
	}
	r4 := evalAll(t, mk(4)) // beyond Max: one duplicate
	if r4.Counts.Duplicates != 1 || r4.Counts.Missing != 0 {
		t.Fatalf("n=4: counts=%+v", r4.Counts)
	}
	r0 := evalAll(t, mk(0)) // below Min: one missing
	if r0.Counts.Missing != 1 {
		t.Fatalf("n=0: counts=%+v", r0.Counts)
	}
}

func TestUnclosedIsNotMissing(t *testing.T) {
	root := &Node{Kind: KindSpan, Name: "req", Children: []*Node{
		{Kind: KindSpan, Name: "db"},
	}}
	evs := starts("s1", "req", "s2", "db")
	evs = withParent(evs, "s2", "s1")
	evs = withEnds(evs, "s1") // s2 never ends
	r := evalAll(t, spanFixture(root, evs...))
	c := r.Counts
	if c.Unclosed != 1 || c.Missing != 0 || c.Matched != 2 {
		t.Fatalf("counts = %+v", c)
	}
}

func TestConditionPrunesNode(t *testing.T) {
	root := &Node{Kind: KindSpan, Name: "req", Children: []*Node{
		{Kind: KindSpan, Name: "sms", When: map[string]string{"channel": "mobile"}},
		{Kind: KindSpan, Name: "web"},
	}}
	evs := starts("s1", "req", "s2", "web")
	evs = withParent(evs, "s2", "s1")
	evs = withEnds(evs, "s1", "s2")
	f := spanFixture(root, evs...)
	f.TraceAttrs = map[string]string{"channel": "web"}
	r := evalAll(t, f)
	if r.Counts.RequiredTotal != 2 || r.Score != 100 {
		t.Fatalf("condition not pruned: score=%v counts=%+v", r.Score, r.Counts)
	}
}

func TestBudgetExhaustedIsIndeterminate(t *testing.T) {
	root := &Node{Kind: KindSpan, Name: "req", Children: []*Node{
		{Kind: KindSpan, Name: "a"},
	}}
	evs := starts("s1", "req", "s2", "a")
	evs = withParent(evs, "s2", "s1")
	f := spanFixture(root, evs...)
	f.Budget = 1
	r := Evaluate(f, len(f.Events))
	if !r.Indeterminate {
		t.Fatalf("want indeterminate, got score %v", r.Score)
	}
}

func TestRootMissingIsCritical(t *testing.T) {
	root := &Node{Kind: KindSpan, Name: "req", Children: []*Node{
		{Kind: KindSpan, Name: "a"},
		{Kind: KindSpan, Name: "b"},
	}}
	// Many children, no root: still critical.
	evs := starts("s2", "a", "s3", "b")
	evs = withParent(evs, "s2", "s1")
	evs = withParent(evs, "s3", "s1")
	evs = withEnds(evs, "s2", "s3")
	r := evalAll(t, spanFixture(root, evs...))
	if !r.RootMissing || len(r.Critical) == 0 {
		t.Fatalf("root missing not flagged: %+v", r)
	}
	if r.Score > RootCap {
		t.Fatalf("score %v exceeds root cap", r.Score)
	}
	if r.Counts.Misplaced != 2 {
		t.Fatalf("orphan children should be misplaced: %+v", r.Counts)
	}
}

func approx(a, b float64) bool {
	d := a - b
	return d < 0.01 && d > -0.01
}
