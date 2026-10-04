// Package cellengine implements an incremental spreadsheet cell engine.
//
// Cells hold formulas over integer constants, cell references, + - *,
// IF(cond, then, else) and the error literal #ERR!. Only the branch of an
// IF that is actually evaluated produces dependencies, so a cycle that
// lives solely in an unselected branch is not an error.
//
// Mutations are applied in batches; each batch creates a new version in
// which all of the batch's changes are visible together. After a batch the
// engine updates its dependency graph and recomputes only the affected
// cells, reporting exactly which cells were recomputed. Old versions stay
// available for read-only queries.
package cellengine

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Op is a single mutation inside a batch. Exactly one of Del/Formula is
// meaningful: Del removes the cell, otherwise the cell is set to Formula
// (a leading '=' is optional).
type Op struct {
	ID      string
	Del     bool
	Formula string
}

// SetOp builds an Op that assigns a formula to a cell.
func SetOp(id, formula string) Op { return Op{ID: id, Formula: formula} }

// DelOp builds an Op that deletes a cell.
func DelOp(id string) Op { return Op{ID: id, Del: true} }

// Version is an immutable snapshot of the sheet after one batch.
type Version struct {
	N          int
	Cells      map[string]string // cell ID -> formula source
	Values     map[string]Value  // cell ID -> value in this version
	Recomputed []string          // cells actually recomputed by this batch, sorted
}

// Engine holds the current sheet plus every historical version.
type Engine struct {
	cells    map[string]string          // current formulas
	deps     map[string]map[string]bool // cell -> cells it actually read
	rev      map[string]map[string]bool // cell -> cells that read it
	cache    map[string]Value           // values valid for the current version
	asts     map[string]parseResult     // formula source -> parsed AST
	version  int
	versions []*Version

	recomputed map[string]bool // cells evaluated during the in-flight batch
}

type parseResult struct {
	ast expr
	err error
}

// New returns an empty engine at version 0.
func New() *Engine {
	return &Engine{
		cells: make(map[string]string),
		deps:  make(map[string]map[string]bool),
		rev:   make(map[string]map[string]bool),
		cache: make(map[string]Value),
		asts:  make(map[string]parseResult),
	}
}

var cellIDRe = regexp.MustCompile(`^[A-Za-z]+[0-9]+$`)

// NormalizeID validates and canonicalizes a cell ID (upper case).
func NormalizeID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if !cellIDRe.MatchString(id) {
		return "", fmt.Errorf("invalid cell ID %q", id)
	}
	return strings.ToUpper(id), nil
}

// Version returns the current version number (0 before any batch).
func (e *Engine) Version() int { return e.version }

// Apply applies a batch of ops atomically as one new version, updates the
// dependency graph, recomputes the affected cells and returns the sorted
// IDs of the cells that were actually recomputed.
func (e *Engine) Apply(ops ...Op) ([]string, error) {
	norm := make([]Op, 0, len(ops))
	for _, op := range ops {
		id, err := NormalizeID(op.ID)
		if err != nil {
			return nil, err
		}
		op.ID = id
		norm = append(norm, op)
	}

	e.version++
	changed := make(map[string]bool)
	for _, op := range norm {
		changed[op.ID] = true
		if op.Del {
			delete(e.cells, op.ID)
		} else {
			f := strings.TrimSpace(op.Formula)
			f = strings.TrimPrefix(f, "=")
			e.cells[op.ID] = f
		}
	}

	// Dirty set: changed cells plus everything that transitively read them
	// through the *previous* graph. Cells whose reads change only because a
	// flag flipped are reachable through the flag's old edge, so this set
	// covers every cell whose value can differ in the new version.
	dirty := make(map[string]bool)
	queue := make([]string, 0, len(changed))
	for id := range changed {
		dirty[id] = true
		queue = append(queue, id)
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for parent := range e.rev[id] {
			if !dirty[parent] {
				dirty[parent] = true
				queue = append(queue, parent)
			}
		}
	}

	// Invalidate only the dirty entries; every other cached value stays
	// valid for the new version because nothing it depends on changed.
	for id := range dirty {
		delete(e.cache, id)
	}
	// Deleted cells lose their graph data entirely.
	for id := range changed {
		if _, ok := e.cells[id]; !ok {
			delete(e.deps, id)
			delete(e.cache, id)
		}
	}

	// Recompute the dirty cells that still exist. Evaluation pulls fresh
	// values of any dirty dependencies on demand and memoizes within the
	// version, so shared subgraphs are evaluated once.
	e.recomputed = make(map[string]bool)
	cx := &evalCtx{e: e, onStack: make(map[string]bool)}
	ids := make([]string, 0, len(dirty))
	for id := range dirty {
		if _, ok := e.cells[id]; ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		cx.evalCell(id)
	}

	// Rebuild the reverse graph from the updated forward edges.
	e.rev = make(map[string]map[string]bool)
	for from, reads := range e.deps {
		for to := range reads {
			if e.rev[to] == nil {
				e.rev[to] = make(map[string]bool)
			}
			e.rev[to][from] = true
		}
	}

	// Snapshot the new version for read-only historical queries.
	vals := make(map[string]Value, len(e.cells))
	for id := range e.cells {
		vals[id] = e.cache[id]
	}
	snap := &Version{
		N:          e.version,
		Cells:      cloneCells(e.cells),
		Values:     vals,
		Recomputed: sortedKeys(e.recomputed),
	}
	e.versions = append(e.versions, snap)
	return snap.Recomputed, nil
}

func cloneCells(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Get returns the value of a cell in the current version. A cell that does
// not exist yields a #REF! error value.
func (e *Engine) Get(id string) Value { return e.GetAt(e.version, id) }

// GetAt returns the value of a cell in a historical version (1-based).
// Versions are read-only snapshots; results never change once committed.
func (e *Engine) GetAt(version int, id string) Value {
	id = strings.ToUpper(strings.TrimSpace(id))
	if version < 0 || version > e.version {
		return errVal(fmt.Sprintf("#VER!(%d)", version))
	}
	if version == 0 {
		return errRef(id)
	}
	snap := e.versions[version-1]
	if v, ok := snap.Values[id]; ok {
		return v
	}
	return errRef(id)
}

// Snapshot returns the immutable snapshot of a historical version.
func (e *Engine) Snapshot(version int) (*Version, bool) {
	if version < 1 || version > e.version {
		return nil, false
	}
	return e.versions[version-1], true
}

// Deps returns the cells the given cell actually read when it was last
// evaluated in the current version (nil if the cell does not exist).
func (e *Engine) Deps(id string) []string {
	reads, ok := e.deps[id]
	if !ok {
		return nil
	}
	return sortedKeys(reads)
}

func (e *Engine) parse(f string) parseResult {
	if r, ok := e.asts[f]; ok {
		return r
	}
	ast, err := Parse(f)
	r := parseResult{ast: ast, err: err}
	e.asts[f] = r
	return r
}

// evalCtx carries the per-batch evaluation state: the recursion stack used
// for cycle detection and the per-cell read sets used to maintain edges.
type evalCtx struct {
	e       *Engine
	stack   []string
	onStack map[string]bool
	frames  []*evalFrame
}

type evalFrame struct{ reads map[string]bool }

func (cx *evalCtx) evalCell(id string) Value {
	e := cx.e
	if v, ok := e.cache[id]; ok {
		return v
	}
	if cx.onStack[id] {
		// The cell is already being evaluated up-stack: a real cycle on an
		// executed path. Report it with the concrete cell IDs; the error is
		// cached by each stack frame as it unwinds.
		start := 0
		for i, s := range cx.stack {
			if s == id {
				start = i
				break
			}
		}
		return errCycle(append([]string{}, cx.stack[start:]...))
	}
	f, ok := e.cells[id]
	if !ok {
		v := errRef(id)
		e.cache[id] = v
		return v
	}
	cx.onStack[id] = true
	cx.stack = append(cx.stack, id)
	fr := &evalFrame{reads: make(map[string]bool)}
	cx.frames = append(cx.frames, fr)

	var v Value
	pr := e.parse(f)
	if pr.err != nil {
		v = errParse(pr.err)
	} else {
		v = cx.evalExpr(pr.ast, fr)
	}

	cx.frames = cx.frames[:len(cx.frames)-1]
	cx.stack = cx.stack[:len(cx.stack)-1]
	delete(cx.onStack, id)
	e.cache[id] = v
	e.deps[id] = fr.reads
	e.recomputed[id] = true
	return v
}

func (cx *evalCtx) evalExpr(ex expr, fr *evalFrame) Value {
	switch n := ex.(type) {
	case numLit:
		return NumVal(n.v)
	case errExpr:
		return errLiteral()
	case refExpr:
		fr.reads[n.id] = true
		return cx.evalCell(n.id)
	case binExpr:
		l := cx.evalExpr(n.l, fr)
		if l.IsErr {
			return l
		}
		r := cx.evalExpr(n.r, fr)
		if r.IsErr {
			return r
		}
		switch n.op {
		case '+':
			return NumVal(l.Num + r.Num)
		case '-':
			return NumVal(l.Num - r.Num)
		case '*':
			return NumVal(l.Num * r.Num)
		}
		return errVal("#OP!")
	case ifExpr:
		c := cx.evalExpr(n.c, fr)
		if c.IsErr {
			return c
		}
		if c.Num != 0 {
			return cx.evalExpr(n.t, fr)
		}
		return cx.evalExpr(n.f, fr)
	}
	return errVal("#AST!")
}
