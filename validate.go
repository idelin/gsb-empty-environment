package netsec

import (
	"errors"
	"fmt"
)

// validate structurally checks the CFG. A review service must not silently
// analyze malformed input, so every violation is reported.
func (g *CFG) validate() error {
	var errs []error

	if g == nil {
		return errors.New("nil CFG")
	}
	if len(g.Blocks) == 0 {
		return errors.New("CFG has no blocks")
	}
	if g.Entry == "" {
		errs = append(errs, errors.New("empty entry block"))
	} else if _, ok := g.Blocks[g.Entry]; !ok {
		errs = append(errs, fmt.Errorf("entry block %q is not defined", g.Entry))
	}

	for id, block := range g.Blocks {
		if block == nil {
			errs = append(errs, fmt.Errorf("block %q is nil", id))
			continue
		}
		if block.ID != "" && block.ID != id {
			errs = append(errs, fmt.Errorf("block key %q does not match ID %q", id, block.ID))
		}
		for i, op := range block.Ops {
			if op == nil {
				errs = append(errs, fmt.Errorf("block %q op %d is nil", id, i))
				continue
			}
			switch op := op.(type) {
			case OpAuth:
				if op.Site == "" {
					errs = append(errs, fmt.Errorf("block %q: OpAuth without site", id))
				}
			case OpRecover:
				if i != 0 {
					errs = append(errs, fmt.Errorf("block %q: OpRecover must be the first operation", id))
				}
			case OpErrorAssign:
				if op.Var == "" {
					errs = append(errs, fmt.Errorf("block %q: OpErrorAssign without variable", id))
				}
				if op.Clear && op.Source != "" && op.Source != "nil" {
					errs = append(errs, fmt.Errorf("block %q: clearing assignment %q has non-nil source", id, op.Var))
				}
			case OpPanic, OpProtectedCallback, OpReturn:
				// site-less operations are allowed for synthetic nodes.
			default:
				errs = append(errs, fmt.Errorf("block %q: unknown op type %T", id, op))
			}
			// Terminating operations must be last in their block.
			if i < len(block.Ops)-1 {
				switch op.(type) {
				case OpReturn, OpPanic:
					errs = append(errs, fmt.Errorf("block %q: %T must be the final operation", id, op))
				}
			}
		}
	}

	successors := map[string][]Edge{}
	for i, edge := range g.Edges {
		if _, ok := g.Blocks[edge.From]; !ok {
			errs = append(errs, fmt.Errorf("edge %d: source block %q is not defined", i, edge.From))
		}
		if _, ok := g.Blocks[edge.To]; !ok {
			errs = append(errs, fmt.Errorf("edge %d: target block %q is not defined", i, edge.To))
		}
		if edge.From == edge.To && edge.Kind == EdgeNormal {
			errs = append(errs, fmt.Errorf("edge %d: self-loop on block %q", i, edge.From))
		}
		successors[edge.From] = append(successors[edge.From], edge)
	}

	for id, block := range g.Blocks {
		if block == nil {
			continue
		}
		out := successors[id]
		last := lastOp(block)

		switch last.(type) {
		case OpReturn:
			if len(out) != 0 {
				errs = append(errs, fmt.Errorf("return block %q must have no outgoing edges", id))
			}
			continue
		case OpPanic:
			if !edgeKinds(out, EdgeException) {
				errs = append(errs, fmt.Errorf("panic block %q requires an exception edge", id))
			}
			if edgeKinds(out, EdgeNormal) {
				errs = append(errs, fmt.Errorf("panic block %q cannot have a normal edge", id))
			}
		case OpAuth:
			auth := last.(OpAuth)
			hasNormal := edgeKinds(out, EdgeNormal)
			hasException := edgeKinds(out, EdgeException)
			if !hasNormal {
				errs = append(errs, fmt.Errorf("auth block %q requires a normal (success) edge", id))
			}
			if !hasException {
				errs = append(errs, fmt.Errorf("auth block %q requires an exception (failure) edge", id))
			}
			if len(out) != 2 {
				errs = append(errs, fmt.Errorf("auth block %q must have exactly two outgoing edges", id))
			}
			_ = auth
		case OpRecover:
			if edgeKinds(out, EdgeException) {
				errs = append(errs, fmt.Errorf("recover block %q cannot leave on an exception edge", id))
			}
		}

		for _, edge := range out {
			switch last.(type) {
			case OpAuth, OpPanic:
				// exception/normal mix is the intended modeling.
			default:
				if edge.Kind == EdgeException {
					errs = append(errs, fmt.Errorf("block %q has an exception edge without an auth/panic terminator", id))
				}
			}
		}
	}

	return errors.Join(errs...)
}

func lastOp(block *Block) Op {
	if len(block.Ops) == 0 {
		return nil
	}
	return block.Ops[len(block.Ops)-1]
}

func edgeKinds(edges []Edge, kind EdgeKind) bool {
	for _, edge := range edges {
		if edge.Kind == kind {
			return true
		}
	}
	return false
}
