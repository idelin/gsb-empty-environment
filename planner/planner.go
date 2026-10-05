// Package planner schedules recovery of compressed blocks under a limited
// disk quota. Each block goes through three stages with distinct footprints:
//
//	download    compressed bytes appear on disk            (+Compressed)
//	decompress  temporary uncompressed bytes appear too    (+Temp, peak point)
//	publish     atomic swap: temp and compressed are freed,
//	            the final bytes remain                     (-Compressed-Temp+Final)
//
// Release timing: a block's Final bytes stay live until every block that
// depends on it has been published, unless the block is an output (outputs
// are retained until the end). A block shared by several dependents is
// downloaded, decompressed and accounted exactly once.
//
// The peak is the maximum instantaneous usage over this event timeline.
// It is NOT the final on-disk size: temporary and compressed copies inflate
// usage mid-run, and released finals deflate it before the run ends.
package planner

import "fmt"

// Block describes one recoverable unit and its per-stage disk footprint.
type Block struct {
	ID         string   `json:"id"`
	Compressed int64    `json:"compressed"` // bytes held after download
	Temp       int64    `json:"temp"`       // extra bytes held while decompressing
	Final      int64    `json:"final"`      // bytes held after atomic publish
	Deps       []string `json:"deps,omitempty"`
}

// Spec is the planner input: the dependency DAG, per-stage sizes, and an
// optional disk quota. Outputs defaults to the DAG sinks.
type Spec struct {
	Blocks  []Block  `json:"blocks"`
	Outputs []string `json:"outputs,omitempty"`
	Quota   int64    `json:"quota,omitempty"` // 0 = no quota
}

// Planner is the validated, indexed form of a Spec.
type Planner struct {
	n          int
	ids        []string
	compressed []int64
	temp       []int64
	final      []int64
	deps       [][]int // deps[i]: blocks that must publish before i
	dependents [][]int // dependents[i]: blocks that list i in Deps
	indegree   []int
	output     []bool
}

// New validates the spec and builds a Planner.
func New(spec Spec) (*Planner, error) {
	n := len(spec.Blocks)
	if n == 0 {
		return nil, fmt.Errorf("spec has no blocks")
	}
	p := &Planner{
		n:          n,
		ids:        make([]string, n),
		compressed: make([]int64, n),
		temp:       make([]int64, n),
		final:      make([]int64, n),
		deps:       make([][]int, n),
		dependents: make([][]int, n),
		indegree:   make([]int, n),
		output:     make([]bool, n),
	}
	index := make(map[string]int, n)
	for i, b := range spec.Blocks {
		if b.ID == "" {
			return nil, fmt.Errorf("block %d: empty id", i)
		}
		if _, dup := index[b.ID]; dup {
			return nil, fmt.Errorf("duplicate block id %q", b.ID)
		}
		if b.Compressed < 0 || b.Temp < 0 || b.Final < 0 {
			return nil, fmt.Errorf("block %q: negative size", b.ID)
		}
		index[b.ID] = i
		p.ids[i] = b.ID
		p.compressed[i] = b.Compressed
		p.temp[i] = b.Temp
		p.final[i] = b.Final
	}
	for i, b := range spec.Blocks {
		seen := make(map[int]bool, len(b.Deps))
		for _, depID := range b.Deps {
			d, ok := index[depID]
			if !ok {
				return nil, fmt.Errorf("block %q: unknown dependency %q", b.ID, depID)
			}
			if d == i {
				return nil, fmt.Errorf("block %q depends on itself", b.ID)
			}
			if seen[d] {
				continue // shared dep listed twice still counts once
			}
			seen[d] = true
			p.deps[i] = append(p.deps[i], d)
			p.dependents[d] = append(p.dependents[d], i)
		}
		p.indegree[i] = len(p.deps[i])
	}
	if len(spec.Outputs) == 0 {
		for i := 0; i < n; i++ {
			p.output[i] = len(p.dependents[i]) == 0
		}
	} else {
		for _, id := range spec.Outputs {
			i, ok := index[id]
			if !ok {
				return nil, fmt.Errorf("unknown output block %q", id)
			}
			p.output[i] = true
		}
	}
	if _, err := p.topoOrder(); err != nil {
		return nil, err
	}
	return p, nil
}

// N returns the number of blocks.
func (p *Planner) N() int { return p.n }

// IDs returns block IDs indexed the same way as internal orders.
func (p *Planner) IDs() []string { return append([]string(nil), p.ids...) }

// topoOrder returns a topological order or an error if the graph has a cycle.
func (p *Planner) topoOrder() ([]int, error) {
	indeg := append([]int(nil), p.indegree...)
	queue := make([]int, 0, p.n)
	for i := 0; i < p.n; i++ {
		if indeg[i] == 0 {
			queue = append(queue, i)
		}
	}
	order := make([]int, 0, p.n)
	for len(queue) > 0 {
		i := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		order = append(order, i)
		for _, j := range p.dependents[i] {
			indeg[j]--
			if indeg[j] == 0 {
				queue = append(queue, j)
			}
		}
	}
	if len(order) != p.n {
		return nil, fmt.Errorf("dependency graph has a cycle")
	}
	return order, nil
}
