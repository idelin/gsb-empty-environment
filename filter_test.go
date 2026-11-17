package netsec

import (
	"errors"
	"math"
	"testing"
)

func TestFilterFirstMatchingRuleAndDefault(t *testing.T) {
	filter := Filter{
		Rules: []Rule{
			DenyRule(1, 13, ArgEquals(0, 7)),
			AllowRule(1),
			TerminateRule(2, 9),
		},
		Default:     ActionNotify,
		DefaultData: 5,
	}

	var args [6]uint64
	args[0] = 7
	if got := filter.Evaluate(1, args); got != (Decision{Action: ActionDeny, Data: 13}) {
		t.Fatalf("specific first rule: got %s", got)
	}

	args[0] = 8
	if got := filter.Evaluate(1, args); got != (Decision{Action: ActionAllow, Data: 0}) {
		t.Fatalf("second first-match rule: got %s", got)
	}

	if got := filter.Evaluate(2, args); got != (Decision{Action: ActionTerminate, Data: 9}) {
		t.Fatalf("terminate rule: got %s", got)
	}

	if got := filter.Evaluate(3, args); got != (Decision{Action: ActionNotify, Data: 5}) {
		t.Fatalf("default: got %s", got)
	}
}

func TestArgumentPredicates(t *testing.T) {
	const high = uint64(1) << 40
	tests := []struct {
		name string
		pred ArgPredicate
		arg  uint64
		want bool
	}{
		{"equal", ArgPredicate{Index: 0, Op: ArgEqual, Value: 9}, 9, true},
		{"not-equal", ArgPredicate{Index: 0, Op: ArgNotEqual, Value: 9}, 8, true},
		{"less", ArgPredicate{Index: 0, Op: ArgLessThan, Value: 9}, 8, true},
		{"less-equal", ArgPredicate{Index: 0, Op: ArgLessEqual, Value: 9}, 9, true},
		{"greater", ArgPredicate{Index: 0, Op: ArgGreaterThan, Value: 9}, 10, true},
		{"greater-equal", ArgPredicate{Index: 0, Op: ArgGreaterEqual, Value: 9}, 9, true},
		{"bits-set", ArgPredicate{Index: 0, Op: ArgBitsSet, Mask: 0b1010}, 0b1110, true},
		{"bits-clear", ArgPredicate{Index: 0, Op: ArgBitsClear, Mask: 0b0100}, 0b1011, true},
		{"masked-equal", ArgPredicate{Index: 0, Op: ArgMaskedEqual, Mask: 0xff00, Value: 0x1200}, 0x1234, true},
		{"masked-equal-high", ArgPredicate{Index: 0, Op: ArgMaskedEqual, Mask: high, Value: high}, high | 7, true},
	}
	for _, tt := range tests {
		if got := tt.pred.Match(tt.arg); got != tt.want {
			t.Errorf("%s: Match(%d) = %v, want %v", tt.name, tt.arg, got, tt.want)
		}
	}

	badRule := Rule{Syscall: 1, Args: []ArgPredicate{{Index: 6, Op: ArgEqual, Value: 1}}}
	if badRule.Matches(1, [6]uint64{1, 0, 0, 0, 0, 0}) {
		t.Fatal("rule with an out-of-range argument index matched")
	}
}

func TestFilterValidation(t *testing.T) {
	if err := (Filter{Default: Action(42)}).Validate(); !errors.Is(err, ErrInvalidFilter) || !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("invalid default: %v", err)
	}
	if err := (Filter{Rules: []Rule{{Syscall: 1, Action: ActionAllow, Args: []ArgPredicate{{Index: 6}}}}}).Validate(); !errors.Is(err, ErrInvalidFilter) || !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("invalid arg index: %v", err)
	}
	if err := (Filter{Rules: []Rule{{Syscall: 1, Action: ActionAllow, Args: []ArgPredicate{{Index: 0, Op: ArgBitsSet, Mask: 0}}}}}).Validate(); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("zero mask: %v", err)
	}
	if err := (Filter{Rules: []Rule{{Syscall: 1, Action: Action(99)}}}).Validate(); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("invalid rule action: %v", err)
	}
}

func TestFilterIsDeepCopiedByChain(t *testing.T) {
	preds := []ArgPredicate{{Index: 0, Op: ArgEqual, Value: 7}}
	filter := Filter{
		Rules: []Rule{{Syscall: 1, Args: preds, Action: ActionDeny, Data: 13}},
	}
	chain, err := NewChain(filter)
	if err != nil {
		t.Fatal(err)
	}

	preds[0].Value = 8
	filter.Rules[0].Action = ActionAllow
	filter.Rules[0].Data = 0

	var args [6]uint64
	args[0] = 7
	if got := chain.Evaluate(1, args); got != (Decision{Action: ActionDeny, Data: 13}) {
		t.Fatalf("caller mutation changed installed chain: %s", got)
	}

	exported := chain.Filters()
	exported[0].Rules[0].Action = ActionAllow
	exported[0].Rules[0].Args[0].Value = 8
	if got := chain.Evaluate(1, args); got != (Decision{Action: ActionDeny, Data: 13}) {
		t.Fatalf("Filters return aliased chain storage: %s", got)
	}
}

func TestZeroValueFilterAndChainAreAllowAll(t *testing.T) {
	var filter Filter
	var chain *Chain
	var args [6]uint64
	args[0] = math.MaxUint64
	want := Decision{Action: ActionAllow, Data: 0}
	if got := filter.Evaluate(123, args); got != want {
		t.Fatalf("zero filter: %s", got)
	}
	if got := chain.Evaluate(123, args); got != want {
		t.Fatalf("nil chain: %s", got)
	}
}
