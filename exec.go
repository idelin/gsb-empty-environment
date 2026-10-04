package docexec

import (
	"fmt"
	"sort"
	"strings"
)

// Status is the outcome of one block.
type Status int

const (
	OK Status = iota
	Failed
	Skipped
)

func (s Status) String() string {
	switch s {
	case OK:
		return "ok"
	case Failed:
		return "failed"
	case Skipped:
		return "skipped"
	}
	return "unknown"
}

// BlockResult is the per-block execution record.
type BlockResult struct {
	Block  *Block
	Status Status
	// Outputs emitted by the block, each mapped to its document line.
	Outputs []Output
	// Err describes a failure (Status == Failed).
	Err string
	// ErrLine is the document line that caused the failure.
	ErrLine int
	// SkipReason explains why the block was skipped (Status == Skipped).
	SkipReason string
}

// Report is the result of executing a whole document.
type Report struct {
	Results []*BlockResult // in document order
	// Namespace is the final shared namespace after all committed blocks.
	Namespace map[string]string
}

// Result returns the result for a block id, or nil.
func (r *Report) Result(id string) *BlockResult {
	for _, br := range r.Results {
		if br.Block.ID == id {
			return br
		}
	}
	return nil
}

// Execute runs the document's blocks in dependency order against a shared
// namespace. Callers should Validate the document first; Execute also runs
// Validate and returns its error unchanged.
//
// Semantics:
//   - blocks run in topological order (document order breaks ties)
//   - each block runs transactionally: on failure none of its `set` writes
//     leak into the shared namespace
//   - a block whose dependency failed or was skipped is itself skipped,
//     transitively; unrelated blocks still run
func Execute(doc *Document) (*Report, error) {
	if err := Validate(doc); err != nil {
		return nil, err
	}

	byID := map[string]*Block{}
	for _, b := range doc.Blocks {
		byID[b.ID] = b
	}

	order := topoOrder(doc.Blocks, byID)
	results := map[string]*BlockResult{}
	ns := namespace{}

	for _, b := range order {
		br := &BlockResult{Block: b}
		results[b.ID] = br

		// Skip if any dependency did not succeed.
		var failedDeps []string
		for _, dep := range b.Needs {
			if dr := results[dep]; dr != nil && dr.Status != OK {
				failedDeps = append(failedDeps, fmt.Sprintf("%s (%s)", dep, dr.Status))
			}
		}
		if len(failedDeps) > 0 {
			br.Status = Skipped
			br.SkipReason = "dependency not ok: " + strings.Join(failedDeps, ", ")
			continue
		}

		// Transactional run: mutate a clone, commit only on success.
		scratch := ns.clone()
		outs, err := runBlock(scratch, b)
		if err != nil {
			br.Status = Failed
			br.Err = err.Error()
			if ie, ok := err.(*instrError); ok {
				br.ErrLine = ie.Line
			}
			continue
		}
		ns = scratch
		br.Status = OK
		br.Outputs = outs
	}

	rep := &Report{Namespace: map[string]string(ns)}
	for _, b := range doc.Blocks {
		rep.Results = append(rep.Results, results[b.ID])
	}
	return rep, nil
}

// topoOrder returns blocks in dependency order using Kahn's algorithm;
// ties are broken by document order for deterministic output.
// The document must be validated (acyclic) first.
func topoOrder(blocks []*Block, byID map[string]*Block) []*Block {
	indegree := map[string]int{}
	dependents := map[string][]*Block{}
	for _, b := range blocks {
		indegree[b.ID] = 0
	}
	for _, b := range blocks {
		for _, dep := range b.Needs {
			if _, ok := byID[dep]; !ok {
				continue
			}
			indegree[b.ID]++
			dependents[dep] = append(dependents[dep], b)
		}
	}

	var ready []*Block
	for _, b := range blocks {
		if indegree[b.ID] == 0 {
			ready = append(ready, b)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].Index < ready[j].Index })

	var order []*Block
	for len(ready) > 0 {
		b := ready[0]
		ready = ready[1:]
		order = append(order, b)
		for _, d := range dependents[b.ID] {
			indegree[d.ID]--
			if indegree[d.ID] == 0 {
				// insert keeping document order
				pos := sort.Search(len(ready), func(i int) bool {
					return ready[i].Index > d.Index
				})
				ready = append(ready, nil)
				copy(ready[pos+1:], ready[pos:])
				ready[pos] = d
			}
		}
	}
	return order
}
