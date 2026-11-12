package taskpkg

import "testing"

// TestDifferentialFullEnumeration compares the reference interpreter against
// the compiled program on every assignment of a battery of rules. Agreement is
// required on the full edge sequence, leaf order, marks and forward/deny.
func TestDifferentialFullEnumeration(t *testing.T) {
	rules := buildDifferentialRules()
	for ri, rule := range rules {
		prog, err := Compile(rule)
		if err != nil {
			t.Fatalf("rule %d compile: %v", ri, err)
		}
		for mask := 0; mask < 1<<rule.Inputs; mask++ {
			inputs := decode(mask, rule.Inputs)
			want, err := Interpret(rule, inputs)
			if err != nil {
				t.Fatalf("rule %d mask %b interpret: %v", ri, mask, err)
			}
			got, err := prog.Evaluate(inputs)
			if err != nil {
				t.Fatalf("rule %d mask %b evaluate: %v", ri, mask, err)
			}
			if err := CompareTraces(want, got); err != nil {
				t.Fatalf("rule %d mask %b divergence: %v\nwant edges=%+v\ngot  edges=%+v",
					ri, mask, err, want.Edges, got.Edges)
			}
		}
	}
}

func buildDifferentialRules() []*Rule {
	a := pureCheck(0, "a")
	b := pureCheck(1, "b")
	c := pureCheck(2, "c")

	markA := leafCheck(0, "a", "mA", true, EmitOnTrue)
	markB := leafCheck(1, "b", "mB", false, EmitOnFalse)
	markAlways := leafCheck(2, "c", "mC", true, EmitAlways)

	raising := pureCheck(2, "c")
	raising.RaiseOn = boolPtr(true)

	reqA := Mark{Field: "a", Name: "mA", Trusted: true}
	reqB := Mark{Field: "b", Name: "mB", Trusted: false}
	reqC := Mark{Field: "c", Name: "mC", Trusted: true}

	complexCond := andExpr(10,
		orExpr(11, checkExpr(12, a), checkExpr(13, b)),
		notExpr(14, checkExpr(15, c)),
	)

	rules := []*Rule{
		// 0: marks then forward, allow and deny paths.
		{
			Inputs: 2,
			Body: seqStmt(1,
				seqStmt(2, checkStmt(20, markA), checkStmt(21, markB)),
				forwardStmt(22, "fwd", reqA, reqB),
			),
		},
		// 1: short-circuiting branch to reject.
		{
			Inputs: 3,
			Body: branchStmt(1, complexCond, rejectStmt(2, "blocked"),
				forwardStmt(3, "fallback", reqC)),
		},
		// 2: try/catch around a raising check, handler rejects.
		{
			Inputs: 3,
			Body: seqStmt(1,
				tryStmt(2, checkStmt(3, raising), rejectStmt(4, "caught")),
				forwardStmt(5, "afterTry", reqC),
			),
		},
		// 3: nested try, inner re-raise handled by outer.
		{
			Inputs: 3,
			Body: tryStmt(1,
				tryStmt(2, checkStmt(3, raising),
					seqStmt(4, checkStmt(5, raising), rejectStmt(6, "inner"))),
				rejectStmt(7, "outer"),
			),
		},
		// 4: repeated pure and effectful occurrences.
		{
			Inputs: 3,
			Body: seqStmt(1,
				seqStmt(2, checkStmt(3, a), checkStmt(4, a)),
				seqStmt(5, checkStmt(6, markAlways), checkStmt(7, markAlways)),
			),
		},
		// 5: untrusted mark cannot satisfy trusted requirement.
		{
			Inputs: 2,
			Body: seqStmt(1, checkStmt(2, markB), forwardStmt(3, "trustFwd",
				Mark{Field: "b", Name: "mB", Trusted: true})),
		},
		// 6: branch without else falls through to a forward.
		{
			Inputs: 1,
			Body: seqStmt(1,
				branchStmt(2, checkExpr(3, markA), checkStmt(4, markA), nil),
				forwardStmt(5, "tail", reqA),
			),
		},
		// 7: Not and Or short-circuit mix.
		{
			Inputs: 3,
			Body: branchStmt(1,
				orExpr(2, notExpr(3, checkExpr(4, a)), andExpr(5, checkExpr(6, b), checkExpr(7, c))),
				rejectStmt(8, "r"), nil),
		},
	}
	return rules
}
