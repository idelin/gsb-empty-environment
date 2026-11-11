package shortcircuit

import (
	"crypto/ed25519"
	"crypto/subtle"
	"errors"
	"fmt"
)

// Validation errors wrap these sentinels so callers can classify rejections.
var (
	// ErrTraceRejected means the trace does not faithfully describe a
	// short-circuit execution of the tree.
	ErrTraceRejected = errors.New("shortcircuit: trace rejected")
	// ErrBadSeal means the Ed25519 seal does not match the trace contents.
	ErrBadSeal = errors.New("shortcircuit: invalid trace seal")
)

// Validator replays traces without executing any check. Leaf outcomes are
// taken solely from the trace entries and checked against the tree traversal;
// therefore a missing step, an added unvisited branch, a reordered visit, a
// wrong value or a rule-version mismatch is rejected.
type Validator struct {
	verifyKey ed25519.PublicKey // optional; when set the trace must carry a valid seal
}

// ValidatorOption configures a Validator.
type ValidatorOption func(*Validator)

// WithVerificationKey requires every validated trace to carry an Ed25519 seal
// verifiable under key, defeating re-chained forgeries built after the fact.
func WithVerificationKey(key ed25519.PublicKey) ValidatorOption {
	return func(v *Validator) { v.verifyKey = key }
}

// NewValidator builds a Validator.
func NewValidator(opts ...ValidatorOption) *Validator {
	v := &Validator{}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// Validate independently replays trace against root. It never invokes a
// CheckFunc. A nil error means the trace proves exactly the executed steps.
func (v *Validator) Validate(root Node, trace *Trace) error {
	if err := validateTree(root); err != nil {
		return err
	}
	if err := preflight(trace, root); err != nil {
		return err
	}
	if err := verifyChains(trace); err != nil {
		return err
	}
	if v.verifyKey != nil {
		if len(trace.Seal) == 0 {
			return fmt.Errorf("%w: trace is not sealed", ErrBadSeal)
		}
		if !ed25519.Verify(v.verifyKey, trace.digest(), trace.Seal) {
			return fmt.Errorf("%w: signature mismatch for run %q", ErrBadSeal, trace.RunID)
		}
	}

	r := &replayer{trace: trace, occ: map[string]int{}}
	final, aborted, err := r.replay(root)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTraceRejected, err)
	}

	if r.cursor != len(trace.Entries) {
		return fmt.Errorf("%w: %d extra entr%s describing unvisited branches",
			ErrTraceRejected, len(trace.Entries)-r.cursor, plural(len(trace.Entries)-r.cursor))
	}
	if aborted {
		if trace.Abort == nil {
			return fmt.Errorf("%w: replay aborted but trace carries no abort marker", ErrTraceRejected)
		}
		return nil
	}
	if trace.Abort != nil {
		return fmt.Errorf("%w: trace carries abort marker but replay completed", ErrTraceRejected)
	}
	if final != trace.Final {
		return fmt.Errorf("%w: final value %v does not match replayed value %v",
			ErrTraceRejected, trace.Final, final)
	}
	return nil
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

func preflight(trace *Trace, root Node) error {
	if trace == nil {
		return fmt.Errorf("%w: nil trace", ErrTraceRejected)
	}
	if trace.RunID == "" {
		return fmt.Errorf("%w: missing run id", ErrTraceRejected)
	}
	if trace.RootID != root.nodeID() {
		return fmt.Errorf("%w: root id %q does not match tree root %q",
			ErrTraceRejected, trace.RootID, root.nodeID())
	}
	if len(trace.Entries) == 0 {
		return fmt.Errorf("%w: trace has no entries", ErrTraceRejected)
	}
	return nil
}

func verifyChains(trace *Trace) error {
	prev := genesisHash(trace.RunID, trace.RootID, trace.StartedAt)
	for i, e := range trace.Entries {
		if e.Seq != i+1 {
			return fmt.Errorf("%w: entry for node %q has sequence %d, expected %d",
				ErrTraceRejected, e.NodeID, e.Seq, i+1)
		}
		want := chainHash(prev, e)
		if subtle.ConstantTimeCompare([]byte(want), []byte(e.Chain)) != 1 {
			return fmt.Errorf("%w: broken hash chain at node %q (seq %d)",
				ErrTraceRejected, e.NodeID, e.Seq)
		}
		prev = e.Chain
	}
	return nil
}

type replayer struct {
	trace  *Trace
	cursor int
	occ    map[string]int
}

func (r *replayer) next() *Entry {
	if r.cursor >= len(r.trace.Entries) {
		return nil
	}
	return r.trace.Entries[r.cursor]
}

// replay models the engine post-order traversal. A leaf or an aborted node is
// represented by the head entry; a healthy composite node first replays its
// children and only then consumes its own completed entry.
func (r *replayer) replay(n Node) (value bool, aborted bool, err error) {
	id := n.nodeID()
	occ := r.occ[id] + 1
	r.occ[id] = occ

	switch t := n.(type) {
	case *CheckNode:
		return r.replayLeaf(t, occ)
	case *AndNode:
		return r.replayAnd(t, occ)
	case *OrNode:
		return r.replayOr(t, occ)
	case *NotNode:
		return r.replayNot(t, occ)
	default:
		return false, false, fmt.Errorf("unknown node type %T", n)
	}
}

// headAborts reports whether the head entry says this composite visit was
// canceled or errored before any child ran, and consumes it when so.
func (r *replayer) headAborts(id string, occ int, kind NodeKind) (bool, bool, error) {
	e := r.next()
	if e == nil {
		return false, false, nil
	}
	if e.NodeID != id || e.Occurrence != occ || e.Kind != kind {
		return false, false, nil // a child entry must come first
	}
	if e.Status != StatusError && e.Status != StatusCanceled {
		return false, false, nil
	}
	v, aborted, err := r.replayAbort(e)
	return v, aborted, err
}

func (r *replayer) expectHead(id string, occ int, kind NodeKind) (*Entry, error) {
	e := r.next()
	if e == nil {
		return nil, fmt.Errorf("missing entry for node %q (occurrence %d)", id, occ)
	}
	if e.NodeID != id || e.Occurrence != occ || e.Kind != kind {
		return nil, fmt.Errorf("entry mismatch at seq %d: got node %q/%d/%s, want %q/%d/%s",
			e.Seq, e.NodeID, e.Occurrence, e.Kind, id, occ, kind)
	}
	return e, nil
}

func (r *replayer) replayLeaf(n *CheckNode, occ int) (bool, bool, error) {
	e, err := r.expectHead(n.ID, occ, KindCheck)
	if err != nil {
		return false, false, err
	}
	if e.Status == StatusError || e.Status == StatusCanceled {
		return r.replayAbort(e)
	}
	return r.replayCheck(n, e)
}

func (r *replayer) replayAbort(e *Entry) (bool, bool, error) {
	ab := r.trace.Abort
	if ab == nil || ab.NodeID != e.NodeID || ab.Occurrence != e.Occurrence || ab.Status != e.Status {
		return false, false, fmt.Errorf("abort marker does not match final entry at node %q", e.NodeID)
	}
	if e.Evidence != nil {
		return false, false, fmt.Errorf("aborted node %q must not carry decision evidence", e.NodeID)
	}
	r.cursor++
	return false, true, nil
}

func (r *replayer) replayCheck(n *CheckNode, e *Entry) (bool, bool, error) {
	if e.RuleName != n.Rule.Name || e.RuleVersion != n.Rule.Version {
		return false, false, fmt.Errorf("rule version mismatch at node %q: trace has %s@%s, tree has %s@%s",
			n.ID, e.RuleName, e.RuleVersion, n.Rule.Name, n.Rule.Version)
	}
	if e.Evidence == nil {
		return false, false, fmt.Errorf("check node %q entry lacks evidence", n.ID)
	}
	switch e.Status {
	case StatusTrue:
		if !e.Value {
			return false, false, fmt.Errorf("node %q marked true but value is false", n.ID)
		}
	case StatusFalse:
		if e.Value {
			return false, false, fmt.Errorf("node %q marked false but value is true", n.ID)
		}
	default:
		return false, false, fmt.Errorf("check node %q has invalid status %q", n.ID, e.Status)
	}
	r.cursor++
	return e.Value, false, nil
}

func (r *replayer) replayAnd(n *AndNode, occ int) (bool, bool, error) {
	if _, aborted, err := r.headAborts(n.ID, occ, KindAnd); aborted || err != nil {
		return false, aborted, err
	}
	result := true
	for _, child := range n.Children {
		v, aborted, err := r.replay(child)
		if err != nil {
			return false, false, err
		}
		if aborted {
			return false, true, nil
		}
		if !v {
			result = false
			break // mirrors engine short-circuit
		}
	}
	e, err := r.expectHead(n.ID, occ, KindAnd)
	if err != nil {
		return false, false, err
	}
	if e.Status == StatusError || e.Status == StatusCanceled {
		return r.replayAbort(e)
	}
	return r.consumeComposite(n, e, result)
}

func (r *replayer) replayOr(n *OrNode, occ int) (bool, bool, error) {
	if _, aborted, err := r.headAborts(n.ID, occ, KindOr); aborted || err != nil {
		return false, aborted, err
	}
	result := false
	for _, child := range n.Children {
		v, aborted, err := r.replay(child)
		if err != nil {
			return false, false, err
		}
		if aborted {
			return false, true, nil
		}
		if v {
			result = true
			break // mirrors engine short-circuit
		}
	}
	e, err := r.expectHead(n.ID, occ, KindOr)
	if err != nil {
		return false, false, err
	}
	if e.Status == StatusError || e.Status == StatusCanceled {
		return r.replayAbort(e)
	}
	return r.consumeComposite(n, e, result)
}

func (r *replayer) replayNot(n *NotNode, occ int) (bool, bool, error) {
	if _, aborted, err := r.headAborts(n.ID, occ, KindNot); aborted || err != nil {
		return false, aborted, err
	}
	v, aborted, err := r.replay(n.Child)
	if err != nil {
		return false, false, err
	}
	if aborted {
		return false, true, nil
	}
	e, err := r.expectHead(n.ID, occ, KindNot)
	if err != nil {
		return false, false, err
	}
	if e.Status == StatusError || e.Status == StatusCanceled {
		return r.replayAbort(e)
	}
	return r.consumeComposite(n, e, !v)
}

func (r *replayer) consumeComposite(n Node, e *Entry, result bool) (bool, bool, error) {
	if e.Status != StatusCompleted || e.Value != result || e.RuleName != "" || e.RuleVersion != "" {
		return false, false, fmt.Errorf("composite entry for node %q inconsistent with replayed value %v",
			n.nodeID(), result)
	}
	r.cursor++
	return result, false, nil
}
