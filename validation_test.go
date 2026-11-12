package taskpkg

import (
	"testing"
)

func TestValidationFailures(t *testing.T) {
	cases := []struct {
		name string
		rule *Rule
	}{
		{"negative inputs", &Rule{Inputs: -1, Body: rejectStmt(1, "x")}},
		{"too many inputs", &Rule{Inputs: MaxInputs + 1, Body: rejectStmt(1, "x")}},
		{"duplicate node id", &Rule{
			Inputs: 1,
			Body:   seqStmt(1, checkStmt(1, pureCheck(0, "a")), rejectStmt(2, "x")),
		}},
		{"input out of range", &Rule{Inputs: 1, Body: checkStmt(1, pureCheck(5, "a"))}},
		{"inconsistent check redeclaration", &Rule{
			Inputs: 1,
			Body: seqStmt(1,
				checkStmt(2, pureCheck(0, "a")),
				checkStmt(3, leafCheck(0, "a", "m", true, EmitAlways))),
		}},
		{"mark name without emit", &Rule{
			Inputs: 1,
			Body:   checkStmt(1, &Check{ID: 0, Field: "a", MarkName: "m"}),
		}},
		{"raise and mark together", &Rule{
			Inputs: 1,
			Body: checkStmt(1, &Check{
				ID: 0, Field: "a", Emit: EmitAlways, MarkName: "m", RaiseOn: boolPtr(true),
			}),
		}},
		{"empty reject reason", &Rule{Inputs: 0, Body: &Stmt{ID: 1, Kind: StmtReject}}},
		{"branch missing then", &Rule{
			Inputs: 1,
			Body:   &Stmt{ID: 1, Kind: StmtBranch, Cond: checkExpr(2, pureCheck(0, "a"))},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.rule.Validate(); err == nil {
				t.Fatal("expected validation error")
			} else if _, ok := err.(*ruleError); !ok {
				t.Fatalf("want *ruleError, got %T", err)
			}
		})
	}
}

func TestEmptyRulePasses(t *testing.T) {
	prog, err := Compile(&Rule{Inputs: 0})
	if err != nil {
		t.Fatal(err)
	}
	if prog.NumOps() != 1 {
		t.Fatalf("empty rule should only contain end op, got %d", prog.NumOps())
	}
	tr, err := prog.Evaluate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if tr.Decision != DecisionPassed {
		t.Fatalf("empty rule must pass, got %s", tr.Decision)
	}
}

func TestInputArityErrors(t *testing.T) {
	rule := &Rule{Inputs: 2, Body: checkStmt(1, pureCheck(0, "a"))}
	prog := mustCompile(t, rule)
	if _, err := prog.Evaluate([]bool{true}); err == nil {
		t.Fatal("expected arity error")
	}
	if _, err := Interpret(rule, []bool{true}); err == nil {
		t.Fatal("expected interpreter arity error")
	}
}

func TestAccessorsAndStrings(t *testing.T) {
	rule := &Rule{Inputs: 1, Body: rejectStmt(1, "boom")}
	prog := mustCompile(t, rule)
	if prog.Rule() != rule {
		t.Fatal("Rule() accessor mismatch")
	}
	if EdgeReject.String() != "reject" || DecisionRejected.String() != "rejected" {
		t.Fatal("string helpers mismatch")
	}
	if err := (&ruleError{msg: "x"}).Error(); err != "x" {
		t.Fatal("ruleError.Error mismatch")
	}
}
