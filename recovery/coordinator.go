package recovery

// Action describes the next fixed-library attempt chosen by the policy.
type Action struct {
	Name    string
	IsProbe bool
	Cost    int
}

// Coordinator is the receipt-driven decision maker. Its only mutable state is
// the belief set and the pending action; it cannot read the replica's bitmap.
type Coordinator struct {
	sol     *Solution
	belief  belief
	pending *action
	spent   int
}

// NewCoordinator builds a coordinator from a solved instance. It returns an
// error when the instance has no guaranteed strategy.
func NewCoordinator(sol *Solution) (*Coordinator, error) {
	if sol == nil || !sol.Solvable() {
		return nil, ErrNotSolved
	}
	return &Coordinator{sol: sol, belief: sol.initial.clone()}, nil
}

// Done reports that every world still compatible with the observed receipts
// equals the target exactly. Announcing completion before Done is unsafe.
func (c *Coordinator) Done() bool { return c.sol.p.atGoal(c.belief) }

// Spent returns fees accumulated along the observed receipt path.
func (c *Coordinator) Spent() int { return c.spent }

// BeliefSize exposes the number of compatible worlds for diagnostics.
func (c *Coordinator) BeliefSize() int { return len(c.belief.worlds) }

// Next returns the action chosen for the current belief. Re-calling Next
// before Observe (for example after a lost last receipt) returns the same
// action so the caller can redeliver it; redelivery stays safe because the
// executor deduplicates committed transactions by ID.
func (c *Coordinator) Next() (Action, error) {
	if c.Done() {
		return Action{}, ErrNoPolicy
	}
	if c.pending != nil {
		// Redelivery after a lost receipt or a reconnect: the interrupted
		// attempt was already paid for and is not refunded, and the new attempt
		// is charged again by the environment.
		c.spent += c.pending.cost
		return c.pending.action(), nil
	}
	a, ok := c.sol.policy[c.belief.key()]
	if !ok {
		return Action{}, ErrNoPolicy
	}
	c.pending = &a
	c.spent += a.cost
	return a.action(), nil
}

// Observe incorporates a public receipt for the pending action and advances
// the belief. Fees are charged at issue time in Next, so a disconnect receipt
// that is followed by redelivery still pays for the interrupted attempt; the
// no-refund rule is therefore reflected in Spent.
func (c *Coordinator) Observe(r Receipt) error {
	if c.pending == nil {
		return ErrUnexpectedReceipt
	}
	a := *c.pending
	switch a.kind {
	case kindCommand:
		if !receiptPossible(c.sol.p.applyCommit(c.belief, a.cmd), r) {
			return ErrUnexpectedReceipt
		}
	case kindProbe:
		if !receiptPossible(c.sol.p.applyProbe(c.belief, a.pr), r) {
			return ErrUnexpectedReceipt
		}
	}
	c.belief = c.filter(a, r)
	c.pending = nil
	return nil
}

func receiptPossible(obs []observation, r Receipt) bool {
	want := receiptKey(r)
	for _, o := range obs {
		if receiptKey(o.receipt) == want {
			return true
		}
	}
	return false
}

func (c *Coordinator) filter(a action, r Receipt) belief {
	var obs []observation
	if a.kind == kindCommand {
		obs = c.sol.p.applyCommit(c.belief, a.cmd)
	} else {
		obs = c.sol.p.applyProbe(c.belief, a.pr)
	}
	want := receiptKey(r)
	for _, o := range obs {
		if receiptKey(o.receipt) == want {
			return o.b
		}
	}
	return c.belief
}

func (a action) action() Action {
	return Action{Name: a.name, IsProbe: a.kind == kindProbe, Cost: a.cost}
}

// RunOnce is a convenience helper that issues the next action through send.
// send must execute it against the single replica and return the public
// receipt; the coordinator never sees anything beyond that receipt.
func (c *Coordinator) RunOnce(send func(Action) (Receipt, error)) error {
	a, err := c.Next()
	if err != nil {
		return err
	}
	r, err := send(a)
	if err != nil {
		return err
	}
	return c.Observe(r)
}
