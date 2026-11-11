package shortcircuit

import (
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
)

func TestCancellationMidEvaluation(t *testing.T) {
	a := &recordingCheck{t: t, id: "a", rule: rule("ra", "1"), decision: true}
	b := &recordingCheck{t: t, id: "b", rule: rule("rb", "1"), decision: true}
	c := &recordingCheck{t: t, id: "c", rule: rule("rc", "1"), decision: true}
	root := &AndNode{ID: "and", Children: []Node{a.node(), b.node(), c.node()}}

	ctx, cancel := context.WithCancel(context.Background())
	bNode := root.Children[1].(*CheckNode)
	original := bNode.Fn
	bNode.Fn = func(ctx context.Context) (bool, Evidence, error) {
		cancel() // cancel after b starts, before c is reached
		return original(ctx)
	}

	_, trace, err := NewEngine().Evaluate(ctx, "run-cancel-mid", root)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if c.calls != 0 {
		t.Fatal("check after cancellation must not run")
	}
	if !equalIDs(entryIDs(trace), []string{"a", "b", "c"}) {
		t.Fatalf("cancel should be recorded at the c entry: %v", entryIDs(trace))
	}
	if last := trace.Entries[2]; last.Status != StatusCanceled {
		t.Fatalf("c entry must be canceled, got %s", last.Status)
	}
	if err := NewValidator().Validate(root, trace); err != nil {
		t.Fatalf("genuine mid-run cancel trace rejected: %v", err)
	}
}

func TestSharedNodeVisitsBoundSeparately(t *testing.T) {
	sharedCheck := &recordingCheck{t: t, id: "shared", rule: rule("rs", "1"), decision: true}
	other := &recordingCheck{t: t, id: "other", rule: rule("ro", "1"), decision: false}
	// AND(shared, OR(shared, other)): shared runs twice; the second true
	// short-circuits the OR so other never runs.
	root := &AndNode{
		ID: "and",
		Children: []Node{
			sharedCheck.node(),
			&OrNode{ID: "or", Children: []Node{sharedCheck.node(), other.node()}},
		},
	}

	decision, trace, err := NewEngine().Evaluate(context.Background(), "run-shared", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision {
		t.Fatal("expression should be true")
	}
	if sharedCheck.calls != 2 || other.calls != 0 {
		t.Fatalf("shared should run twice, other zero times: %d/%d", sharedCheck.calls, other.calls)
	}
	if !equalIDs(entryIDs(trace), []string{"shared", "shared", "or", "and"}) {
		t.Fatalf("unexpected entries: %v", entryIDs(trace))
	}
	occ := []int{}
	for _, e := range trace.Entries {
		if e.NodeID == "shared" {
			occ = append(occ, e.Occurrence)
		}
	}
	if len(occ) != 2 || occ[0] != 1 || occ[1] != 2 {
		t.Fatalf("shared occurrences must be 1,2: %v", occ)
	}
	if err := NewValidator().Validate(root, trace); err != nil {
		t.Fatalf("genuine shared-node trace rejected: %v", err)
	}
}

func TestRuleVersionAndEvidenceBound(t *testing.T) {
	a := &recordingCheck{t: t, id: "a", rule: rule("authz", "2026-11-01"), decision: true}
	root := a.node()

	_, trace, err := NewEngine().Evaluate(context.Background(), "run-binding", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	e := trace.Entries[0]
	if e.RuleName != "authz" || e.RuleVersion != "2026-11-01" {
		t.Fatalf("rule version not bound: %s@%s", e.RuleName, e.RuleVersion)
	}
	if e.Evidence == nil || e.Evidence.Summary != "ev-a" {
		t.Fatalf("evidence not bound: %+v", e.Evidence)
	}
	if e.Seq != 1 || e.Chain == "" {
		t.Fatal("entries must be ordered and hash-chained")
	}
}

func TestSealedTraceAndForgery(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	a := &recordingCheck{t: t, id: "a", rule: rule("ra", "1"), decision: true}
	b := &recordingCheck{t: t, id: "b", rule: rule("rb", "1"), decision: false}
	root := &AndNode{ID: "and", Children: []Node{a.node(), b.node()}}

	_, trace, err := NewEngine(WithSigningKey(priv)).Evaluate(context.Background(), "run-sealed", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	validator := NewValidator(WithVerificationKey(pub))
	if err := validator.Validate(root, trace); err != nil {
		t.Fatalf("sealed genuine trace rejected: %v", err)
	}

	t.Run("mutated evidence breaks chain", func(t *testing.T) {
		forged := cloneTrace(trace)
		forged.Entries[0].Evidence = &Evidence{Summary: "forged", Data: "never happened"}
		rechain(forged)
		if err := validator.Validate(root, forged); !errors.Is(err, ErrBadSeal) {
			t.Fatalf("re-chained evidence forgery must fail seal check, got %v", err)
		}
	})

	t.Run("added unexecuted branch rejected", func(t *testing.T) {
		forged := cloneTrace(trace)
		fake := fakeCheckEntry("c", rule("rc", "1"), true)
		forged.Entries = append(forged.Entries[:1], append([]*Entry{fake}, forged.Entries[1:]...)...)
		rechain(forged)
		err := validator.Validate(root, forged)
		if !errors.Is(err, ErrTraceRejected) && !errors.Is(err, ErrBadSeal) {
			t.Fatalf("added branch must be rejected, got %v", err)
		}
	})

	t.Run("missing short-circuit step rejected", func(t *testing.T) {
		forged := cloneTrace(trace)
		forged.Entries = append(forged.Entries[:1], forged.Entries[2:]...) // drop b
		rechain(forged)
		err := validator.Validate(root, forged)
		if !errors.Is(err, ErrTraceRejected) && !errors.Is(err, ErrBadSeal) {
			t.Fatalf("missing step must be rejected, got %v", err)
		}
	})

	t.Run("value flip rejected", func(t *testing.T) {
		forged := cloneTrace(trace)
		forged.Entries[1].Status = StatusTrue
		forged.Entries[1].Value = true
		rechain(forged)
		err := validator.Validate(root, forged)
		if !errors.Is(err, ErrTraceRejected) && !errors.Is(err, ErrBadSeal) {
			t.Fatalf("value flip must be rejected, got %v", err)
		}
	})

	t.Run("unsealed trace rejected by verifying validator", func(t *testing.T) {
		unsigned := cloneTrace(trace)
		unsigned.Seal = nil
		if err := validator.Validate(root, unsigned); !errors.Is(err, ErrBadSeal) {
			t.Fatalf("missing seal must be rejected, got %v", err)
		}
	})
}

func TestRuleVersionMismatchRejected(t *testing.T) {
	a := &recordingCheck{t: t, id: "a", rule: rule("ra", "1"), decision: true}
	root := a.node()

	_, trace, err := NewEngine().Evaluate(context.Background(), "run-version", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	changed := root
	changed.Rule = rule("ra", "2") // tree now claims a newer rule
	forged := cloneTrace(trace)
	rechain(forged)
	err = NewValidator().Validate(changed, forged)
	if !errors.Is(err, ErrTraceRejected) {
		t.Fatalf("stale rule-version trace must be rejected, got %v", err)
	}
}

func TestSharedNodeForgedExtraVisitRejected(t *testing.T) {
	sharedCheck := &recordingCheck{t: t, id: "shared", rule: rule("rs", "1"), decision: true}
	root := &AndNode{
		ID:       "and",
		Children: []Node{sharedCheck.node(), sharedCheck.node()},
	}
	_, trace, err := NewEngine().Evaluate(context.Background(), "run-shared-forge", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	forged := cloneTrace(trace)
	fake := fakeCheckEntry("shared", rule("rs", "1"), true)
	fake.Occurrence = 3
	forged.Entries = append(forged.Entries[:2], append([]*Entry{fake}, forged.Entries[2:]...)...)
	rechain(forged)

	err = NewValidator().Validate(root, forged)
	if !errors.Is(err, ErrTraceRejected) {
		t.Fatalf("forged third visit of shared node must be rejected, got %v", err)
	}
}

func TestReorderedEntriesRejected(t *testing.T) {
	a := &recordingCheck{t: t, id: "a", rule: rule("ra", "1"), decision: true}
	b := &recordingCheck{t: t, id: "b", rule: rule("rb", "1"), decision: false}
	root := &AndNode{ID: "and", Children: []Node{a.node(), b.node()}}

	_, trace, err := NewEngine().Evaluate(context.Background(), "run-reorder", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Swap the two leaf entries and rebuild the hash chain: a structurally
	// independent replay (no signing key involved) must still reject it.
	forged := cloneTrace(trace)
	forged.Entries[0], forged.Entries[1] = forged.Entries[1], forged.Entries[0]
	rechain(forged)

	err = NewValidator().Validate(root, forged)
	if !errors.Is(err, ErrTraceRejected) {
		t.Fatalf("reordered trace must be rejected by structural replay, got %v", err)
	}
}

func TestForgedEvidenceRejectedWithoutSeal(t *testing.T) {
	// Even without Ed25519 sealing, a fully fabricated branch cannot be made
	// consistent with the actual short-circuit traversal.
	a := &recordingCheck{t: t, id: "a", rule: rule("ra", "1"), decision: true}
	b := &recordingCheck{t: t, id: "b", rule: rule("rb", "1"), decision: false}
	c := &recordingCheck{t: t, id: "c", rule: rule("rc", "1"), decision: true}
	root := &AndNode{ID: "and", Children: []Node{a.node(), b.node(), c.node()}}

	_, trace, err := NewEngine().Evaluate(context.Background(), "run-unsigned-forge", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.calls != 0 {
		t.Fatal("c must be short-circuited and never execute")
	}

	forged := cloneTrace(trace)
	cNode := root.Children[2].(*CheckNode)
	fake := fakeCheckEntry("c", cNode.Rule, true)
	// Insert a fabricated c visit between b and the AND completion entry.
	forged.Entries = append(forged.Entries[:2], append([]*Entry{fake}, forged.Entries[2:]...)...)
	rechain(forged)

	err = NewValidator().Validate(root, forged)
	if !errors.Is(err, ErrTraceRejected) {
		t.Fatalf("fabricated unexecuted branch must be rejected without a seal, got %v", err)
	}
}

func TestReplayDoesNotInvokeChecks(t *testing.T) {
	a := &recordingCheck{t: t, id: "a", rule: rule("ra", "1"), decision: true}
	b := &recordingCheck{t: t, id: "b", rule: rule("rb", "1"), decision: false}
	root := &AndNode{ID: "and", Children: []Node{a.node(), b.node()}}

	_, trace, err := NewEngine().Evaluate(context.Background(), "run-replay", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	callsBefore := a.calls + b.calls

	// Poison every check: any invocation during replay would panic.
	var poisoned []*CheckNode
	var walk func(Node)
	walk = func(n Node) {
		switch t := n.(type) {
		case *CheckNode:
			poisoned = append(poisoned, t)
		case *AndNode:
			for _, c := range t.Children {
				walk(c)
			}
		case *OrNode:
			for _, c := range t.Children {
				walk(c)
			}
		case *NotNode:
			walk(t.Child)
		}
	}
	walk(root)
	for _, n := range poisoned {
		n.Fn = func(context.Context) (bool, Evidence, error) {
			t.Fatal("validator must never execute a check")
			return false, Evidence{}, nil
		}
	}

	if err := NewValidator().Validate(root, trace); err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if got := a.calls + b.calls; got != callsBefore {
		t.Fatalf("replay invoked checks: before=%d after=%d", callsBefore, got)
	}
}

func TestInvalidTreesRejected(t *testing.T) {
	dupA := &CheckNode{ID: "dup", Rule: rule("r", "1"), Fn: func(context.Context) (bool, Evidence, error) { return true, Evidence{}, nil }}
	dupB := &CheckNode{ID: "dup", Rule: rule("r", "1"), Fn: func(context.Context) (bool, Evidence, error) { return true, Evidence{}, nil }}
	cases := []Node{
		nil,
		&CheckNode{ID: "", Rule: rule("r", "1"), Fn: func(context.Context) (bool, Evidence, error) { return true, Evidence{}, nil }},
		&CheckNode{ID: "x", Rule: rule("r", ""), Fn: func(context.Context) (bool, Evidence, error) { return true, Evidence{}, nil }},
		&AndNode{ID: "and"},
		&NotNode{ID: "not"},
		&AndNode{ID: "and-dup", Children: []Node{dupA, dupB}},
	}
	for i, root := range cases {
		if _, _, err := NewEngine().Evaluate(context.Background(), "run-bad", root); !errors.Is(err, ErrInvalidTree) {
			t.Fatalf("case %d: expected ErrInvalidTree, got %v", i, err)
		}
	}
}

func TestRunIDRequired(t *testing.T) {
	a := &recordingCheck{t: t, id: "a", rule: rule("ra", "1"), decision: true}
	if _, _, err := NewEngine().Evaluate(context.Background(), "", a.node()); !errors.Is(err, ErrEmptyRunID) {
		t.Fatalf("expected ErrEmptyRunID, got %v", err)
	}
}

// cloneTrace deep-copies entries so a forgery never mutates the original.
func cloneTrace(t *Trace) *Trace {
	cp := *t
	cp.Entries = make([]*Entry, len(t.Entries))
	for i, e := range t.Entries {
		entry := *e
		if e.Evidence != nil {
			ev := *e.Evidence
			entry.Evidence = &ev
		}
		cp.Entries[i] = &entry
	}
	if t.Abort != nil {
		ab := *t.Abort
		cp.Abort = &ab
	}
	if t.Seal != nil {
		cp.Seal = append([]byte(nil), t.Seal...)
	}
	return &cp
}

// rechain recomputes sequence numbers and hash links after a forgery, showing
// that structural replay and the Ed25519 seal still catch the tampering.
func rechain(t *Trace) {
	prev := genesisHash(t.RunID, t.RootID, t.StartedAt)
	for i, e := range t.Entries {
		e.Seq = i + 1
		e.Chain = chainHash(prev, e)
		prev = e.Chain
	}
	t.Seal = nil
}

func fakeCheckEntry(id string, rv RuleVersion, value bool) *Entry {
	status := StatusFalse
	if value {
		status = StatusTrue
	}
	ev := Evidence{Summary: "fabricated", Data: "no such call"}
	return &Entry{
		NodeID:      id,
		Occurrence:  1,
		Kind:        KindCheck,
		RuleName:    rv.Name,
		RuleVersion: rv.Version,
		Status:      status,
		Value:       value,
		Evidence:    &ev,
	}
}
