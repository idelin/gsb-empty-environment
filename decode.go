package errser

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Decode parses an envelope produced by Encode and rebuilds the error
// graph. It is safe against hostile input:
//
//   - inputs larger than MaxInputBytes are rejected before parsing;
//   - node counts, string lengths and edge indices are validated before
//     any graph allocation proportional to them;
//   - kinds dispatch through a closed switch — unknown kinds decode to
//     KindOpaque. There is no reflection and no name-based lookup, so a
//     payload can never cause a host type to be instantiated or host
//     code to run.
func Decode(data []byte, lim Limits) (error, error) {
	lim = lim.withDefaults()
	if len(data) > lim.MaxInputBytes {
		return nil, fmt.Errorf("errser: input %d bytes exceeds limit %d", len(data), lim.MaxInputBytes)
	}
	var env Envelope
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return nil, fmt.Errorf("errser: invalid envelope: %w", err)
	}
	if err := validateEnvelope(&env, lim); err != nil {
		return nil, err
	}
	return buildGraph(&env)
}

// validateEnvelope checks every structural invariant before buildGraph
// allocates anything proportional to the declared counts.
func validateEnvelope(env *Envelope, lim Limits) error {
	if env.V != 1 {
		return fmt.Errorf("errser: unsupported version %d", env.V)
	}
	if len(env.Nodes) == 0 {
		return errors.New("errser: empty node list")
	}
	if len(env.Nodes) > lim.MaxNodes {
		return fmt.Errorf("%w: %d nodes > limit %d", ErrBudget, len(env.Nodes), lim.MaxNodes)
	}
	if env.Root < 0 || env.Root >= len(env.Nodes) {
		return fmt.Errorf("errser: root index %d out of range", env.Root)
	}
	maxMsgRunes := lim.MaxMsgLen + 1 // truncated messages carry an ellipsis
	for i, n := range env.Nodes {
		if n.ID != i {
			return fmt.Errorf("errser: node %d has non-sequential id %d", i, n.ID)
		}
		if len([]rune(n.Msg)) > maxMsgRunes {
			return fmt.Errorf("errser: node %d message exceeds limit", i)
		}
		if len(n.Ctx) > lim.MaxCtxKeys {
			return fmt.Errorf("errser: node %d context exceeds limit", i)
		}
		for k, v := range n.Ctx {
			if !isSafeScalar(v) {
				return fmt.Errorf("errser: node %d ctx %q has non-scalar value", i, k)
			}
		}
		isRef := n.Ref != nil
		isReal := n.Kind != ""
		if isRef == isReal {
			return fmt.Errorf("errser: node %d must be exactly one of ref/real", i)
		}
		if isRef {
			// Refs must point backwards (encoder emits targets first);
			// the Cycle flag is informational and needs no check.
			if *n.Ref < 0 || *n.Ref >= i {
				return fmt.Errorf("errser: node %d ref %d must point backwards", i, *n.Ref)
			}
			if n.Cause != nil || len(n.Children) != 0 || len(n.Ctx) != 0 {
				return fmt.Errorf("errser: node %d ref carries payload", i)
			}
			continue
		}
		if n.Cause != nil && (*n.Cause <= i || *n.Cause >= len(env.Nodes)) {
			return fmt.Errorf("errser: node %d cause index %d out of range", i, *n.Cause)
		}
		if len(n.Children) > lim.MaxNodes {
			return fmt.Errorf("%w: node %d children", ErrBudget, i)
		}
		for _, c := range n.Children {
			if c <= i || c >= len(env.Nodes) {
				return fmt.Errorf("errser: node %d child index %d out of range", i, c)
			}
		}
		if n.Trunc != "" && Kind(n.Kind) != KindTruncated {
			return fmt.Errorf("errser: node %d trunc marker on non-truncated kind", i)
		}
	}
	return nil
}

func isSafeScalar(v any) bool {
	switch v.(type) {
	case nil, bool, string, float64:
		return true
	}
	return false
}

// buildGraph constructs the graph in two passes: shells first (closed
// kind dispatch), then edges. Cycles arrive as backward refs and are
// reconstructed as real pointer cycles, which Error() and re-encoding
// handle without unbounded recursion.
func buildGraph(env *Envelope) (error, error) {
	built := make([]error, len(env.Nodes))
	for i, n := range env.Nodes {
		if n.Ref != nil {
			continue // wired in pass 2
		}
		switch Kind(n.Kind) {
		case KindValidation, KindTimeout, KindAuth, KindInternal, KindOpaque:
			built[i] = &Error{kind: Kind(n.Kind), msg: n.Msg, ctx: n.Ctx}
		case KindTruncated:
			built[i] = &Error{kind: KindTruncated, msg: n.Trunc}
		case KindAggregate:
			built[i] = &Aggregate{msg: n.Msg}
		default:
			// Unknown kind: keep the generic code, preserve the display
			// text, never dispatch on the attacker-controlled string.
			built[i] = &Error{kind: KindOpaque, msg: n.Msg, ctx: n.Ctx}
		}
	}
	// Resolve refs before wiring edges: refs point backwards, so every
	// target is already a shell or a resolved ref. This restores both
	// shared-node identity and real pointer cycles.
	for i, n := range env.Nodes {
		if n.Ref != nil {
			built[i] = built[*n.Ref]
		}
	}
	for i, n := range env.Nodes {
		if n.Ref != nil {
			continue
		}
		switch t := built[i].(type) {
		case *Error:
			if n.Cause != nil {
				t.cause = built[*n.Cause]
			}
		case *Aggregate:
			for _, c := range n.Children {
				t.errs = append(t.errs, built[c])
			}
		}
	}
	return built[env.Root], nil
}
