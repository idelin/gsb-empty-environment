package planner

import "fmt"

// Trace records how an expression evaluated against a collected evidence set.
type Trace struct {
	Node      *Node
	Satisfied bool
	Detail    string // leaf-level explanation
	Children  []*Trace
}

// Evaluate checks the expression against the collected evidence set.
// AND requires every child satisfied; OR requires at least one.
// The trace preserves the full tree so callers can report the exact
// satisfaction path (or the failing branches) without confusing
// "any satisfied" with "all satisfied".
func Evaluate(n *Node, collected map[string]bool, inst *Instance) *Trace {
	tr := &Trace{Node: n}
	switch n.Kind {
	case KindEvidence:
		if collected[n.ID] {
			tr.Satisfied = true
			tr.Detail = "collected"
		} else {
			info := inst.Evidence[n.ID]
			switch {
			case info == nil:
				tr.Detail = "unknown evidence"
			case info.HasFrom && inst.AsOf.Before(info.ValidFrom):
				tr.Detail = "not collected (not yet valid)"
			case info.HasUntil && inst.AsOf.After(info.ValidUntil):
				tr.Detail = "not collected (expired evidence is never collected)"
			default:
				tr.Detail = "not collected"
			}
		}
	case KindNot:
		inner := Evaluate(n.Children[0], collected, inst)
		tr.Children = []*Trace{inner}
		tr.Satisfied = !inner.Satisfied
	case KindAnd:
		all := true
		for _, ch := range n.Children {
			ct := Evaluate(ch, collected, inst)
			tr.Children = append(tr.Children, ct)
			if !ct.Satisfied {
				all = false
			}
		}
		tr.Satisfied = all
	case KindOr:
		any := false
		for _, ch := range n.Children {
			ct := Evaluate(ch, collected, inst)
			tr.Children = append(tr.Children, ct)
			if ct.Satisfied {
				any = true
			}
		}
		tr.Satisfied = any
	}
	return tr
}

// Render formats the trace as an indented tree with check marks.
func (tr *Trace) Render() string {
	var buf []string
	renderTrace(tr, "", true, &buf)
	out := ""
	for i, line := range buf {
		if i > 0 {
			out += "\n"
		}
		out += line
	}
	return out
}

func renderTrace(tr *Trace, prefix string, last bool, lines *[]string) {
	mark := "✗"
	if tr.Satisfied {
		mark = "✓"
	}
	label := ""
	switch tr.Node.Kind {
	case KindEvidence:
		label = tr.Node.ID
		if tr.Detail != "" {
			label += " (" + tr.Detail + ")"
		}
	case KindAnd:
		label = fmt.Sprintf("AND (all %d required)", len(tr.Node.Children))
	case KindOr:
		label = fmt.Sprintf("OR (any of %d)", len(tr.Node.Children))
	case KindNot:
		label = "NOT"
	}
	connector := "└─ "
	childPrefix := "    "
	if !last {
		connector = "├─ "
		childPrefix = "│   "
	}
	*lines = append(*lines, prefix+connector+mark+" "+label)
	for i, ch := range tr.Children {
		renderTrace(ch, prefix+childPrefix, i == len(tr.Children)-1, lines)
	}
}
