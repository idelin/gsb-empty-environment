package selector

import (
	"sort"
	"strings"
)

// ChangeReason explains why a symbol entered the changed set.
type ChangeReason string

const (
	ReasonExplicit     ChangeReason = "explicit"      // given by the caller (e.g. from git diff)
	ReasonDeleted      ChangeReason = "deleted"       // present in old graph, absent in new
	ReasonAdded        ChangeReason = "added"         // present in new graph, absent in old
	ReasonEdgesChanged ChangeReason = "edges-changed" // outgoing edge targets differ old -> new
)

// Change records one symbol entering the changed set.
type Change struct {
	Symbol string       `json:"symbol"`
	Reason ChangeReason `json:"reason"`
}

// edgeSignature is a canonical fingerprint of a caller's expanded targets.
func edgeSignature(g *Graph, universe map[string]Symbol) map[string]string {
	sig := map[string][]string{}
	for _, e := range g.Edges {
		sig[e.Caller] = append(sig[e.Caller], ExpandEdge(e, universe)...)
	}
	out := map[string]string{}
	for caller, targets := range sig {
		sort.Strings(targets)
		out[caller] = strings.Join(targets, "|")
	}
	return out
}

// ChangedSymbols computes the impacted root set from the old/new graphs plus
// any explicitly changed symbols. Deleted symbols, added symbols, and symbols
// whose expanded outgoing edge set changed between graphs are all included.
func ChangedSymbols(oldG, newG *Graph, explicit []string) []Change {
	oldU := oldG.Universe()
	newU := newG.Universe()

	seen := map[string]bool{}
	var changes []Change
	add := func(id string, r ChangeReason) {
		if !seen[id] {
			seen[id] = true
			changes = append(changes, Change{Symbol: id, Reason: r})
		}
	}

	for _, id := range explicit {
		add(id, ReasonExplicit)
	}
	for id := range oldU {
		if _, ok := newU[id]; !ok {
			add(id, ReasonDeleted)
		}
	}
	for id := range newU {
		if _, ok := oldU[id]; !ok {
			add(id, ReasonAdded)
		}
	}

	oldSig := edgeSignature(oldG, oldU)
	newSig := edgeSignature(newG, newU)
	for caller, ns := range newSig {
		os, ok := oldSig[caller]
		if ok && os != ns {
			add(caller, ReasonEdgesChanged)
		}
		if !ok {
			// caller had no outgoing edges in the old graph
			if _, existed := oldU[caller]; existed {
				add(caller, ReasonEdgesChanged)
			}
		}
	}
	for caller := range oldSig {
		if _, ok := newSig[caller]; !ok {
			if _, stillThere := newU[caller]; stillThere {
				add(caller, ReasonEdgesChanged)
			}
		}
	}

	sort.Slice(changes, func(i, j int) bool { return changes[i].Symbol < changes[j].Symbol })
	return changes
}
