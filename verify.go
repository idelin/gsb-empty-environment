package main

import (
	"fmt"
	"math/big"
	"sort"
)

// Verification is the result of independently checking a plan.
type Verification struct {
	OK       bool     `json:"ok"`
	Cost     string   `json:"cost,omitempty"`
	Problems []string `json:"problems,omitempty"`
}

// Verify independently checks that the given file list is a valid restore
// plan for the input. It is deliberately implemented from the raw input
// (its own maps, its own graph walks) rather than from the solver's model,
// so it can catch solver bugs. It reports the recomputed total cost with
// each file charged exactly once.
func Verify(in *Input, files []string) Verification {
	var v Verification

	byID := map[string]FileSpec{}
	for _, f := range in.Files {
		byID[f.ID] = f
	}

	selected := map[string]bool{}
	for _, id := range files {
		if _, ok := byID[id]; !ok {
			v.Problems = append(v.Problems, "unknown file in plan: "+id)
			continue
		}
		if selected[id] {
			v.Problems = append(v.Problems, "duplicate file in plan: "+id)
		}
		selected[id] = true
	}

	// Integrity.
	for id := range selected {
		f := byID[id]
		if f.Intact != nil && !*f.Intact {
			v.Problems = append(v.Problems, "plan selects corrupted file: "+id)
		}
	}

	// Dependency closure: every transitive dependency must be selected.
	for id := range selected {
		stack := append([]string(nil), byID[id].Deps...)
		seen := map[string]bool{}
		for len(stack) > 0 {
			d := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[d] {
				continue
			}
			seen[d] = true
			df, ok := byID[d]
			if !ok {
				v.Problems = append(v.Problems, fmt.Sprintf("file %s depends on unknown file %s", id, d))
				continue
			}
			if !selected[d] {
				v.Problems = append(v.Problems, fmt.Sprintf("file %s requires missing dependency %s", id, d))
			}
			stack = append(stack, df.Deps...)
		}
	}

	// Each shard must have a 0 -> target path using only selected files,
	// never overshooting the target.
	for si, sp := range in.Shards {
		name := sp.Name
		if name == "" {
			name = fmt.Sprintf("shard-%d", si)
		}
		adj := map[int64][]int64{}
		for _, e := range sp.Edges {
			if !selected[e.File] || e.To > sp.Target || e.To <= e.From {
				continue
			}
			adj[e.From] = append(adj[e.From], e.To)
		}
		reached := map[int64]bool{0: true}
		stack := []int64{0}
		for len(stack) > 0 {
			x := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, y := range adj[x] {
				if !reached[y] {
					reached[y] = true
					stack = append(stack, y)
				}
			}
		}
		if !reached[sp.Target] {
			v.Problems = append(v.Problems, fmt.Sprintf("shard %s: no path from 0 to %d within the selected files", name, sp.Target))
		}
		_ = si
	}

	// Cost: each selected file charged once, arbitrary precision.
	total := new(big.Int)
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		b := byID[id].Bytes.V
		if b != nil {
			total.Add(total, b)
		}
	}
	v.Cost = total.String()

	v.OK = len(v.Problems) == 0
	return v
}
