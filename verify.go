package main

import (
	"fmt"
	"math/big"
)

// VerifyRequest pairs a planner input with a claimed solution.
type VerifyRequest struct {
	Input    Input        `json:"input"`
	Solution SolutionJSON `json:"solution"`
}

// VerifyOutput reports whether the claimed solution is legal and what the
// independently computed cost and per-shard witness paths are.
type VerifyOutput struct {
	Valid              bool               `json:"valid"`
	Reason             string             `json:"reason,omitempty"`
	ComputedTotalBytes string             `json:"computedTotalBytes,omitempty"`
	ShardPaths         map[string][]int64 `json:"shardPaths,omitempty"`
}

// verify independently checks a claimed solution: file IDs exist, no
// corrupted or cascade-tainted file is used, dependencies are included,
// every shard has a 0 -> target path that never overshoots the target, and
// the claimed total matches the recomputed big-integer cost.
func verify(req *VerifyRequest) *VerifyOutput {
	e, errs := buildEnv(&req.Input)
	if len(errs) > 0 {
		return &VerifyOutput{Valid: false, Reason: "invalid input: " + errs[0].Kind + ": " + errs[0].Detail}
	}
	bad := func(format string, args ...interface{}) *VerifyOutput {
		return &VerifyOutput{Valid: false, Reason: fmt.Sprintf(format, args...)}
	}
	claimed, ok := new(big.Int).SetString(req.Solution.TotalBytes, 10)
	if !ok || claimed.Sign() < 0 {
		return bad("invalid totalBytes %q", req.Solution.TotalBytes)
	}
	mask := newBitmap(e.n)
	seen := map[string]bool{}
	for _, id := range req.Solution.Files {
		i, ok := e.index[id]
		if !ok {
			return bad("unknown file id %s", id)
		}
		if seen[id] {
			return bad("duplicate file id %s in solution", id)
		}
		seen[id] = true
		if !e.usable[i] {
			return bad("file %s is corrupted or depends on a corrupted file", id)
		}
		mask.set(i)
	}
	for _, id := range req.Solution.Files {
		i := e.index[id]
		missing := ""
		e.deps[i].forEachSet(e.n, func(d int) {
			if !mask.get(d) {
				missing = e.ids[d]
			}
		})
		if missing != "" {
			return bad("file %s is missing dependency %s", id, missing)
		}
	}
	paths := map[string][]int64{}
	for _, sh := range e.shards {
		p := bfsPath(sh, mask)
		if p == nil {
			return bad("shard %s has no path from 0 to target %d", sh.name, sh.target)
		}
		paths[sh.name] = p
	}
	total := e.costOfMask(mask)
	if total.Cmp(claimed) != 0 {
		return bad("totalBytes mismatch: claimed %s but computed %s", claimed.String(), total.String())
	}
	return &VerifyOutput{Valid: true, ComputedTotalBytes: total.String(), ShardPaths: paths}
}

// bfsPath finds a 0 -> target version path using only edges served by files
// in mask (edges with to > target were filtered at build time, so the path
// can never overshoot the target). Written independently from the solver.
func bfsPath(sh shard, mask bitmap) []int64 {
	prev := map[int64]int64{}
	visited := map[int64]bool{0: true}
	queue := []int64{0}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		if v == sh.target {
			path := []int64{}
			for cur := v; ; {
				path = append([]int64{cur}, path...)
				if cur == 0 {
					break
				}
				cur = prev[cur]
			}
			return path
		}
		for _, ed := range sh.adj[v] {
			if mask.get(ed.file) && !visited[ed.to] {
				visited[ed.to] = true
				prev[ed.to] = v
				queue = append(queue, ed.to)
			}
		}
	}
	if sh.target == 0 {
		return []int64{0}
	}
	return nil
}
