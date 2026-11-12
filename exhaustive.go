package taskpkg

import (
	"bytes"
	"errors"
	"fmt"
)

// spaceCap guards the candidate-space estimate against overflow.
const spaceCap = 1 << 62

// Exhaustive is the brute-force reference reducer. It evaluates every
// combination of dropped optional nodes and integer candidate values and
// returns the global minimum under the same ordering as Reduce. maxCombos
// bounds the estimated candidate space; a non-positive value means
// unlimited. The result is always marked proven minimal.
func Exhaustive(root *Node, validate Validate, execute Execute, maxCombos int64) (*Result, error) {
	if err := CheckShape(root); err != nil {
		return nil, err
	}
	if validate == nil || execute == nil {
		return nil, errors.New("taskpkg: validate and execute callbacks are required")
	}
	total := int64(1)
	walk(root, func(n *Node) {
		for _, f := range n.Fields {
			seen := map[uint64]bool{}
			count := int64(0)
			for _, v := range f.Candidates {
				if !seen[v] {
					seen[v] = true
					count++
				}
			}
			total *= count
			if total > spaceCap {
				total = spaceCap
			}
		}
	})
	for i := 0; i < len(OptionalNodes(root)); i++ {
		total *= 2
		if total > spaceCap {
			total = spaceCap
			break
		}
	}
	if maxCombos > 0 && total > maxCombos {
		return nil, fmt.Errorf("taskpkg: candidate space %d exceeds limit %d", total, maxCombos)
	}

	r := newReducer(validate, execute, Budget{})
	baseData, err := Encode(root)
	if err != nil {
		return nil, fmt.Errorf("taskpkg: encode original: %w", err)
	}
	if err := validate(baseData); err != nil {
		return nil, fmt.Errorf("taskpkg: original input fails validation: %w", err)
	}
	r.executions++
	r.baseline = execute(baseData)
	if !r.baseline.OK || !r.baseline.OOB {
		return nil, errors.New("taskpkg: original input does not reproduce the out-of-bounds read")
	}

	var best *candidate
	optionals := OptionalNodes(root)
	for d := 0; d <= len(optionals); d++ {
		for _, subset := range combinations(optionals, d) {
			t := DropOptional(root, sliceToSet(subset))
			if _, err := Encode(t); err != nil {
				r.trace = append(r.trace, Step{
					Seq:      len(r.trace),
					Dropped:  append([]string(nil), subset...),
					ValidErr: err.Error(),
				})
				continue
			}
			axes := collectAxes(t)
			var rec func(i int)
			rec = func(i int) {
				if i == len(axes) {
					data, ev, repro, _ := r.evalCandidate(t, subset)
					if repro {
						cand := &candidate{
							tree:    Clone(t),
							data:    append([]byte(nil), data...),
							dropped: append([]string(nil), subset...),
							nodes:   CountNodes(t),
							bytes:   len(data),
							ev:      ev,
						}
						if best == nil || betterCandidate(cand, best) {
							best = cand
						}
					}
					return
				}
				for _, v := range axes[i].values {
					axes[i].node.Fields[axes[i].field].Value = v
					rec(i + 1)
				}
			}
			rec(0)
		}
	}
	if best == nil {
		return nil, errors.New("taskpkg: no reproducing candidate found")
	}
	return &Result{
		Tree:          best.tree,
		Encoded:       best.data,
		Dropped:       best.dropped,
		NodeCount:     best.nodes,
		Bytes:         best.bytes,
		Baseline:      r.baseline,
		Evidence:      best.ev,
		ProvenMinimal: true,
		Executions:    r.executions,
		Candidates:    r.candidates,
		Trace:         r.trace,
	}, nil
}

// CrossCheck verifies a reduction result against the exhaustive reference:
// both must agree on the minimal node count, encoded size, and encoding,
// and re-executing the reduced encoding must reproduce the recorded
// evidence exactly.
func CrossCheck(reduced, reference *Result, execute Execute) error {
	if reduced == nil || reference == nil {
		return errors.New("taskpkg: nil result")
	}
	if !reference.ProvenMinimal {
		return errors.New("taskpkg: reference result is not proven minimal")
	}
	if reduced.NodeCount != reference.NodeCount {
		return fmt.Errorf("taskpkg: node count mismatch: reduced %d, reference %d",
			reduced.NodeCount, reference.NodeCount)
	}
	if reduced.Bytes != reference.Bytes {
		return fmt.Errorf("taskpkg: encoded size mismatch: reduced %d, reference %d",
			reduced.Bytes, reference.Bytes)
	}
	if !bytes.Equal(reduced.Encoded, reference.Encoded) {
		return errors.New("taskpkg: minimal encoding differs from exhaustive reference")
	}
	if ev := execute(reduced.Encoded); ev != reduced.Evidence {
		return fmt.Errorf("taskpkg: re-execution diverges from recorded evidence: got %+v, want %+v",
			ev, reduced.Evidence)
	}
	if reduced.Evidence != reference.Evidence {
		return fmt.Errorf("taskpkg: evidence mismatch with reference: reduced %+v, reference %+v",
			reduced.Evidence, reference.Evidence)
	}
	return nil
}

// VerifyTrace replays every step of a search trace: it rebuilds each
// candidate from the input tree and the recorded dropped nodes and field
// values, then checks the encoded size, the validation verdict, and - for
// executed candidates - the vulnerability evidence against the record.
func VerifyTrace(root *Node, res *Result, validate Validate, execute Execute) error {
	if res == nil {
		return errors.New("taskpkg: nil result")
	}
	for _, step := range res.Trace {
		t := DropOptional(root, sliceToSet(step.Dropped))
		for _, fv := range step.Values {
			n := findNode(t, fv.Node)
			if n == nil {
				return fmt.Errorf("taskpkg: step %d: unknown node %q", step.Seq, fv.Node)
			}
			found := false
			for i := range n.Fields {
				if n.Fields[i].Name == fv.Field {
					n.Fields[i].Value = fv.Value
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("taskpkg: step %d: unknown field %s.%s", step.Seq, fv.Node, fv.Field)
			}
		}
		data, err := Encode(t)
		if err != nil {
			if step.Valid || step.ValidErr != err.Error() {
				return fmt.Errorf("taskpkg: step %d: encode error mismatch: %q vs %q",
					step.Seq, err, step.ValidErr)
			}
			continue
		}
		if len(data) != step.Bytes {
			return fmt.Errorf("taskpkg: step %d: encoded size mismatch: %d vs %d",
				step.Seq, len(data), step.Bytes)
		}
		if verr := validate(data); verr != nil {
			if step.Valid || step.ValidErr != verr.Error() {
				return fmt.Errorf("taskpkg: step %d: validation mismatch: %q vs %q",
					step.Seq, verr, step.ValidErr)
			}
			continue
		}
		if !step.Valid {
			return fmt.Errorf("taskpkg: step %d: recorded invalid but re-validation passed", step.Seq)
		}
		if step.Executed {
			ev := execute(data)
			if ev != step.Evidence {
				return fmt.Errorf("taskpkg: step %d: evidence mismatch: got %+v, recorded %+v",
					step.Seq, ev, step.Evidence)
			}
			if step.Reproduces != ev.Reproduces(res.Baseline) {
				return fmt.Errorf("taskpkg: step %d: reproduction verdict mismatch", step.Seq)
			}
		}
	}
	return nil
}

func findNode(root *Node, name string) *Node {
	var found *Node
	walk(root, func(n *Node) {
		if found == nil && n.Name == name {
			found = n
		}
	})
	return found
}
