package netsec

import (
	"fmt"
	"strings"
)

// MaxHandles bounds the number of inherited handles Analyze accepts.
// The bound keeps exhaustive re-verification of close-set candidates
// cheap for the launch-plan host.
const MaxHandles = 18

// CwdHandleID is the reserved handle ID of the implicit
// working-directory handle that every process holds.
const CwdHandleID = -1

// HandleKind distinguishes inherited file descriptors from the
// implicit working-directory handle.
type HandleKind int

const (
	// HandleInherited is a directory file descriptor inherited
	// across the root switch.
	HandleInherited HandleKind = iota
	// HandleCwd is the implicit working-directory handle. Closing
	// it means repointing the working directory into the new root
	// before the process is considered confined.
	HandleCwd
)

func (k HandleKind) String() string {
	switch k {
	case HandleInherited:
		return "inherited"
	case HandleCwd:
		return "cwd"
	default:
		return fmt.Sprintf("HandleKind(%d)", int(k))
	}
}

// Handle is an inherited directory handle: an open file descriptor
// referring to a directory object in the graph.
type Handle struct {
	// ID is the host-assigned handle identifier (e.g. the fd
	// number). IDs must be unique and must not equal CwdHandleID.
	ID int
	// Object is the directory object the handle refers to.
	Object ObjectID
}

// Input bundles everything the launch-plan host knows before exec:
// the directory object graph, the root the process is switching to,
// its working directory, and the handles it inherits.
type Input struct {
	Graph   *Graph
	Root    ObjectID
	Cwd     ObjectID
	Handles []Handle
}

// HandleReport describes the capability closure of a single handle.
type HandleReport struct {
	Handle Handle
	Kind   HandleKind
	// Capped is true when parent ("..") traversal from this handle
	// stops at the current root. Handles starting outside the root
	// are not capped.
	Capped bool
	// Reachable is the sorted set of objects reachable from the
	// handle via child and (possibly capped) parent traversal.
	Reachable []ObjectID
	// Escapes is true when Reachable contains objects outside the
	// current root.
	Escapes bool
	// Witness is a path of objects from the handle's object to the
	// first reachable object outside the root. Nil when the handle
	// cannot escape.
	Witness []ObjectID
}

// Report is the result of Analyze.
type Report struct {
	Root ObjectID
	Cwd  ObjectID
	// Handles holds one report per analyzed handle. The implicit
	// working-directory handle (ID CwdHandleID) comes first.
	Handles []HandleReport
	// CloseSet is the minimal set of handle IDs that must be closed
	// (for HandleCwd: repointed into the root) to block every
	// reachable path outside the root.
	CloseSet []int
	// CwdEscapes is true when the working-directory handle itself
	// reaches outside the root.
	CwdEscapes bool
	// Verified is true once the close set has been re-verified for
	// sufficiency and minimality.
	Verified bool
}

// Safe reports whether no handle can escape the root, i.e. nothing
// needs to be closed.
func (r *Report) Safe() bool { return len(r.CloseSet) == 0 }

// Analyze computes the capability closure of every handle and the
// minimal close set, then verifies that the close set blocks all
// root-external reachable paths and that no smaller set does.
func Analyze(in *Input) (*Report, error) {
	if in == nil || in.Graph == nil {
		return nil, fmt.Errorf("netsec: input and input graph must not be nil")
	}
	g := in.Graph
	if !g.Valid(in.Root) {
		return nil, fmt.Errorf("netsec: root object %d out of range", in.Root)
	}
	if !g.Valid(in.Cwd) {
		return nil, fmt.Errorf("netsec: cwd object %d out of range", in.Cwd)
	}
	if len(in.Handles) > MaxHandles {
		return nil, fmt.Errorf("netsec: %d inherited handles exceeds MaxHandles=%d", len(in.Handles), MaxHandles)
	}
	seen := map[int]bool{CwdHandleID: true}
	for _, h := range in.Handles {
		if seen[h.ID] {
			return nil, fmt.Errorf("netsec: duplicate or reserved handle ID %d", h.ID)
		}
		seen[h.ID] = true
		if !g.Valid(h.Object) {
			return nil, fmt.Errorf("netsec: handle %d: object %d out of range", h.ID, h.Object)
		}
	}

	inside := g.insideSet(in.Root)

	rep := &Report{Root: in.Root, Cwd: in.Cwd}
	jobs := make([]HandleReport, 0, len(in.Handles)+1)
	jobs = append(jobs, analyzeHandle(g, Handle{ID: CwdHandleID, Object: in.Cwd}, HandleCwd, in.Root, inside))
	for _, h := range in.Handles {
		jobs = append(jobs, analyzeHandle(g, h, HandleInherited, in.Root, inside))
	}
	for _, jr := range jobs {
		if jr.Escapes {
			rep.CloseSet = append(rep.CloseSet, jr.Handle.ID)
			if jr.Kind == HandleCwd {
				rep.CwdEscapes = true
			}
		}
		rep.Handles = append(rep.Handles, jr)
	}

	if err := rep.verify(in, inside); err != nil {
		return nil, err
	}
	rep.Verified = true
	return rep, nil
}

// analyzeHandle computes the capability closure of one handle.
func analyzeHandle(g *Graph, h Handle, kind HandleKind, root ObjectID, inside []bool) HandleReport {
	capped := inside[h.Object]
	reach, witness := g.reachable(h.Object, root, capped, inside)
	return HandleReport{
		Handle:    h,
		Kind:      kind,
		Capped:    capped,
		Reachable: reach,
		Escapes:   witness != nil,
		Witness:   witness,
	}
}

// reachable performs a breadth-first traversal from start over child
// edges and parent edges. When capped is true the parent edge of the
// root object is suppressed (".." stops at the current root); the
// parent edge of a global root is always a self-loop. It returns the
// sorted reachable set and, if any reachable object lies outside the
// root, a witness path from start to the first such object.
func (g *Graph) reachable(start, root ObjectID, capped bool, inside []bool) ([]ObjectID, []ObjectID) {
	n := len(g.parent)
	visited := make([]bool, n)
	prev := make([]ObjectID, n)
	for i := range prev {
		prev[i] = InvalidObject
	}
	visited[start] = true
	queue := []ObjectID{start}
	escape := InvalidObject
	for len(queue) > 0 {
		o := queue[0]
		queue = queue[1:]
		if escape == InvalidObject && !inside[o] {
			escape = o
		}
		for _, c := range g.children[o] {
			if !visited[c] {
				visited[c] = true
				prev[c] = o
				queue = append(queue, c)
			}
		}
		p := g.parent[o]
		if capped && o == root {
			p = o
		}
		if !visited[p] {
			visited[p] = true
			prev[p] = o
			queue = append(queue, p)
		}
	}
	reach := make([]ObjectID, 0, n)
	for o := ObjectID(0); o < ObjectID(n); o++ {
		if visited[o] {
			reach = append(reach, o)
		}
	}
	if escape == InvalidObject {
		return reach, nil
	}
	var path []ObjectID
	for o := escape; o != InvalidObject; o = prev[o] {
		path = append(path, o)
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return reach, path
}

// verify re-checks the report's close set against fresh traversals:
//
//  1. Sufficiency: with exactly CloseSet closed (the cwd handle
//     repointed into the root), no remaining handle reaches any object
//     outside the root.
//  2. Minimality: every handle in CloseSet is necessary; removing any
//     one of them re-opens at least one escape path.
func (r *Report) verify(in *Input, inside []bool) error {
	closed := make(map[int]bool, len(r.CloseSet))
	for _, id := range r.CloseSet {
		closed[id] = true
	}
	if esc := escapingHandles(in, inside, closed); len(esc) != 0 {
		return fmt.Errorf("netsec: close set %v does not block all escapes; still escaping: %v", r.CloseSet, esc)
	}
	for _, id := range r.CloseSet {
		trial := make(map[int]bool, len(closed)-1)
		for k := range closed {
			if k != id {
				trial[k] = true
			}
		}
		if esc := escapingHandles(in, inside, trial); len(esc) == 0 {
			return fmt.Errorf("netsec: handle %d is not necessary in the minimal close set", id)
		}
	}
	return nil
}

// escapingHandles returns the IDs of handles that can reach outside
// the root, treating the given handle IDs as closed. A closed cwd
// handle is treated as repointed to the root, which cannot escape.
func escapingHandles(in *Input, inside []bool, closed map[int]bool) []int {
	var esc []int
	if !closed[CwdHandleID] && reachesOutside(in.Graph, in.Cwd, in.Root, inside) {
		esc = append(esc, CwdHandleID)
	}
	for _, h := range in.Handles {
		if closed[h.ID] {
			continue
		}
		if reachesOutside(in.Graph, h.Object, in.Root, inside) {
			esc = append(esc, h.ID)
		}
	}
	return esc
}

// reachesOutside re-runs a full traversal for one handle and reports
// whether any reachable object lies outside the root.
func reachesOutside(g *Graph, start, root ObjectID, inside []bool) bool {
	_, witness := g.reachable(start, root, inside[start], inside)
	return witness != nil
}

// DescribePath renders a witness path for logging, annotating parent
// hops with "..".
func (g *Graph) DescribePath(path []ObjectID) string {
	if len(path) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(g.Name(path[0]))
	for i := 1; i < len(path); i++ {
		if g.parent[path[i-1]] == path[i] {
			b.WriteString(" --..--> ")
		} else {
			b.WriteString(" --> ")
		}
		b.WriteString(g.Name(path[i]))
	}
	return b.String()
}
