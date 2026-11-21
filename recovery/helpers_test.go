package recovery

import "testing"

// deliver executes one coordinator action against the executor, translating
// the fixed library names back into commands/probes.
func deliver(t *testing.T, p *Problem, e *Executor, a Action) Receipt {
	t.Helper()
	if a.IsProbe {
		pr, ok := p.FindProbe(a.Name)
		if !ok {
			t.Fatalf("unknown probe %s", a.Name)
		}
		return e.RunProbe(pr)
	}
	c, ok := p.FindCommand(a.Name)
	if !ok {
		t.Fatalf("unknown command %s", a.Name)
	}
	return e.RunCommit(c)
}

// runToDoneWithBreak drives the coordinator until completion. A disconnect
// receipt triggers Reconnect and redelivery of the pending action; the
// coordinator keeps the pending action until a non-disconnect receipt arrives.
func runToDoneWithBreak(t *testing.T, p *Problem, c *Coordinator, e *Executor) {
	t.Helper()
	for steps := 0; steps < 200; steps++ {
		if c.Done() {
			return
		}
		a, err := c.Next()
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		r := deliver(t, p, e, a)
		if _, disc := r.(DisconnectReceipt); disc {
			if !e.Disconnected() {
				t.Fatal("executor did not register disconnect")
			}
			e.Reconnect()
			// Re-ask without Observe: the receipt carried no length.
			continue
		}
		if err := c.Observe(r); err != nil {
			t.Fatalf("observe: %v", err)
		}
	}
	t.Fatal("coordinator did not finish within step budget")
}

// bruteForceValue is an independent minimax solver written directly from the
// game definition. It shares only the low-level transition simulation with
// production code and uses memoized recursion with an on-stack cycle guard.
// Positive costs make goal-free cycles worthless, so a re-entered node with no
// resolved value is treated as losing.
func bruteForceValue(p *Problem, b belief, memo map[string]int, depth int) (int, bool) {
	if p.atGoal(b) {
		return 0, true
	}
	if depth > 64 {
		return 0, false
	}
	if v, ok := memo[b.key()]; ok {
		if v < 0 {
			return 0, false
		}
		return v, true
	}
	memo[b.key()] = -1
	best := -1
	for _, a := range p.buildActions() {
		var obs []observation
		if a.kind == kindCommand {
			obs = p.applyCommit(b, a.cmd)
		} else {
			obs = p.applyProbe(b, a.pr)
		}
		worst := 0
		ok := true
		for _, o := range obs {
			v, childOK := bruteForceValue(p, o.b, memo, depth+1)
			if !childOK {
				ok = false
				break
			}
			if v > worst {
				worst = v
			}
		}
		if ok {
			candidate := a.cost + worst
			if best == -1 || candidate < best {
				best = candidate
			}
		}
	}
	if best == -1 {
		memo[b.key()] = -1
		return 0, false
	}
	memo[b.key()] = best
	return best, true
}
