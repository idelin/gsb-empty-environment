package verenum

import (
	"fmt"
	"sort"
	"strings"
)

// ConflictError is a same-version registration conflict.
type ConflictError struct {
	Path  string
	Name  string
	Cause string
}

func (e *ConflictError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("verenum: conflicting name %q at field %s: %s", e.Name, e.Path, e.Cause)
	}
	return fmt.Sprintf("verenum: conflicting name %q: %s", e.Name, e.Cause)
}

// MergeError reports that merging graphs would force one active name onto
// two different stable IDs.
type MergeError struct {
	Name     string
	Bindings []ChainStep
}

func (e *MergeError) Error() string {
	parts := make([]string, len(e.Bindings))
	for i, s := range e.Bindings {
		parts[i] = s.String()
	}
	return fmt.Sprintf("verenum: cannot merge graphs: active name %q maps to distinct IDs: %s",
		e.Name, strings.Join(parts, ", "))
}

// AmbiguityError is returned by migration when a name reaches more than one
// stable ID in the merged version graph. It carries the shortest conflict
// chain per candidate ID and the original field path supplied by the caller.
type AmbiguityError struct {
	Path   string
	Name   string
	Chains map[int64][]ChainStep
}

func (e *AmbiguityError) Error() string {
	where := fmt.Sprintf("%q", e.Name)
	if e.Path != "" {
		where = fmt.Sprintf("%q at field %s", e.Name, e.Path)
	}
	return fmt.Sprintf("verenum: ambiguous name %s: refusing to pick one candidate:%s",
		where, renderCandidates(e.Name, e.Chains))
}

// UnknownNameError means the lexical name is not declared in any involved
// version; callers should pass it through unchanged.
type UnknownNameError struct {
	Name string
	From Version
	To   Version
}

func (e *UnknownNameError) Error() string {
	return fmt.Sprintf("verenum: name %q is unknown between v%d and v%d", e.Name, e.From, e.To)
}

// PathConflictError is returned by VerifyPath: a name maps to different IDs
// along the minor-version path, with the shortest conflict chain attached.
type PathConflictError struct {
	Path   string
	Name   string
	Chains map[int64][]ChainStep
}

func (e *PathConflictError) Error() string {
	return fmt.Sprintf("verenum: migration path inconsistent for field %s, name %q:%s",
		e.Path, e.Name, renderCandidates(e.Name, e.Chains))
}

func renderCandidates(start string, chains map[int64][]ChainStep) string {
	ids := make([]int64, 0, len(chains))
	for id := range chains {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var b strings.Builder
	for _, id := range ids {
		fmt.Fprintf(&b, "\n  id=%d: %s", id, renderChain(start, chains[id]))
	}
	return b.String()
}

// renderChain renders a sequence of hops; alias step i points at step i+1's
// name, and the final active step carries the canonical terminal.
func renderChain(start string, chain []ChainStep) string {
	if len(chain) == 0 {
		return fmt.Sprintf("%q -> <unreachable>", start)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%q", start)
	for i, step := range chain {
		switch step.Rel {
		case RelActive:
			fmt.Fprintf(&b, " -(v%d active)-> %q[id=%d]", step.Version, step.Name, step.ID)
		default:
			target := "<unreachable>"
			if i+1 < len(chain) {
				target = fmt.Sprintf("%q", chain[i+1].Name)
			}
			fmt.Fprintf(&b, " -(v%d alias)-> %s", step.Version, target)
		}
	}
	return b.String()
}
