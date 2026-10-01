package main

import (
	"fmt"
	"sort"
)

// Finding describes one pairwise relation between an earlier
// (higher priority) rule and a later rule.
type Finding struct {
	EarlierID int
	LaterID   int
	Kind      string // "shadowed" (later fully inside earlier) or "overlap" (partial)
	Conflict  bool   // actions differ on the shared region
	Witness   Packet // synthetic counterexample packet from the shared region
}

func (f Finding) String() string {
	conflict := ""
	if f.Conflict {
		conflict = " CONFLICT(actions differ)"
	}
	return fmt.Sprintf("rule #%d vs earlier rule #%d: %s%s; witness %s",
		f.LaterID, f.EarlierID, f.Kind, conflict, f.Witness)
}

// UnreachableRule is a later rule fully covered by the union of
// earlier rules: no packet can ever reach it.
type UnreachableRule struct {
	RuleID  int
	Witness Packet // synthetic packet that matches the dead rule but is
	// captured by an earlier rule
}

// Report is the result of Analyze.
type Report struct {
	Unreachable []UnreachableRule
	Findings    []Finding
}

// sortedRules orders rules by priority (ascending), then ID.
func sortedRules(rules []Rule) []Rule {
	out := make([]Rule, len(rules))
	copy(out, rules)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Analyze checks the ruleset for shadowing and conflicts.
//
//   - A later rule fully covered by the union of earlier rules is
//     unreachable and reported in Unreachable.
//   - A later rule partially covered by an earlier rule is only
//     reported as an overlap Finding (it stays reachable).
//   - Any overlap between rules with different actions is a conflict.
//   - IPv4 and IPv6 rules live in disjoint spaces and never interact.
//
// Every finding carries a synthetic witness packet as a counterexample.
func Analyze(rules []Rule) Report {
	rs := sortedRules(rules)
	rects := make([]rect, len(rs))
	for i, r := range rs {
		rects[i] = ruleRect(r)
	}
	var rep Report
	var earlier []rect
	witnessID := 0
	nextWitness := func(r rect) Packet {
		witnessID++
		return Packet{ID: witnessID, Dst: r.lo, Proto: r.pLo, DstPort: r.dLo}
	}
	for j := range rs {
		if len(earlier) > 0 && len(remainder(rects[j], earlier)) == 0 {
			rep.Unreachable = append(rep.Unreachable, UnreachableRule{
				RuleID:  rs[j].ID,
				Witness: nextWitness(rects[j]),
			})
		}
		for i := 0; i < j; i++ {
			inter, ok := intersect(rects[i], rects[j])
			if !ok {
				continue
			}
			kind := "overlap"
			if len(subtract(rects[j], rects[i])) == 0 {
				kind = "shadowed"
			}
			rep.Findings = append(rep.Findings, Finding{
				EarlierID: rs[i].ID,
				LaterID:   rs[j].ID,
				Kind:      kind,
				Conflict:  rs[i].Action != rs[j].Action,
				Witness:   nextWitness(inter),
			})
		}
		earlier = append(earlier, rects[j])
	}
	return rep
}
