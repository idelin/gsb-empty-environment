package netsec

import "testing"

func indexOf(g *constraintGraph, id string) int {
	for i, n := range g.nodes {
		if n.evt.ID == id {
			return i
		}
	}
	return -1
}

// TestAnalyticMatchesBruteForce compares every analytic verdict against a
// fully independent brute-force enumeration of all tick assignments over the
// narrowed windows with every sequence edge re-checked from scratch.
func TestAnalyticMatchesBruteForce(t *testing.T) {
	events := []Event{
		evt("u1", "f", 1, KindUse, "c", 1, 4),
		evt("u2", "f", 2, KindUse, "c", 1, 4),
		evt("r1", "g", 1, KindRevoke, "c", 1, 4),
		evt("r2", "g", 2, KindRevoke, "c", 1, 4),
		evt("u3", "h", 1, KindUse, "c", 2, 3),
	}
	rep, err := Correlate(events)
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}

	g, _, err := buildConstraintGraph(events)
	if err != nil {
		t.Fatalf("buildConstraintGraph: %v", err)
	}
	order, ok := g.topoOrder()
	if !ok {
		t.Fatalf("topo order failed")
	}

	type key struct{ use, revoke string }
	brute := map[key]enumCounts{}
	ticks := make([]int64, len(g.nodes))

	edgeOK := func() bool {
		for i, n := range g.nodes {
			for _, p := range n.pred {
				if ticks[p] >= ticks[i] {
					return false
				}
			}
		}
		return true
	}

	var walk func(pos int)
	walk = func(pos int) {
		if pos == len(order) {
			if !edgeOK() {
				return
			}
			for _, p := range rep.Pairs {
				ui := indexOf(g, p.UseID)
				ri := indexOf(g, p.RevokeID)
				k := key{p.UseID, p.RevokeID}
				c := brute[k]
				switch {
				case ticks[ui] < ticks[ri]:
					c.before++
				case ticks[ui] > ticks[ri]:
					c.after++
				default:
					c.same++
				}
				brute[k] = c
			}
			return
		}
		i := order[pos]
		for tick := g.nodes[i].window.Lo; tick <= g.nodes[i].window.Hi; tick++ {
			ticks[i] = tick
			walk(pos + 1)
		}
	}
	walk(0)

	for _, p := range rep.Pairs {
		got := brute[key{p.UseID, p.RevokeID}]
		if got.before != p.EnumBefore || got.after != p.EnumAfter || got.same != p.EnumSame {
			t.Fatalf("pair %s/%s production enum (%d,%d,%d) != brute force (%d,%d,%d)",
				p.UseID, p.RevokeID,
				p.EnumBefore, p.EnumAfter, p.EnumSame,
				got.before, got.after, got.same)
		}
		want := verdictFromCounts(got)
		if want != p.Verdict {
			t.Fatalf("pair %s/%s analytic verdict %s != brute force %s",
				p.UseID, p.RevokeID, p.Verdict, want)
		}
	}
}

// TestLargerDomainBruteForce runs the same independent check on a denser
// domain including chains across three feeds to exercise transitive
// narrowing.
func TestLargerDomainBruteForce(t *testing.T) {
	events := []Event{
		evt("a1", "a", 1, KindUse, "c", 0, 3),
		evt("a2", "a", 2, KindRevoke, "c", 0, 3),
		evt("a3", "a", 3, KindUse, "c", 0, 3),
		evt("b1", "b", 1, KindRevoke, "c", 0, 3),
		evt("b2", "b", 2, KindUse, "c", 0, 3),
	}
	rep, err := Correlate(events, WithMaxEnumNodes(20))
	if err != nil {
		t.Fatalf("Correlate: %v", err)
	}
	g, _, err := buildConstraintGraph(events)
	if err != nil {
		t.Fatalf("buildConstraintGraph: %v", err)
	}
	order, ok := g.topoOrder()
	if !ok {
		t.Fatalf("topo order failed")
	}

	type key struct{ use, revoke string }
	brute := map[key]enumCounts{}
	ticks := make([]int64, len(g.nodes))
	var walk func(pos int)
	walk = func(pos int) {
		if pos == len(order) {
			for i, n := range g.nodes {
				for _, p := range n.pred {
					if ticks[p] >= ticks[i] {
						return
					}
				}
			}
			for _, p := range rep.Pairs {
				ui := indexOf(g, p.UseID)
				ri := indexOf(g, p.RevokeID)
				k := key{p.UseID, p.RevokeID}
				c := brute[k]
				switch {
				case ticks[ui] < ticks[ri]:
					c.before++
				case ticks[ui] > ticks[ri]:
					c.after++
				default:
					c.same++
				}
				brute[k] = c
			}
			return
		}
		i := order[pos]
		for tick := g.nodes[i].window.Lo; tick <= g.nodes[i].window.Hi; tick++ {
			ticks[i] = tick
			walk(pos + 1)
		}
	}
	walk(0)

	for _, p := range rep.Pairs {
		got := brute[key{p.UseID, p.RevokeID}]
		if got.before != p.EnumBefore || got.after != p.EnumAfter || got.same != p.EnumSame {
			t.Fatalf("pair %s/%s enum mismatch: production (%d,%d,%d) brute (%d,%d,%d)",
				p.UseID, p.RevokeID,
				p.EnumBefore, p.EnumAfter, p.EnumSame,
				got.before, got.after, got.same)
		}
	}
}
