package recovery

import (
	"math/rand"
	"testing"
)

func TestEveryBreakpointRedelivery(t *testing.T) {
	p := instanceA()
	sol, err := p.Solve()
	if err != nil {
		t.Fatalf("solve: %v", err)
	}
	hit := map[int]bool{}
	for _, site := range p.Sites {
		// Collect the commands the policy needs on this site. Every position
		// 0..Prefix is an admissible breakpoint, including head and tail.
		probe := NewExecutor(p, site)
		pc, _ := NewCoordinator(sol)
		reachable := map[int]bool{}
		for !pc.Done() {
			a, err := pc.Next()
			if err != nil {
				t.Fatalf("next: %v", err)
			}
			if a.IsProbe {
				r := probe.RunProbe(mustProbe(t, p, a.Name))
				if err := pc.Observe(r); err != nil {
					t.Fatalf("observe: %v", err)
				}
				continue
			}
			cmd := mustCommand(t, p, a.Name)
			for idx := 0; idx <= cmd.Prefix; idx++ {
				reachable[idx] = true
			}
			r := probe.RunCommit(cmd)
			if err := pc.Observe(r); err != nil {
				t.Fatalf("observe: %v", err)
			}
		}

		for breakpoint := 0; breakpoint <= 3; breakpoint++ {
			if !reachable[breakpoint] {
				continue
			}
			bp := breakpoint
			e := NewExecutor(p, site)
			hookFired := false
			e.BreakHook = func(_ Command, nextIndex int) bool {
				if !hookFired && nextIndex == bp {
					hookFired = true
					hit[bp] = true
					return true
				}
				return false
			}
			c, err := NewCoordinator(sol)
			if err != nil {
				t.Fatalf("coordinator: %v", err)
			}
			runToDoneWithBreak(t, p, c, e)
			if !e.AtTarget() {
				t.Fatalf("site=%s breakpoint=%d did not reach target", site.Name, bp)
			}
			if !e.EverDisconnected() {
				t.Fatalf("site=%s breakpoint=%d hook never fired", site.Name, bp)
			}
			if c.Spent() != e.Spent() {
				t.Fatalf("coordinator fee %d != executor fee %d", c.Spent(), e.Spent())
			}
		}
	}
	if len(hit) == 0 {
		t.Fatal("no breakpoint was exercised")
	}
}

func mustCommand(t *testing.T, p *Problem, name string) Command {
	t.Helper()
	c, ok := p.FindCommand(name)
	if !ok {
		t.Fatalf("unknown command %s", name)
	}
	return c
}

func mustProbe(t *testing.T, p *Problem, name string) Probe {
	t.Helper()
	pr, ok := p.FindProbe(name)
	if !ok {
		t.Fatalf("unknown probe %s", name)
	}
	return pr
}

func TestLostLastReceiptReplay(t *testing.T) {
	p := instanceA()
	sol, _ := p.Solve()
	e := NewExecutor(p, p.Sites[0])
	c, _ := NewCoordinator(sol)

	first, err := c.Next()
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	r := deliver(t, p, e, first)
	if _, ok := r.(OKReceipt); !ok {
		t.Fatalf("first receipt = %T, want OKReceipt on empty site", r)
	}
	// The final receipt is lost before Observe; the coordinator must redeliver
	// the same action, and committed transactions dedupe by ID.
	again, err := c.Next()
	if err != nil {
		t.Fatalf("redelivery next: %v", err)
	}
	if again.Name != first.Name {
		t.Fatalf("pending action changed: %s -> %s", first.Name, again.Name)
	}
	againReceipt := deliver(t, p, e, again)
	if _, ok := againReceipt.(OKReceipt); !ok {
		t.Fatalf("redelivery receipt = %T, want OKReceipt", againReceipt)
	}
	if err := c.Observe(againReceipt); err != nil {
		t.Fatalf("observe redelivery: %v", err)
	}
	if !c.Done() || !e.AtTarget() {
		t.Fatal("replay after lost receipt must finish safely")
	}
	if e.Committed() != p.TargetCommitted {
		t.Fatal("replay must not duplicate or remove committed transactions")
	}
}

func TestFeasibleShortPrefixIsNotCompletion(t *testing.T) {
	p := instanceA()
	sol, _ := p.Solve()
	e := NewExecutor(p, p.Sites[2]) // already has txns 0 and 1.
	short, ok := p.FindCommand("c1")
	if !ok {
		t.Fatal("c1 missing")
	}
	r := e.RunCommit(short)
	if _, ok := r.(OKReceipt); !ok {
		t.Fatalf("short prefix should be feasible, got %T", r)
	}
	if e.AtTarget() {
		t.Fatal("a feasible prefix that only dedupes must not be mistaken for completion")
	}
	c, _ := NewCoordinator(sol)
	if c.Done() {
		t.Fatal("coordinator must not announce completion from a feasible short prefix alone")
	}
}

func TestAdversarialRandomRuns(t *testing.T) {
	for seed := int64(0); seed < 60; seed++ {
		rng := rand.New(rand.NewSource(seed))
		p := randomProblem(rng)
		if err := p.Validate(); err != nil {
			t.Fatalf("random problem invalid: %v", err)
		}
		sol, err := p.Solve()
		if err != nil {
			t.Fatalf("solve seed=%d: %v", seed, err)
		}
		naive, naiveOK := bruteForceValue(p, p.initialBelief(), map[string]int{}, 0)
		if sol.Solvable() != naiveOK {
			t.Fatalf("seed=%d solvability mismatch planner=%v brute=%v", seed, sol.Solvable(), naiveOK)
		}
		if !sol.Solvable() {
			if err := p.Verify(sol.Certificate()); err != nil {
				t.Fatalf("seed=%d certificate: %v", seed, err)
			}
			continue
		}
		optimal, _ := sol.OptimalCost()
		if optimal != naive {
			t.Fatalf("seed=%d cost mismatch planner=%d brute=%d", seed, optimal, naive)
		}
		// Adversarial environment: choose the worst breakpoint (or none) while
		// still forcing the coordinator to the target against every site.
		for _, site := range p.Sites {
			runAdversarial(t, p, sol, site, rng)
		}
	}
}

func runAdversarial(t *testing.T, p *Problem, sol *Solution, site Site, rng *rand.Rand) {
	t.Helper()
	e := NewExecutor(p, site)
	willBreak := rng.Intn(2) == 0
	broke := false
	e.BreakHook = func(_ Command, nextIndex int) bool {
		if willBreak && !broke && rng.Intn(2) == 0 {
			broke = true
			return true
		}
		return false
	}
	c, err := NewCoordinator(sol)
	if err != nil {
		t.Fatalf("coordinator: %v", err)
	}
	for steps := 0; steps < 300; steps++ {
		if c.Done() {
			if !e.AtTarget() {
				t.Fatal("coordinator announced done but replica differs from target")
			}
			return
		}
		a, err := c.Next()
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		r := deliver(t, p, e, a)
		if _, disc := r.(DisconnectReceipt); disc {
			e.Reconnect()
			continue
		}
		if err := c.Observe(r); err != nil {
			t.Fatalf("observe: %v", err)
		}
	}
	t.Fatal("adversarial run exceeded step budget")
}
