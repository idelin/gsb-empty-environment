package merge

import (
	"fmt"
	"strings"
)

// Reason classifies why a path does or does not exist in the merged
// configuration.
type Reason int

const (
	// ReasonExists: the path is present in the merged configuration.
	ReasonExists Reason = iota
	// ReasonNeverSet: no layer ever touched the path.
	ReasonNeverSet
	// ReasonDeleted: the key (or an ancestor of it) was removed by an
	// explicit Delete.
	ReasonDeleted
	// ReasonDroppedByParent: an ancestor was replaced by a leaf value
	// (scalar, array or null), discarding the subtree below it.
	ReasonDroppedByParent
	// ReasonParentIsLeaf: an ancestor is currently a leaf
	// (scalar, array or null), so the path cannot exist below it.
	ReasonParentIsLeaf
)

// Explanation describes the fate of one configuration path.
type Explanation struct {
	Path   string
	Exists bool
	Reason Reason

	// Set when the path exists as a leaf.
	Leaf *Leaf
	// Set when the path exists as an object: number of leaves below it.
	ObjectLeaves int

	// Set for ReasonDeleted / ReasonDroppedByParent /
	// ReasonParentIsLeaf: the layer and ancestor path responsible.
	ByLayer int
	ByPath  string
}

// String renders a human-readable explanation.
func (e Explanation) String() string {
	switch {
	case e.Exists && e.Leaf != nil:
		return fmt.Sprintf("%s = %v (from layer %d, %d overridden)",
			e.Path, e.Leaf.Value, e.Leaf.Layer, len(e.Leaf.History))
	case e.Exists:
		return fmt.Sprintf("%s is an object with %d leaf value(s)", e.Path, e.ObjectLeaves)
	}
	switch e.Reason {
	case ReasonNeverSet:
		return fmt.Sprintf("%s was never set by any layer", e.Path)
	case ReasonDeleted:
		if e.ByPath != e.Path {
			return fmt.Sprintf("%s was removed when %s was deleted at layer %d",
				e.Path, e.ByPath, e.ByLayer)
		}
		return fmt.Sprintf("%s was deleted at layer %d", e.Path, e.ByLayer)
	case ReasonDroppedByParent:
		return fmt.Sprintf("%s was discarded when %s was replaced by a leaf value at layer %d",
			e.Path, e.ByPath, e.ByLayer)
	case ReasonParentIsLeaf:
		return fmt.Sprintf("%s cannot exist: %s is a leaf value", e.Path, e.ByPath)
	}
	return fmt.Sprintf("%s does not exist", e.Path)
}

// Explain accounts for any configuration path: its final value and
// provenance when it exists, or why it is absent when it does not.
func (r *Result) Explain(path string) Explanation {
	exp := Explanation{Path: path}

	if leaf, ok := r.Leaf(path); ok {
		exp.Exists = true
		exp.Leaf = &leaf
		return exp
	}

	// An object exists at path if any live leaf sits below it.
	prefix := path + "."
	count := 0
	for p := range r.leaves {
		if strings.HasPrefix(p, prefix) {
			count++
		}
	}
	if count > 0 {
		exp.Exists = true
		exp.ObjectLeaves = count
		return exp
	}

	// Consult the removal archive for this exact path: it records the
	// event that actually eliminated the path.
	if rems := r.removed[path]; len(rems) > 0 {
		last := rems[len(rems)-1]
		exp.ByLayer = last.Layer
		exp.ByPath = last.Cause
		switch last.Op {
		case OpDelete:
			exp.Reason = ReasonDeleted
		case OpDroppedByParent:
			exp.Reason = ReasonDroppedByParent
		default:
			exp.Reason = ReasonNeverSet
		}
		return exp
	}

	// A live ancestor leaf blocks the path outright.
	for _, anc := range ancestors(path) {
		if _, ok := r.leaves[anc]; ok {
			exp.Reason = ReasonParentIsLeaf
			exp.ByPath = anc
			exp.ByLayer = r.leaves[anc].layer
			return exp
		}
	}

	exp.Reason = ReasonNeverSet
	return exp
}
