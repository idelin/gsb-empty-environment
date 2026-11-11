package shortcircuit

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrEmptyRunID is returned when Evaluate is called without a run id.
	ErrEmptyRunID = errors.New("shortcircuit: run id is required")
	// ErrInvalidTree is returned when the rule tree is malformed.
	ErrInvalidTree = errors.New("shortcircuit: invalid rule tree")
)

// AbortError is returned by Evaluate when a check errors, panics or the
// context is canceled. The trace still records exactly what was executed.
type AbortError struct {
	NodeID     string
	Occurrence int
	Status     Status
	Err        error
}

func (a *AbortError) Error() string {
	return fmt.Sprintf("shortcircuit: evaluation aborted at node %q (occurrence %d, %s): %v",
		a.NodeID, a.Occurrence, a.Status, a.Err)
}

// Unwrap exposes the underlying cause so errors.Is works against
// context.Canceled, context.DeadlineExceeded or the original check error.
func (a *AbortError) Unwrap() error { return a.Err }

// Entry records one node visit. Composite nodes are recorded after their
// children (post-order); an aborted node is the final entry and has no
// completed ancestor entries, because the ancestors never finished.
type Entry struct {
	Seq         int       // 1-based position in the execution order
	NodeID      string    // bound node
	Occurrence  int       // 1-based visit counter for shared nodes
	Kind        NodeKind  // check / and / or / not
	RuleName    string    // rule version binding, empty for composite nodes
	RuleVersion string    // rule version binding, empty for composite nodes
	Status      Status    // true/false for checks, completed for composites, error/cancel on abort
	Value       bool      // returned value (meaningful when Status is true/false/completed)
	Evidence    *Evidence // evidence summary, nil for composites and aborts
	Reason      string    // abort reason text
	Chain       string    // hash-chain link over this entry and the previous link
}

// Abort pinpoints where and why evaluation stopped.
type Abort struct {
	NodeID     string
	Occurrence int
	Status     Status
	Reason     string
}

// Trace is the auditable record of one Evaluate call.
type Trace struct {
	RunID     string
	RootID    string
	StartedAt time.Time
	Final     bool // decision of a completed run; always false on abort
	Abort     *Abort
	Entries   []*Entry
	Seal      []byte // Ed25519 signature over the trace digest, nil when unsigned
}

func (t *Trace) digest() []byte {
	last := ""
	if n := len(t.Entries); n > 0 {
		last = t.Entries[n-1].Chain
	}
	body := strings.Join([]string{
		"seal",
		t.RunID,
		t.RootID,
		strconv.FormatInt(t.StartedAt.UnixNano(), 10),
		strconv.FormatBool(t.Final),
		last,
	}, "\x1f")
	h := sha256.Sum256([]byte(body))
	return h[:]
}

// Engine evaluates rule trees and produces traces.
type Engine struct {
	signKey ed25519.PrivateKey
	now     func() time.Time
}

// Option configures an Engine.
type Option func(*Engine)

// WithSigningKey enables Ed25519 sealing of every produced trace.
func WithSigningKey(key ed25519.PrivateKey) Option {
	return func(e *Engine) { e.signKey = key }
}

// NewEngine builds an Engine.
func NewEngine(opts ...Option) *Engine {
	e := &Engine{now: time.Now}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Evaluate runs root with short-circuit semantics. On success it returns the
// decision and a complete trace. On check error, panic or cancellation it
// returns the false decision, the trace up to and including the aborting
// node, and an *AbortError.
func (e *Engine) Evaluate(ctx context.Context, runID string, root Node) (bool, *Trace, error) {
	if runID == "" {
		return false, nil, ErrEmptyRunID
	}
	if err := validateTree(root); err != nil {
		return false, nil, err
	}

	st := &evalState{
		engine: e,
		ctx:    ctx,
		trace: &Trace{
			RunID:     runID,
			RootID:    root.nodeID(),
			StartedAt: e.now().UTC(),
		},
		occ: map[string]int{},
	}
	st.prev = st.genesis()

	final, aborted := st.eval(root)
	st.trace.Final = final

	if e.signKey != nil {
		st.trace.Seal = ed25519.Sign(e.signKey, st.trace.digest())
	}

	if aborted {
		ab := st.trace.Abort
		return false, st.trace, &AbortError{
			NodeID:     ab.NodeID,
			Occurrence: ab.Occurrence,
			Status:     ab.Status,
			Err:        st.abortErr,
		}
	}
	return final, st.trace, nil
}

type evalState struct {
	engine   *Engine
	ctx      context.Context
	trace    *Trace
	occ      map[string]int
	prev     string
	abortErr error
}

func (s *evalState) genesis() string {
	return genesisHash(s.trace.RunID, s.trace.RootID, s.trace.StartedAt)
}

func genesisHash(runID, rootID string, startedAt time.Time) string {
	body := strings.Join([]string{
		"genesis",
		runID,
		rootID,
		strconv.FormatInt(startedAt.UnixNano(), 10),
	}, "\x1f")
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:])
}

func (s *evalState) eval(n Node) (value bool, aborted bool) {
	id := n.nodeID()
	occ := s.occ[id] + 1
	s.occ[id] = occ

	if err := s.ctx.Err(); err != nil {
		return false, s.recordAbort(n, occ, StatusCanceled, err, err.Error())
	}

	switch t := n.(type) {
	case *CheckNode:
		return s.evalCheck(t, occ)
	case *AndNode:
		return s.evalAnd(t, occ)
	case *OrNode:
		return s.evalOr(t, occ)
	case *NotNode:
		return s.evalNot(t, occ)
	default:
		err := fmt.Errorf("%w: unknown node type %T", ErrInvalidTree, n)
		return false, s.recordAbort(n, occ, StatusError, err, err.Error())
	}
}

func (s *evalState) evalCheck(n *CheckNode, occ int) (bool, bool) {
	var (
		decision bool
		ev       Evidence
		checkErr error
	)
	func() {
		defer func() {
			if r := recover(); r != nil {
				checkErr = fmt.Errorf("check panicked: %v", r)
			}
		}()
		decision, ev, checkErr = n.Fn(s.ctx)
	}()

	if checkErr != nil {
		status := StatusError
		if errors.Is(checkErr, context.Canceled) || errors.Is(checkErr, context.DeadlineExceeded) {
			status = StatusCanceled
		}
		return false, s.recordAbort(n, occ, status, checkErr, checkErr.Error())
	}

	status := StatusFalse
	if decision {
		status = StatusTrue
	}
	evidence := ev
	s.append(&Entry{
		NodeID:      n.ID,
		Occurrence:  occ,
		Kind:        KindCheck,
		RuleName:    n.Rule.Name,
		RuleVersion: n.Rule.Version,
		Status:      status,
		Value:       decision,
		Evidence:    &evidence,
	})
	return decision, false
}

func (s *evalState) evalAnd(n *AndNode, occ int) (bool, bool) {
	result := true
	for _, child := range n.Children {
		v, aborted := s.eval(child)
		if aborted {
			return false, true
		}
		if !v {
			result = false
			break
		}
	}
	s.append(&Entry{NodeID: n.ID, Occurrence: occ, Kind: KindAnd, Status: StatusCompleted, Value: result})
	return result, false
}

func (s *evalState) evalOr(n *OrNode, occ int) (bool, bool) {
	result := false
	for _, child := range n.Children {
		v, aborted := s.eval(child)
		if aborted {
			return false, true
		}
		if v {
			result = true
			break
		}
	}
	s.append(&Entry{NodeID: n.ID, Occurrence: occ, Kind: KindOr, Status: StatusCompleted, Value: result})
	return result, false
}

func (s *evalState) evalNot(n *NotNode, occ int) (bool, bool) {
	v, aborted := s.eval(n.Child)
	if aborted {
		return false, true
	}
	result := !v
	s.append(&Entry{NodeID: n.ID, Occurrence: occ, Kind: KindNot, Status: StatusCompleted, Value: result})
	return result, false
}

func (s *evalState) recordAbort(n Node, occ int, status Status, cause error, reason string) bool {
	s.append(&Entry{
		NodeID:     n.nodeID(),
		Occurrence: occ,
		Kind:       kindOf(n),
		Status:     status,
		Reason:     reason,
	})
	s.trace.Abort = &Abort{
		NodeID:     n.nodeID(),
		Occurrence: occ,
		Status:     status,
		Reason:     reason,
	}
	s.abortErr = cause
	return true
}

func (s *evalState) append(e *Entry) {
	e.Seq = len(s.trace.Entries) + 1
	e.Chain = chainHash(s.prev, e)
	s.prev = e.Chain
	s.trace.Entries = append(s.trace.Entries, e)
}

// chainHash computes the tamper-evident link for an entry. It covers every
// field the audit relies on, including node identity, order, occurrence,
// returned value, rule version and evidence digest.
func chainHash(prev string, e *Entry) string {
	parts := []string{
		"chain",
		prev,
		strconv.Itoa(e.Seq),
		e.NodeID,
		strconv.Itoa(e.Occurrence),
		string(e.Kind),
		e.RuleName,
		e.RuleVersion,
		string(e.Status),
		strconv.FormatBool(e.Value),
		"", // evidence digest placeholder
		e.Reason,
	}
	if e.Evidence != nil {
		parts[10] = e.Evidence.digest()
	}
	h := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(h[:])
}
