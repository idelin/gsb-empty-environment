package budget_test

import (
	"errors"
	"testing"

	"example.org/netsec/q0040/budget"
)

func TestFacadeRejectsAliasBomb(t *testing.T) {
	data := []byte("a: &a [x, x]\nb: &b [*a, *a]\nc: &c [*b, *b]\nroot: *c\n")
	root, err := budget.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	err = budget.Check(root, budget.Limits{MaxNodes: 20, MaxDepth: 100, MaxMergeDepth: 10})
	if !errors.Is(err, budget.ErrNodesExceeded) {
		t.Fatalf("expected node rejection, got %v", err)
	}
}

func TestFacadeAllowsSharedConfig(t *testing.T) {
	data := []byte("base: &base {image: app}\njob:\n  <<: *base\n")
	root, stats, err := budget.Preflight(data, budget.Limits{MaxNodes: 100, MaxDepth: 10, MaxMergeDepth: 2})
	if err != nil {
		t.Fatalf("Preflight: %v (stats=%v)", err, stats)
	}
	if root == nil {
		t.Fatalf("Preflight returned no tree")
	}
}
