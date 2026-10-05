package planner

import (
	"fmt"
	"strings"
)

// Reason explains why a node was rebuilt. Via points to the upstream reason,
// forming a chain that ends at a root cause (Via == nil), e.g. a source file
// that changed on disk.
type Reason struct {
	Node  string
	Cause string
	Via   *Reason
}

// Depth is the length of the reason chain.
func (r *Reason) Depth() int {
	depth := 0
	for cur := r; cur != nil; cur = cur.Via {
		depth++
	}
	return depth
}

// Chain returns the chain root-cause-first.
func (r *Reason) Chain() []string {
	var rev []string
	for cur := r; cur != nil; cur = cur.Via {
		rev = append(rev, fmt.Sprintf("%s: %s", cur.Node, cur.Cause))
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

func (r *Reason) String() string {
	if r == nil {
		return ""
	}
	if r.Via == nil {
		return fmt.Sprintf("%s: %s", r.Node, r.Cause)
	}
	return fmt.Sprintf("%s: %s <- %s", r.Node, r.Cause, r.Via)
}

// Result summarizes one Build call.
type Result struct {
	// Rebuilt lists tasks that committed fresh results, in execution order.
	Rebuilt []string
	// Reasons maps each rebuilt task to its shortest invalidation chain.
	Reasons map[string]*Reason
	// Notes maps tasks to the reasons their discarded attempts were rejected
	// (stale mid-run reads, stale discovered dependencies).
	Notes map[string][]*Reason
}

// Report renders the shortest reason chain of every rebuilt node.
func (r *Result) Report() string {
	if len(r.Rebuilt) == 0 {
		return "up to date\n"
	}
	var b strings.Builder
	for _, name := range r.Rebuilt {
		fmt.Fprintf(&b, "rebuilt %s\n  reason: %s\n", name, r.Reasons[name])
		for _, n := range r.Notes[name] {
			fmt.Fprintf(&b, "  discarded attempt: %s\n", n)
		}
	}
	return b.String()
}
