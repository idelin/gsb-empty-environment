package main

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
)

// Amount is a non-negative arbitrary-precision integer. It accepts a JSON
// number or a JSON string, and always marshals as a string so that values
// beyond 64 bits survive a round trip.
type Amount struct {
	V *big.Int
}

func (a *Amount) UnmarshalJSON(b []byte) error {
	s := string(b)
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &s); err != nil {
			return fmt.Errorf("invalid byte amount: %w", err)
		}
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return fmt.Errorf("invalid byte amount %q", s)
	}
	if v.Sign() < 0 {
		return fmt.Errorf("negative byte amount %q", s)
	}
	a.V = v
	return nil
}

func (a Amount) MarshalJSON() ([]byte, error) {
	if a.V == nil {
		return []byte(`"0"`), nil
	}
	return json.Marshal(a.V.String())
}

// FileSpec describes one backup file.
type FileSpec struct {
	ID     string   `json:"id"`
	Bytes  Amount   `json:"bytes"`
	Intact *bool    `json:"intact"` // nil means true
	Deps   []string `json:"deps"`
}

// Edge is one coverage record fromVersion -> toVersion served by a file.
type Edge struct {
	From int64  `json:"from"`
	To   int64  `json:"to"`
	File string `json:"file"`
}

// ShardSpec describes one shard and its target version.
type ShardSpec struct {
	Name   string `json:"name"`
	Target int64  `json:"target"`
	Edges  []Edge `json:"edges"`
}

// Input is the planner request.
type Input struct {
	Files  []FileSpec  `json:"files"`
	Shards []ShardSpec `json:"shards"`
}

// Diagnostic is a structured finding about the input or the plan.
type Diagnostic struct {
	Kind    string   `json:"kind"`
	Message string   `json:"message"`
	Shard   string   `json:"shard,omitempty"`
	Files   []string `json:"files,omitempty"`
}

// Solution is the planner output.
type Solution struct {
	Status      string       `json:"status"` // optimal | feasible | unknown | infeasible | error
	Files       []string     `json:"files,omitempty"`
	Cost        string       `json:"cost,omitempty"`
	LowerBound  string       `json:"lowerBound,omitempty"`
	Expansions  int64        `json:"expansions"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

type edge struct {
	to   int64
	file int
}

type shardModel struct {
	name   string
	target int64
	adj    map[int64][]edge // sorted by (to, fileID)
}

// pathInfo is one usable path: the dependency-closed set of files it needs.
type pathInfo struct {
	set bitset
}

type model struct {
	ids     []string // sorted lexicographically; index order == lex order
	index   map[string]int
	bytes   []*big.Int
	intact  []bool
	deps    [][]int
	closure []bitset // transitive dependency closure including self
	usable  []bool   // intact and whole closure intact
	shards  []shardModel
	paths   [][]pathInfo // usable paths per shard, fixed enumeration order
}

func (m *model) costOf(sel bitset) *big.Int {
	total := new(big.Int)
	sel.forEach(func(i int) { total.Add(total, m.bytes[i]) })
	return total
}

// marginalCost returns the bytes in add that are not already in sel.
func (m *model) marginalCost(sel, add bitset) *big.Int {
	total := new(big.Int)
	add.forEach(func(i int) {
		if !sel.get(i) {
			total.Add(total, m.bytes[i])
		}
	})
	return total
}

func (m *model) idsOf(sel bitset) []string {
	var out []string
	sel.forEach(func(i int) { out = append(out, m.ids[i]) })
	return out
}

// buildModel validates the input and precomputes closures and per-shard
// usable paths. Fatal problems (missing IDs, dependency cycles, malformed
// edges) are returned as diagnostics with fatal=true.
func buildModel(in *Input) (m *model, diags []Diagnostic, fatal bool) {
	m = &model{index: map[string]int{}}

	// Files: uniqueness, sorted order.
	seen := map[string]bool{}
	for _, f := range in.Files {
		if f.ID == "" {
			diags = append(diags, Diagnostic{Kind: "invalid_input", Message: "file with empty id"})
			fatal = true
			continue
		}
		if seen[f.ID] {
			diags = append(diags, Diagnostic{Kind: "duplicate_id", Message: "duplicate file id " + f.ID, Files: []string{f.ID}})
			fatal = true
			continue
		}
		seen[f.ID] = true
		m.ids = append(m.ids, f.ID)
	}
	sort.Strings(m.ids)
	n := len(m.ids)
	m.bytes = make([]*big.Int, n)
	m.intact = make([]bool, n)
	specByID := map[string]FileSpec{}
	for _, f := range in.Files {
		specByID[f.ID] = f
	}
	for i, id := range m.ids {
		m.index[id] = i
		f := specByID[id]
		if f.Bytes.V == nil {
			m.bytes[i] = new(big.Int)
		} else {
			m.bytes[i] = f.Bytes.V
		}
		m.intact[i] = f.Intact == nil || *f.Intact
	}

	// Dependency references.
	m.deps = make([][]int, n)
	for i, id := range m.ids {
		depSet := map[int]bool{}
		for _, d := range specByID[id].Deps {
			j, ok := m.index[d]
			if !ok {
				diags = append(diags, Diagnostic{
					Kind:    "missing_id",
					Message: fmt.Sprintf("file %s depends on unknown file %s", id, d),
					Files:   []string{id, d},
				})
				fatal = true
				continue
			}
			depSet[j] = true
		}
		var ds []int
		for j := range depSet {
			ds = append(ds, j)
		}
		sort.Ints(ds)
		m.deps[i] = ds
	}
	if fatal {
		return m, diags, true
	}

	// Dependency cycle detection (iterative, deterministic order).
	if cyc := findDepCycle(m); cyc != nil {
		diags = append(diags, Diagnostic{
			Kind:    "dep_cycle",
			Message: "dependency cycle detected",
			Files:   cyc,
		})
		return m, diags, true
	}

	// Transitive closures.
	m.closure = make([]bitset, n)
	done := make([]bool, n)
	var clo func(i int) bitset
	clo = func(i int) bitset {
		if done[i] {
			return m.closure[i]
		}
		c := newBitset(n)
		c.set(i)
		for _, d := range m.deps[i] {
			c.or(clo(d))
		}
		m.closure[i] = c
		done[i] = true
		return c
	}
	for i := 0; i < n; i++ {
		clo(i)
	}

	// Integrity and corrupted-dependency cascade.
	m.usable = make([]bool, n)
	var corrupted, cascaded []string
	for i := 0; i < n; i++ {
		ok := true
		m.closure[i].forEach(func(j int) {
			if !m.intact[j] {
				ok = false
			}
		})
		m.usable[i] = ok
		if !m.intact[i] {
			corrupted = append(corrupted, m.ids[i])
		} else if !ok {
			cascaded = append(cascaded, m.ids[i])
		}
	}
	if len(corrupted) > 0 {
		diags = append(diags, Diagnostic{
			Kind:    "corrupted",
			Message: "files marked as not intact are excluded from any plan",
			Files:   corrupted,
		})
	}
	if len(cascaded) > 0 {
		diags = append(diags, Diagnostic{
			Kind:    "corrupted_cascade",
			Message: "files unusable because a transitive dependency is corrupted",
			Files:   cascaded,
		})
	}

	// Shards and per-shard paths.
	m.paths = make([][]pathInfo, len(in.Shards))
	for si, sp := range in.Shards {
		name := sp.Name
		if name == "" {
			name = fmt.Sprintf("shard-%d", si)
		}
		if sp.Target < 0 {
			diags = append(diags, Diagnostic{Kind: "bad_version", Message: "negative target version", Shard: name})
			fatal = true
			continue
		}
		sm := shardModel{name: name, target: sp.Target, adj: map[int64][]edge{}}
		for _, e := range sp.Edges {
			fi, ok := m.index[e.File]
			if !ok {
				diags = append(diags, Diagnostic{
					Kind:    "missing_id",
					Message: fmt.Sprintf("edge %d->%d references unknown file %s", e.From, e.To, e.File),
					Shard:   name,
					Files:   []string{e.File},
				})
				fatal = true
				continue
			}
			if e.From < 0 || e.To <= e.From {
				diags = append(diags, Diagnostic{
					Kind:    "bad_edge",
					Message: fmt.Sprintf("edge %d->%d violates strictly increasing versions", e.From, e.To),
					Shard:   name,
				})
				fatal = true
				continue
			}
			sm.adj[e.From] = append(sm.adj[e.From], edge{to: e.To, file: fi})
		}
		for v := range sm.adj {
			es := sm.adj[v]
			sort.Slice(es, func(a, b int) bool {
				if es[a].to != es[b].to {
					return es[a].to < es[b].to
				}
				return m.ids[es[a].file] < m.ids[es[b].file]
			})
			sm.adj[v] = es
		}
		m.shards = append(m.shards, sm)
	}
	if fatal {
		return m, diags, true
	}

	for i := range m.shards {
		sm := &m.shards[i]
		raw := enumeratePaths(sm)
		var usable []pathInfo
		for _, p := range raw {
			set := newBitset(n)
			ok := true
			for _, fi := range p {
				if !m.usable[fi] {
					ok = false
					break
				}
				set.or(m.closure[fi])
			}
			if ok {
				usable = append(usable, pathInfo{set: set})
			}
		}
		m.paths[i] = usable
		if len(raw) == 0 {
			diags = append(diags, Diagnostic{
				Kind:    "gap",
				Message: fmt.Sprintf("no version path from 0 to %d; backup chain has a gap", sm.target),
				Shard:   sm.name,
			})
		} else if len(usable) == 0 {
			diags = append(diags, Diagnostic{
				Kind:    "unrecoverable",
				Message: "every path from 0 to target requires a corrupted file",
				Shard:   sm.name,
			})
		}
	}
	return m, diags, false
}

// findDepCycle returns one dependency cycle as a list of file IDs, or nil.
func findDepCycle(m *model) []string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	n := len(m.ids)
	color := make([]int, n)
	var stack []int
	var cycle []string
	var visit func(i int) bool
	visit = func(i int) bool {
		color[i] = gray
		stack = append(stack, i)
		for _, d := range m.deps[i] {
			if color[d] == gray {
				pos := 0
				for pos < len(stack) && stack[pos] != d {
					pos++
				}
				for _, k := range stack[pos:] {
					cycle = append(cycle, m.ids[k])
				}
				return true
			}
			if color[d] == white && visit(d) {
				return true
			}
		}
		stack = stack[:len(stack)-1]
		color[i] = black
		return false
	}
	for i := 0; i < n; i++ {
		if color[i] == white && visit(i) {
			return cycle
		}
	}
	return nil
}

// enumeratePaths lists every strictly increasing path from version 0 to the
// shard target, in a fixed DFS order over the sorted adjacency lists. Edges
// that would overshoot the target are never taken, so a path can neither
// pass the target nor come back.
func enumeratePaths(sm *shardModel) [][]int {
	var paths [][]int
	var cur []int
	var dfs func(v int64)
	dfs = func(v int64) {
		if v == sm.target {
			paths = append(paths, append([]int(nil), cur...))
			return
		}
		for _, e := range sm.adj[v] {
			if e.to > sm.target {
				continue
			}
			cur = append(cur, e.file)
			dfs(e.to)
			cur = cur[:len(cur)-1]
		}
	}
	dfs(0)
	return paths
}
