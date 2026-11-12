package shrink_test

import (
	"testing"

	"example.org/netsec/q0497/shrink"
)

func TestPreconditionGuardsReproduce(t *testing.T) {
	s := mustSchema(t, "packet", "ASSERT-9", []shrink.Field{
		intField("code", 0, 3),
		optIntField("flag", 0, 3),
	})

	pre := func(c *shrink.Candidate) bool {
		v, ok := c.ValueAt(1)
		return ok && v.Int >= 2
	}

	enum, err := shrink.NewEnumerator(s, pre)
	if err != nil {
		t.Fatalf("NewEnumerator: %v", err)
	}
	for {
		cand, ok := enum.Next()
		if !ok {
			break
		}
		v, present := cand.ValueAt(1)
		if !present || v.Int < 2 {
			t.Fatalf("candidate breached precondition: present=%v value=%v", present, v)
		}
	}
	// Only flag values 2 and 3 pass, for each of 4 required values.
	if enum.Rejected == 0 {
		t.Fatal("expected some candidates to be rejected by precondition")
	}
}

func TestShrinkBudgetIncomplete(t *testing.T) {
	s := mustSchema(t, "packet", "ASSERT-10", []shrink.Field{
		intField("code", 0, 4),
		optIntField("flag", 0, 4),
	})

	// Nothing reproduces, so enumeration runs until the budget expires.
	neverFail := func(*shrink.Candidate) bool { return false }

	result, err := shrink.Shrink(shrink.Request{
		Schema:       s,
		Precondition: allowAll,
		Reproduce:    neverFail,
		Budget:       3,
	})
	if err != nil {
		t.Fatalf("Shrink: %v", err)
	}
	if result.Trials != 3 {
		t.Fatalf("Trials = %d, want 3", result.Trials)
	}
	if result.Best != nil {
		t.Fatal("Best should be nil when nothing fails")
	}
	if result.Exhausted || !result.Incomplete {
		t.Fatalf("expected incomplete budget run, got %+v", result)
	}

	// Budget large enough to exhaust the whole domain yields a complete run.
	full, err := shrink.Shrink(shrink.Request{
		Schema:       s,
		Precondition: allowAll,
		Reproduce:    neverFail,
		Budget:       s.Size() + 1,
	})
	if err != nil {
		t.Fatalf("Shrink full: %v", err)
	}
	if !full.Exhausted || full.Incomplete {
		t.Fatalf("expected exhaustive run, got %+v", full)
	}
	if full.Trials != s.Size() {
		t.Fatalf("Trials = %d, want %d", full.Trials, s.Size())
	}
}

func TestShrinkRejectsBadRequest(t *testing.T) {
	s := mustSchema(t, "packet", "ASSERT-11", []shrink.Field{intField("code", 0, 1)})
	if _, err := shrink.Shrink(shrink.Request{Schema: s, Precondition: allowAll, Budget: 1}); err == nil {
		t.Fatal("expected error for nil Reproduce")
	}
	if _, err := shrink.Shrink(shrink.Request{
		Schema:       s,
		Precondition: allowAll,
		Reproduce:    func(*shrink.Candidate) bool { return true },
		Budget:       0,
	}); err == nil {
		t.Fatal("expected error for non-positive budget")
	}
	if _, err := shrink.NewEnumerator(nil, allowAll); err == nil {
		t.Fatal("expected error for nil schema")
	}
	if _, err := shrink.NewEnumerator(s, nil); err == nil {
		t.Fatal("expected error for nil precondition")
	}
}

func TestEnumerationIsDeterministic(t *testing.T) {
	s := mustSchema(t, "packet", "ASSERT-12", []shrink.Field{
		intField("code", -2, 2),
		optStrField("label", 2),
		optIntField("flag", 1, 2),
	})

	drain := func() [][]bool {
		enum, err := shrink.NewEnumerator(s, allowAll)
		if err != nil {
			t.Fatalf("NewEnumerator: %v", err)
		}
		var out [][]bool
		for {
			cand, ok := enum.Next()
			if !ok {
				return out
			}
			present, _ := cand.Snapshot()
			out = append(out, present)
		}
	}

	first := drain()
	second := drain()
	if len(first) != len(second) || len(first) != s.Size() {
		t.Fatalf("lengths differ or mismatch Size(): %d vs %d vs %d", len(first), len(second), s.Size())
	}
	for i := range first {
		for j := range first[i] {
			if first[i][j] != second[i][j] {
				t.Fatalf("non-deterministic enumeration at %d", i)
			}
		}
	}
}

func TestSixteenOptionalFields(t *testing.T) {
	fields := []shrink.Field{intField("code", 0, 0)}
	for i := 0; i < shrink.MaxOptionalFields; i++ {
		fields = append(fields, optIntField("opt", 0, 0))
	}
	s := mustSchema(t, "packet", "ASSERT-13", fields)

	enum, err := shrink.NewEnumerator(s, allowAll)
	if err != nil {
		t.Fatalf("NewEnumerator: %v", err)
	}
	count := 0
	lastOptional := -1
	for {
		cand, ok := enum.Next()
		if !ok {
			break
		}
		count++
		optional := cand.OptionalPresentCount()
		if optional < lastOptional {
			t.Fatalf("optional count went backwards: %d then %d", lastOptional, optional)
		}
		lastOptional = optional
		if cand.TotalBytes() != 2+1+optional {
			t.Fatalf("TotalBytes = %d, want %d", cand.TotalBytes(), 3+optional)
		}
	}
	if count != 1<<shrink.MaxOptionalFields {
		t.Fatalf("enumerated %d, want %d", count, 1<<shrink.MaxOptionalFields)
	}
}
