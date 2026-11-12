package netsec

import (
	"errors"
	"testing"
)

func TestEmptyBuildAndOrdering(t *testing.T) {
	b := NewBuilder()
	env := mustBuild(t, b)
	if len(env.OrderedNames()) != 0 || len(env.Env()) != 0 {
		t.Fatal("empty builder must produce an empty environment")
	}
	if env.Stats() != (Stats{}) {
		t.Fatalf("empty build stats = %+v, want zero", env.Stats())
	}

	mustDefine(t, b, Spec{Name: "ZED", Template: "z"})
	mustDefine(t, b, Spec{Name: "ALPHA", Template: "a"})
	if err := b.Import([]string{"MID=m"}, []string{"MID"}); err != nil {
		t.Fatal(err)
	}
	env = mustBuild(t, b)
	wantOrder := []string{"ZED", "ALPHA", "MID"}
	if got := env.OrderedNames(); len(got) != len(wantOrder) {
		t.Fatalf("order = %v, want %v", got, wantOrder)
	} else {
		for i := range wantOrder {
			if got[i] != wantOrder[i] {
				t.Fatalf("order = %v, want %v", got, wantOrder)
			}
		}
	}
	if _, ok := env.Get("ABSENT"); ok {
		t.Fatal("absent variable must not be reported")
	}
}

func TestOriginAtPanics(t *testing.T) {
	value := Value{Name: "V", Data: "ab", Runs: []Run{
		{Origin: Origin{Source: Trusted, Name: "V"}, Data: "ab"},
	}}
	if value.OriginAt(1) != (Origin{Source: Trusted, Name: "V"}) {
		t.Fatal("OriginAt returned the wrong origin")
	}
	for _, bad := range []int{-1, 2} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("OriginAt(%d) must panic", bad)
				}
			}()
			value.OriginAt(bad)
		}()
	}
}

func TestImportNameValidation(t *testing.T) {
	b := NewBuilder()
	err := b.Import([]string{"1BAD=x"}, []string{"1BAD"})
	var importErr *ImportError
	if err == nil {
		t.Fatal("invalid allowlist name must be rejected")
	}
	if !errors.As(err, &importErr) {
		t.Fatalf("want *ImportError, got %v", err)
	}

	// Absent allowlisted names are skipped without error.
	if err := b.Import(nil, []string{"PATH"}); err != nil {
		t.Fatalf("absent variable must be skipped, got %v", err)
	}
}

func TestDeepChainWithinBudget(t *testing.T) {
	b := NewBuilder()
	if err := b.SetBudget(Budget{MaxOutputBytes: 1 << 16, MaxDepth: 4}); err != nil {
		t.Fatal(err)
	}
	mustDefine(t, b, Spec{Name: "L0", Template: "${L1}"})
	mustDefine(t, b, Spec{Name: "L1", Template: "${L2}"})
	mustDefine(t, b, Spec{Name: "L2", Template: "${L3}"})
	mustDefine(t, b, Spec{Name: "L3", Template: "deep"})

	env := mustBuild(t, b)
	root, _ := env.Get("L0")
	if root.Data != "deep" {
		t.Fatalf("L0 = %q, want deep", root.Data)
	}
	if env.Stats().MaxDepthObserved != 4 {
		t.Fatalf("MaxDepthObserved = %d, want 4", env.Stats().MaxDepthObserved)
	}
}

func TestErrorMessagesAreStable(t *testing.T) {
	cases := []error{
		&SyntaxError{Name: "X", Position: 3, Template: "a$b"},
		&NameError{Name: "1BAD"},
		&DuplicateError{Name: "X"},
		&ImportError{Entry: "BAD"},
		&UndefinedError{Ref: "Y", In: "X"},
		&CycleError{Path: []string{"A", "B", "A"}},
		&SensitiveError{Name: "SECRET"},
		&BudgetExceededError{Kind: "depth", Limit: 2, Name: "A"},
		&BudgetError{Reason: "no"},
	}
	for _, err := range cases {
		if err.Error() == "" {
			t.Fatalf("%T produced an empty error message", err)
		}
	}
	if Untrusted.String() != "untrusted" || Trusted.String() != "trusted" {
		t.Fatal("unexpected Source string form")
	}
}
