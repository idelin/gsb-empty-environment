package taskpkg

import (
	"bytes"
	"container/heap"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Evidence describes one deterministic execution of a candidate input.
// OK is false for ordinary parse or execution failures, which never count
// as reproductions of the vulnerability.
type Evidence struct {
	OK         bool   // execution completed without a parse failure
	OOB        bool   // an out-of-bounds read occurred
	ReadObject string // object actually read out of bounds
	OOBOffset  int64  // offset of the read relative to the allocation start
	AllocSite  string // allocation source of the over-read buffer
	Detail     string
}

// Reproduces reports whether e preserves the vulnerability established by
// baseline: a successful execution with an out-of-bounds read at the same
// allocation site.
func (e Evidence) Reproduces(baseline Evidence) bool {
	return baseline.OOB && e.OK && e.OOB && e.AllocSite == baseline.AllocSite
}

// Validate is the independent format validator. A candidate that fails
// validation is discarded without being executed.
type Validate func(data []byte) error

// Execute runs a candidate deterministically and reports the evidence.
type Execute func(data []byte) Evidence

// Budget bounds the reduction search. A non-positive field means
// unlimited. Exhausting any budget flags the result as not proven minimal.
type Budget struct {
	MaxExecutions int // Execute invocations, including the baseline run
	MaxCandidates int // candidate evaluations, including invalid ones
}

// FieldValue pins one integer field of one node to a value.
type FieldValue struct {
	Node  string
	Field string
	Value uint64
}

// Step is the per-candidate vulnerability evidence recorded during a
// search. Every evaluated candidate appends exactly one step.
type Step struct {
	Seq        int
	Dropped    []string
	Values     []FieldValue
	Bytes      int
	Valid      bool
	ValidErr   string
	Executed   bool
	Evidence   Evidence
	Reproduces bool
}

// Result is the outcome of a reduction or exhaustive search.
type Result struct {
	Tree          *Node    // minimal reproducing tree
	Encoded       []byte   // its deterministic encoding
	Dropped       []string // optional nodes removed from the input
	NodeCount     int
	Bytes         int
	Baseline      Evidence // evidence of the original input
	Evidence      Evidence // evidence of the minimal candidate
	ProvenMinimal bool     // false when a budget ran out mid-search
	Executions    int
	Candidates    int
	Trace         []Step
}

type reducer struct {
	validate Validate
	execute  Execute

	baseline Evidence

	execLeft int64 // -1 means unlimited
	candLeft int64 // -1 means unlimited

	executions int
	candidates int
	exhausted  bool
	trace      []Step
}

func newReducer(v Validate, e Execute, b Budget) *reducer {
	r := &reducer{validate: v, execute: e, execLeft: -1, candLeft: -1}
	if b.MaxExecutions > 0 {
		r.execLeft = int64(b.MaxExecutions)
	}
	if b.MaxCandidates > 0 {
		r.candLeft = int64(b.MaxCandidates)
	}
	return r
}

func fieldValues(t *Node) []FieldValue {
	var out []FieldValue
	walk(t, func(n *Node) {
		for _, f := range n.Fields {
			out = append(out, FieldValue{Node: n.Name, Field: f.Name, Value: f.Value})
		}
	})
	return out
}

// evalCandidate validates and executes one candidate tree, appending a
// trace step. It returns the encoding, the evidence, whether the candidate
// reproduces the vulnerability, and false when a budget ran out.
func (r *reducer) evalCandidate(t *Node, dropped []string) (data []byte, ev Evidence, repro, cont bool) {
	if r.candLeft == 0 {
		r.exhausted = true
		return nil, Evidence{}, false, false
	}
	if r.candLeft > 0 {
		r.candLeft--
	}
	r.candidates++
	step := Step{Seq: len(r.trace), Dropped: append([]string(nil), dropped...)}
	data, err := Encode(t)
	if err != nil {
		step.ValidErr = err.Error()
		r.trace = append(r.trace, step)
		return nil, Evidence{}, false, true
	}
	step.Bytes = len(data)
	step.Values = fieldValues(t)
	if verr := r.validate(data); verr != nil {
		step.ValidErr = verr.Error()
		r.trace = append(r.trace, step)
		return data, Evidence{}, false, true
	}
	step.Valid = true
	if r.execLeft == 0 {
		r.exhausted = true
		r.trace = append(r.trace, step)
		return data, Evidence{}, false, false
	}
	if r.execLeft > 0 {
		r.execLeft--
	}
	r.executions++
	ev = r.execute(data)
	step.Executed = true
	step.Evidence = ev
	step.Reproduces = ev.Reproduces(r.baseline)
	r.trace = append(r.trace, step)
	return data, ev, step.Reproduces, true
}

type candidate struct {
	tree    *Node
	data    []byte
	dropped []string
	nodes   int
	bytes   int
	ev      Evidence
}

// betterCandidate orders candidates by node count, then encoded bytes,
// then lexicographic encoding for a deterministic tie-break.
func betterCandidate(a, b *candidate) bool {
	if a.nodes != b.nodes {
		return a.nodes < b.nodes
	}
	if a.bytes != b.bytes {
		return a.bytes < b.bytes
	}
	return bytes.Compare(a.data, b.data) < 0
}

// fieldAxis is one integer field of a candidate tree together with its
// deduplicated candidate values.
type fieldAxis struct {
	node   *Node
	field  int
	values []uint64
}

func collectAxes(t *Node) []fieldAxis {
	var axes []fieldAxis
	walk(t, func(n *Node) {
		for i, f := range n.Fields {
			seen := map[uint64]bool{}
			var vals []uint64
			for _, v := range f.Candidates {
				if !seen[v] {
					seen[v] = true
					vals = append(vals, v)
				}
			}
			axes = append(axes, fieldAxis{node: n, field: i, values: vals})
		}
	})
	return axes
}

// comboState is one point in the integer-candidate grid, ordered by the
// total varint size of the chosen values.
type comboState struct {
	idx   []int
	bytes int
}

type comboHeap []comboState

func (h comboHeap) Len() int { return len(h) }

func (h comboHeap) Less(i, j int) bool {
	if h[i].bytes != h[j].bytes {
		return h[i].bytes < h[j].bytes
	}
	for k := range h[i].idx {
		if h[i].idx[k] != h[j].idx[k] {
			return h[i].idx[k] < h[j].idx[k]
		}
	}
	return false
}

func (h comboHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *comboHeap) Push(x any) { *h = append(*h, x.(comboState)) }

func (h *comboHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

func comboKey(idx []int) string {
	var sb strings.Builder
	for i, v := range idx {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.Itoa(v))
	}
	return sb.String()
}

// searchSubset finds the minimal reproducing candidate for one fixed set
// of dropped nodes, enumerating integer combinations in ascending encoded
// size. completed is false when a budget ran out mid-search.
func (r *reducer) searchSubset(t *Node, dropped []string) (best *candidate, completed bool) {
	// Structural failures (e.g. dangling references) do not depend on the
	// integer values, so one encode attempt settles the whole subset.
	if _, err := Encode(t); err != nil {
		r.trace = append(r.trace, Step{
			Seq:      len(r.trace),
			Dropped:  append([]string(nil), dropped...),
			ValidErr: err.Error(),
		})
		return nil, true
	}
	axes := collectAxes(t)
	for i := range axes {
		vals := axes[i].values
		sort.Slice(vals, func(a, b int) bool {
			la, lb := uvarintLen(vals[a]), uvarintLen(vals[b])
			if la != lb {
				return la < lb
			}
			return vals[a] < vals[b]
		})
	}
	lens := make([][]int, len(axes))
	for i, ax := range axes {
		lens[i] = make([]int, len(ax.values))
		for j, v := range ax.values {
			lens[i][j] = uvarintLen(v)
		}
	}
	start := comboState{idx: make([]int, len(axes))}
	h := &comboHeap{start}
	seen := map[string]bool{comboKey(start.idx): true}
	var bestIntBytes int
	for h.Len() > 0 {
		st := heap.Pop(h).(comboState)
		if best != nil && st.bytes > bestIntBytes {
			break // only drain combinations of equal encoded size
		}
		for i, ax := range axes {
			ax.node.Fields[ax.field].Value = ax.values[st.idx[i]]
		}
		data, ev, repro, cont := r.evalCandidate(t, dropped)
		if !cont {
			return best, false
		}
		if repro {
			cand := &candidate{
				tree:    Clone(t),
				data:    append([]byte(nil), data...),
				dropped: append([]string(nil), dropped...),
				nodes:   CountNodes(t),
				bytes:   len(data),
				ev:      ev,
			}
			switch {
			case best == nil:
				best = cand
				bestIntBytes = st.bytes
			case bytes.Compare(data, best.data) < 0:
				best = cand
			}
		}
		for i := range axes {
			if st.idx[i]+1 < len(axes[i].values) {
				nidx := append([]int(nil), st.idx...)
				nidx[i]++
				key := comboKey(nidx)
				if !seen[key] {
					seen[key] = true
					heap.Push(h, comboState{
						idx:   nidx,
						bytes: st.bytes - lens[i][st.idx[i]] + lens[i][nidx[i]],
					})
				}
			}
		}
	}
	return best, true
}

func combinations(names []string, k int) [][]string {
	if k < 0 || k > len(names) {
		return nil
	}
	var out [][]string
	var rec func(start int, acc []string)
	rec = func(start int, acc []string) {
		if len(acc) == k {
			out = append(out, append([]string(nil), acc...))
			return
		}
		for i := start; i <= len(names)-(k-len(acc)); i++ {
			rec(i+1, append(acc, names[i]))
		}
	}
	rec(0, nil)
	return out
}

func sliceToSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// Reduce searches for the globally smallest counterexample: fewest nodes,
// then fewest encoded bytes. A candidate only counts when it passes the
// independent format validation and its execution reproduces an
// out-of-bounds read at the same allocation site as the original input;
// ordinary parse failures never substitute for the vulnerability. If a
// budget runs out before the search space is covered, the best candidate
// found so far is returned with ProvenMinimal set to false.
func Reduce(root *Node, validate Validate, execute Execute, budget Budget) (*Result, error) {
	if err := CheckShape(root); err != nil {
		return nil, err
	}
	if validate == nil || execute == nil {
		return nil, errors.New("taskpkg: validate and execute callbacks are required")
	}
	r := newReducer(validate, execute, budget)

	baseData, err := Encode(root)
	if err != nil {
		return nil, fmt.Errorf("taskpkg: encode original: %w", err)
	}
	if err := validate(baseData); err != nil {
		return nil, fmt.Errorf("taskpkg: original input fails validation: %w", err)
	}
	if r.execLeft == 0 {
		return nil, errors.New("taskpkg: budget too small to execute the original input")
	}
	if r.execLeft > 0 {
		r.execLeft--
	}
	r.executions++
	r.baseline = execute(baseData)
	if !r.baseline.OK || !r.baseline.OOB {
		return nil, errors.New("taskpkg: original input does not reproduce the out-of-bounds read")
	}

	best := &candidate{
		tree:  Clone(root),
		data:  baseData,
		nodes: CountNodes(root),
		bytes: len(baseData),
		ev:    r.baseline,
	}
	optionals := OptionalNodes(root)
	proven := true

search:
	for d := len(optionals); d >= 0; d-- {
		var levelBest *candidate
		levelComplete := true
		for _, subset := range combinations(optionals, d) {
			t := DropOptional(root, sliceToSet(subset))
			cand, complete := r.searchSubset(t, subset)
			if !complete {
				levelComplete = false
			}
			if cand != nil && (levelBest == nil || betterCandidate(cand, levelBest)) {
				levelBest = cand
			}
			if r.exhausted {
				levelComplete = false
				break
			}
		}
		if levelBest != nil {
			best = levelBest
			proven = levelComplete && !r.exhausted
			break
		}
		if r.exhausted {
			proven = false
			break search
		}
	}
	return &Result{
		Tree:          best.tree,
		Encoded:       best.data,
		Dropped:       best.dropped,
		NodeCount:     best.nodes,
		Bytes:         best.bytes,
		Baseline:      r.baseline,
		Evidence:      best.ev,
		ProvenMinimal: proven,
		Executions:    r.executions,
		Candidates:    r.candidates,
		Trace:         r.trace,
	}, nil
}
