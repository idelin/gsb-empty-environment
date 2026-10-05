package main

import (
	"fmt"
	"math/big"
	"sort"
)

// edgeRef is an adjacency entry inside one shard's version graph.
type edgeRef struct {
	to   int64
	file int
}

// shard is the per-shard version graph restricted to edges that cannot
// overshoot the target (to <= target), so any path stays below the target.
type shard struct {
	name     string
	target   int64
	adj      map[int64][]edgeRef
	versions []int64 // sorted, includes 0 and target
	// shareCount[g] counts this shard's edges whose serving file's
	// dependency closure contains g; used by the fractional lower bound.
	shareCount map[int]int
}

// env is the validated, solver-ready form of an Input.
type env struct {
	n         int
	ids       []string // sorted file IDs
	index     map[string]int
	bytes     []*big.Int
	deps      []bitmap // direct dependency masks
	closure   []bitmap // transitive dependency masks, including the file itself
	corrupted []bool
	usable    []bool // not corrupted and not tainted by a corrupted dependency
	shards    []shard
	diags     *Diagnostics
}

// buildEnv validates the input and precomputes solver structures. Hard
// errors (duplicate IDs, missing IDs, dependency cycles, malformed edges)
// are returned separately from soft corruption diagnostics.
func buildEnv(in *Input) (*env, []DiagError) {
	var errs []DiagError

	ids := make([]string, 0, len(in.Files))
	seen := map[string]bool{}
	for _, f := range in.Files {
		if f.ID == "" {
			errs = append(errs, DiagError{Kind: "duplicate_file_id", Detail: "empty file id"})
			continue
		}
		if seen[f.ID] {
			errs = append(errs, DiagError{Kind: "duplicate_file_id", Detail: "duplicate file id " + f.ID})
			continue
		}
		seen[f.ID] = true
		ids = append(ids, f.ID)
	}
	sort.Strings(ids)
	n := len(ids)
	index := make(map[string]int, n)
	for i, id := range ids {
		index[id] = i
	}

	e := &env{
		n:         n,
		ids:       ids,
		index:     index,
		bytes:     make([]*big.Int, n),
		deps:      make([]bitmap, n),
		corrupted: make([]bool, n),
		diags:     &Diagnostics{CorruptedFiles: []string{}, CascadedUnusableFiles: []string{}},
	}
	for _, f := range in.Files {
		i, ok := index[f.ID]
		if !ok {
			continue // duplicate already reported
		}
		e.bytes[i] = f.Bytes.Int()
		e.corrupted[i] = f.Corrupted
		e.deps[i] = newBitmap(n)
		for _, d := range f.Dependencies {
			j, ok := index[d]
			if !ok {
				errs = append(errs, DiagError{Kind: "missing_id", Detail: "file " + f.ID + " depends on unknown id " + d})
				continue
			}
			e.deps[i].set(j)
		}
	}

	for si, sh := range in.Shards {
		name := sh.Name
		if name == "" {
			name = fmt.Sprintf("shard%d", si)
		}
		if sh.Target < 0 {
			errs = append(errs, DiagError{Kind: "invalid_target", Detail: fmt.Sprintf("shard %s has negative target %d", name, sh.Target)})
			continue
		}
		s := shard{name: name, target: sh.Target, adj: map[int64][]edgeRef{}}
		vset := map[int64]bool{0: true, sh.Target: true}
		for _, ed := range sh.Edges {
			if ed.From < 0 || ed.To <= ed.From {
				errs = append(errs, DiagError{Kind: "invalid_edge", Detail: fmt.Sprintf("shard %s edge %d->%d is not strictly increasing", name, ed.From, ed.To)})
				continue
			}
			fi, ok := index[ed.File]
			if !ok {
				errs = append(errs, DiagError{Kind: "missing_id", Detail: fmt.Sprintf("shard %s edge %d->%d references unknown file id %s", name, ed.From, ed.To, ed.File)})
				continue
			}
			if ed.To > sh.Target {
				continue // a valid path may never overshoot the target
			}
			s.adj[ed.From] = append(s.adj[ed.From], edgeRef{to: ed.To, file: fi})
			vset[ed.From] = true
			vset[ed.To] = true
		}
		for v := range vset {
			s.versions = append(s.versions, v)
		}
		sort.Slice(s.versions, func(a, b int) bool { return s.versions[a] < s.versions[b] })
		// Deterministic adjacency order: by target version, then file index.
		for v := range s.adj {
			lst := s.adj[v]
			sort.Slice(lst, func(a, b int) bool {
				if lst[a].to != lst[b].to {
					return lst[a].to < lst[b].to
				}
				return lst[a].file < lst[b].file
			})
		}
		e.shards = append(e.shards, s)
	}

	if len(errs) > 0 {
		return nil, errs
	}

	// Dependency cycle detection (iterative DFS, three colors).
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make([]int, n)
	var stack []int
	var cyc []string
	var visit func(i int) bool
	visit = func(i int) bool {
		color[i] = gray
		stack = append(stack, i)
		found := false
		e.deps[i].forEachSet(n, func(d int) {
			if found {
				return
			}
			if color[d] == gray {
				// Reconstruct the cycle from the stack.
				start := 0
				for k, v := range stack {
					if v == d {
						start = k
						break
					}
				}
				for _, v := range stack[start:] {
					cyc = append(cyc, ids[v])
				}
				cyc = append(cyc, ids[d])
				found = true
				return
			}
			if color[d] == white && visit(d) {
				found = true
				return
			}
		})
		stack = stack[:len(stack)-1]
		color[i] = black
		return found
	}
	for i := 0; i < n; i++ {
		if color[i] == white && visit(i) {
			break
		}
	}
	if len(cyc) > 0 {
		errs = append(errs, DiagError{Kind: "dependency_cycle", Detail: "dependency cycle: " + joinStrings(cyc, " -> ")})
		return nil, errs
	}

	// Transitive dependency closures (graph is acyclic here).
	e.closure = make([]bitmap, n)
	done := make([]bool, n)
	var buildClosure func(i int) bitmap
	buildClosure = func(i int) bitmap {
		if done[i] {
			return e.closure[i]
		}
		done[i] = true
		m := newBitmap(n)
		m.set(i)
		e.deps[i].forEachSet(n, func(d int) {
			m.orIn(buildClosure(d))
		})
		e.closure[i] = m
		return m
	}
	for i := 0; i < n; i++ {
		buildClosure(i)
	}

	// Per-shard share counts for the lower bound weights.
	for si := range e.shards {
		sc := map[int]int{}
		for _, lst := range e.shards[si].adj {
			for _, ed := range lst {
				e.closure[ed.file].forEachSet(n, func(g int) {
					sc[g]++
				})
			}
		}
		e.shards[si].shareCount = sc
	}

	// Corruption cascade: a file is unusable if it is corrupted or any of
	// its dependencies (transitively) is corrupted.
	rev := make([]bitmap, n)
	for i := 0; i < n; i++ {
		rev[i] = newBitmap(n)
	}
	for i := 0; i < n; i++ {
		e.deps[i].forEachSet(n, func(d int) {
			rev[d].set(i)
		})
	}
	unusable := newBitmap(n)
	queue := []int{}
	for i := 0; i < n; i++ {
		if e.corrupted[i] && !unusable.get(i) {
			unusable.set(i)
			queue = append(queue, i)
		}
	}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		rev[v].forEachSet(n, func(u int) {
			if !unusable.get(u) {
				unusable.set(u)
				queue = append(queue, u)
			}
		})
	}
	e.usable = make([]bool, n)
	for i := 0; i < n; i++ {
		e.usable[i] = !unusable.get(i)
		if e.corrupted[i] {
			e.diags.CorruptedFiles = append(e.diags.CorruptedFiles, ids[i])
		} else if unusable.get(i) {
			e.diags.CascadedUnusableFiles = append(e.diags.CascadedUnusableFiles, ids[i])
		}
	}
	return e, nil
}

func joinStrings(parts []string, sep string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out
}

// costOfMask sums the bytes of all files in the mask (big integers).
func (e *env) costOfMask(m bitmap) *big.Int {
	sum := new(big.Int)
	m.forEachSet(e.n, func(i int) {
		sum.Add(sum, e.bytes[i])
	})
	return sum
}

// idsOf returns the sorted file IDs of a mask (indices are sorted by ID).
func (e *env) idsOf(m bitmap) []string {
	out := []string{}
	m.forEachSet(e.n, func(i int) {
		out = append(out, e.ids[i])
	})
	return out
}

// addedCost returns the extra cost of pulling in the dependency closure of
// file f on top of the already included set inc.
func (e *env) addedCost(f int, inc bitmap) *big.Int {
	sum := new(big.Int)
	e.closure[f].forEachSet(e.n, func(i int) {
		if !inc.get(i) {
			sum.Add(sum, e.bytes[i])
		}
	})
	return sum
}

// viable computes the greatest dependency-closed subset of files that are
// usable and not excluded: exactly the files a completion may still use.
func (e *env) viable(exc bitmap) bitmap {
	poss := newBitmap(e.n)
	for i := 0; i < e.n; i++ {
		if e.usable[i] && !exc.get(i) {
			poss.set(i)
		}
	}
	for {
		changed := false
		for i := 0; i < e.n; i++ {
			if poss.get(i) && !e.deps[i].subsetOf(poss) {
				poss.clear(i)
				changed = true
			}
		}
		if !changed {
			return poss
		}
	}
}

// shardReachable reports whether version target is reachable from 0 using
// only edges whose file is in mask. Edges with to > target were already
// filtered out, so no path can overshoot the target.
func shardReachable(sh shard, mask bitmap) bool {
	if sh.target == 0 {
		return true
	}
	visited := map[int64]bool{0: true}
	stack := []int64{0}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if v == sh.target {
			return true
		}
		for _, ed := range sh.adj[v] {
			if mask.get(ed.file) && !visited[ed.to] {
				visited[ed.to] = true
				stack = append(stack, ed.to)
			}
		}
	}
	return false
}
