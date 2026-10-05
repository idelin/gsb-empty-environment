package docexec

import (
	"fmt"
)

// BlockStatus is the outcome of a single block.
type BlockStatus string

const (
	StatusSucceeded BlockStatus = "succeeded"
	StatusFailed    BlockStatus = "failed"
	StatusSkipped   BlockStatus = "skipped"
)

// BlockResult records one block's outcome and its captured output.
type BlockResult struct {
	Block  *Block
	Status BlockStatus
	Output []string // lines printed before completion or failure
	Err    error    // set when Status is failed
	Reason string   // set when Status is skipped
}

// RunResult is the outcome of executing a whole document.
type RunResult struct {
	Results []*BlockResult // in document order
	Errors  []error        // validation errors (duplicates, cycles, unknown deps)
	NS      *Namespace     // final shared namespace
}

// Run validates the block graph and executes blocks in dependency order.
// A failed block only skips the blocks that (transitively) depend on it;
// independent blocks still run. Failed blocks commit no state.
func Run(doc *Document) *RunResult {
	return RunWithNamespace(doc, NewNamespace())
}

// RunWithNamespace is Run against a caller-provided namespace.
func RunWithNamespace(doc *Document, ns *Namespace) *RunResult {
	res := &RunResult{NS: ns}
	byName := make(map[string]*Block, len(doc.Blocks))
	results := make(map[*Block]*BlockResult, len(doc.Blocks))

	// Validation: duplicate block names. The first definition wins;
	// later duplicates fail without running.
	for _, b := range doc.Blocks {
		if first, dup := byName[b.Name]; dup {
			res.Errors = append(res.Errors, fmt.Errorf(
				"duplicate block name %q (lines %d and %d)", b.Name, first.StartLine, b.StartLine))
			results[b] = &BlockResult{Block: b, Status: StatusFailed,
				Err: fmt.Errorf("duplicate block name %q", b.Name)}
		} else {
			byName[b.Name] = b
		}
	}

	// Validation: unknown dependencies. The block is skipped, and the skip
	// propagates to its dependents like a failure would.
	skipReason := make(map[*Block]string)
	for _, b := range doc.Blocks {
		if results[b] != nil {
			continue
		}
		for _, need := range b.Needs {
			if _, ok := byName[need]; !ok {
				res.Errors = append(res.Errors, fmt.Errorf(
					"block %q (line %d) depends on unknown block %q", b.Name, b.StartLine, need))
				skipReason[b] = fmt.Sprintf("unknown dependency %q", need)
			}
		}
		if b.Needs != nil {
			for _, need := range b.Needs {
				if need == b.Name {
					skipReason[b] = "dependency cycle: block depends on itself"
				}
			}
		}
	}

	// Worklist scheduling: a block becomes decidable once every dependency
	// has a result. Among decidable blocks, document order wins so the
	// execution order is stable and matches reading order where possible.
	remaining := make(map[*Block]bool)
	for _, b := range doc.Blocks {
		if results[b] == nil {
			remaining[b] = true
		}
	}
	for progress := true; progress && len(remaining) > 0; {
		progress = false
		for _, b := range doc.Blocks { // document order
			if !remaining[b] {
				continue
			}
			if !depsResolved(b, byName, results) {
				continue
			}
			delete(remaining, b)
			progress = true
			if reason, skip := skipReason[b]; skip {
				results[b] = &BlockResult{Block: b, Status: StatusSkipped, Reason: reason}
				continue
			}
			if reason := failedDep(b, byName, results); reason != "" {
				results[b] = &BlockResult{Block: b, Status: StatusSkipped, Reason: reason}
				continue
			}
			output, err := runInstructions(b.Code, ns)
			if err != nil {
				results[b] = &BlockResult{Block: b, Status: StatusFailed, Output: output, Err: err}
			} else {
				results[b] = &BlockResult{Block: b, Status: StatusSucceeded, Output: output}
			}
		}
	}

	// Whatever remains is part of, or downstream of, a dependency cycle.
	for _, b := range doc.Blocks {
		if !remaining[b] {
			continue
		}
		res.Errors = append(res.Errors, fmt.Errorf(
			"dependency cycle involves block %q (line %d)", b.Name, b.StartLine))
		results[b] = &BlockResult{Block: b, Status: StatusSkipped, Reason: "dependency cycle detected"}
	}

	for _, b := range doc.Blocks {
		res.Results = append(res.Results, results[b])
	}
	return res
}

// depsResolved reports whether every known dependency of b has a result.
func depsResolved(b *Block, byName map[string]*Block, results map[*Block]*BlockResult) bool {
	for _, need := range b.Needs {
		dep, ok := byName[need]
		if !ok {
			continue // unknown deps are handled via skipReason
		}
		if dep == b {
			continue // self-dependency is handled via skipReason
		}
		if results[dep] == nil {
			return false
		}
	}
	return true
}

// failedDep returns a skip reason if any dependency did not succeed.
func failedDep(b *Block, byName map[string]*Block, results map[*Block]*BlockResult) string {
	for _, need := range b.Needs {
		dep, ok := byName[need]
		if !ok || dep == b {
			continue
		}
		if r := results[dep]; r != nil && r.Status != StatusSucceeded {
			return fmt.Sprintf("dependency %q %s", need, r.Status)
		}
	}
	return ""
}
