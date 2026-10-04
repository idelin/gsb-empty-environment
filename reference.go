package annmerge

import (
	"fmt"
	"sort"
)

// ReferenceEval is a deliberately naive per-character reference
// evaluator. It exists to cross-check Engine: it keeps flat maps instead
// of a tree, derives the final anchor sequence by sorting root-to-node
// ID paths, and resolves every character's attributes independently
// (no run merging, no shared state between characters). It must agree
// with Engine.CharAttrs for every delivery order of the same operations.
func ReferenceEval(ops []Op) ([]CharAttr, error) {
	parent := map[string]string{RootID: "", EndID: ""}
	charOf := map[string]rune{}
	deleted := map[string]bool{}
	type ann struct {
		left, right         string
		leftIncl, rightIncl bool
		attrs               map[string]string
		time                uint64
		opID                string
		undone              bool
	}
	anns := map[string]*ann{}

	apply := func(op Op) bool {
		switch op.Kind {
		case KindInsert:
			if _, dup := parent[op.ID]; dup {
				return true
			}
			if _, ok := parent[op.Parent]; !ok {
				return false
			}
			parent[op.ID] = op.Parent
			charOf[op.ID] = op.Char
			return true
		case KindDelete:
			if _, ok := parent[op.Target]; !ok || op.Target == RootID || op.Target == EndID {
				return false
			}
			deleted[op.Target] = true
			return true
		case KindAnnotate:
			if _, dup := anns[op.AnnID]; dup {
				return true
			}
			if _, ok := parent[op.Left]; !ok {
				return false
			}
			if _, ok := parent[op.Right]; !ok {
				return false
			}
			anns[op.AnnID] = &ann{
				left: op.Left, right: op.Right,
				leftIncl: op.LeftIncl, rightIncl: op.RightIncl,
				attrs: op.Attrs, time: op.Time, opID: op.ID,
			}
			return true
		case KindUndo:
			a, ok := anns[op.Undo]
			if !ok {
				return false
			}
			a.undone = true
			return true
		}
		return false
	}

	pending := append([]Op(nil), ops...)
	for len(pending) > 0 {
		next := make([]Op, 0, len(pending))
		progress := false
		for _, op := range pending {
			if apply(op) {
				progress = true
			} else {
				next = append(next, op)
			}
		}
		if !progress {
			return nil, fmt.Errorf("annmerge: reference evaluator stuck on %d ops", len(next))
		}
		pending = next
	}

	// Final anchor sequence: sort every node by its root-to-node ID path,
	// comparing IDs descending at the first difference (matching the
	// same-predecessor ordering rule), shorter (ancestor) path first.
	// EndID is pinned to the tail.
	ids := make([]string, 0, len(parent))
	for id := range parent {
		if id != EndID {
			ids = append(ids, id)
		}
	}
	paths := map[string][]string{}
	var pathOf func(id string) []string
	pathOf = func(id string) []string {
		if p, ok := paths[id]; ok {
			return p
		}
		var p []string
		if id == RootID {
			p = []string{RootID}
		} else {
			p = append(append([]string{}, pathOf(parent[id])...), id)
		}
		paths[id] = p
		return p
	}
	sort.Slice(ids, func(i, j int) bool {
		pi, pj := pathOf(ids[i]), pathOf(ids[j])
		for k := 0; k < len(pi) && k < len(pj); k++ {
			if pi[k] != pj[k] {
				return pi[k] > pj[k]
			}
		}
		return len(pi) < len(pj)
	})
	ids = append(ids, EndID)

	pos := make(map[string]int, len(ids))
	for i, id := range ids {
		pos[id] = i
	}

	var out []CharAttr
	for i, id := range ids {
		if id == RootID || id == EndID || deleted[id] {
			continue
		}
		attrs := map[string]string{}
		bestTime := map[string]uint64{}
		bestOp := map[string]string{}
		for _, a := range anns {
			if a.undone {
				continue
			}
			li, ri := pos[a.left], pos[a.right]
			if !(i > li || (a.leftIncl && i == li)) {
				continue
			}
			if !(i < ri || (a.rightIncl && i == ri)) {
				continue
			}
			for k, v := range a.attrs {
				curT, ok := bestTime[k]
				curOp := bestOp[k]
				if !ok || a.time > curT || (a.time == curT && a.opID > curOp) {
					bestTime[k] = a.time
					bestOp[k] = a.opID
					attrs[k] = v
				}
			}
		}
		out = append(out, CharAttr{ID: id, Char: charOf[id], Attrs: attrs})
	}
	return out, nil
}
