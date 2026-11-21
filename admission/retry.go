package admission

import "fmt"

func (a *Analyzer) validateRetries() error {
	incomingRetry := map[string]bool{}
	fixedVars := map[string]string{} // variable name -> node ID

	// Fixed-draw uniqueness: one fixed random quantity has exactly one node,
	// so no retry layer can ever draw it again.
	for _, id := range a.order {
		n := a.nodes[id]
		if n.Kind == KindDraw && n.Draw.FixedVariable != "" {
			if owner := fixedVars[n.Draw.FixedVariable]; owner != "" {
				return fmt.Errorf(
					"admission: fixed variable %q is drawn on both %q and %q; fixed quantities must not be re-drawn",
					n.Draw.FixedVariable, owner, n.ID)
			}
			fixedVars[n.Draw.FixedVariable] = n.ID
		}
	}

	for _, id := range a.order {
		n := a.nodes[id]
		for _, e := range n.Out {
			dst := a.nodes[e.To]
			if e.Retry {
				if dst.Kind != KindEntry {
					return fmt.Errorf("admission: retry edge %q -> %q must target an entry node", id, e.To)
				}
				if e.To == a.model.Root {
					return fmt.Errorf("admission: retry edge %q -> root would restart the initial attempt", id)
				}
				incomingRetry[e.To] = true
			}
		}
	}

	// Every non-root entry exists to host a retry re-entry.
	for _, id := range a.order {
		n := a.nodes[id]
		if n.Kind == KindEntry && id != a.model.Root && !incomingRetry[id] {
			return fmt.Errorf("admission: entry node %q has no incoming retry edge", id)
		}
	}

	// Longest retry count on any root-to-terminal path, exact O(V+E) DP on
	// the topological order (edges only run forward).
	maxRetry := map[string]int{}
	for _, id := range a.order {
		n := a.nodes[id]
		best := maxRetry[id]
		if n.Kind == KindTerminal && best > a.model.MaxRetries {
			return fmt.Errorf("admission: path ending at %q consumes %d retries, exceeding MaxRetries %d", id, best, a.model.MaxRetries)
		}
		for _, e := range n.Out {
			cand := best
			if e.Retry {
				cand++
			}
			if cand > maxRetry[e.To] {
				maxRetry[e.To] = cand
			}
		}
	}
	return nil
}
