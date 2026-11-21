package recovery

// Executor runs commands against one concrete replica. It is the only
// component that touches the real committed bitmap and key/value state; the
// coordinator receives receipts only.
//
// At most one disconnect happens over the executor's lifetime. A breakpoint
// hook lets tests force the disconnect at every admissible position,
// including before the first and after the last transaction.
type Executor struct {
	p            *Problem
	bits         Bitset
	kv           map[Key]Value
	disconnected bool
	spent        int

	// BreakHook, when set, is called before each transaction and once before
	// the first transaction with the index of the next transaction (0..len).
	// Returning true consumes the single allowed disconnect at that point.
	BreakHook        func(cmd Command, nextIndex int) bool
	everDisconnected bool
}

// NewExecutor installs a replica at a candidate site's persisted state.
func NewExecutor(p *Problem, s Site) *Executor {
	return &Executor{p: p, bits: s.Committed, kv: copyKV(s.KV)}
}

// Committed exposes the real bitmap only to test harness code that owns the
// executor; the coordinator API never receives this value.
func (e *Executor) Committed() Bitset { return e.bits }

// KV returns a defensive copy of the current replica key/value state.
func (e *Executor) KV() map[Key]Value { return copyKV(e.kv) }

// Spent returns the total fees charged so far. Rejections and disconnects are
// not refunded.
func (e *Executor) Spent() int { return e.spent }

// Disconnected reports whether the single allowed disconnect has occurred.
func (e *Executor) Disconnected() bool { return e.disconnected }

// Reconnect restores communication after the disconnect. It never rolls back
// committed transactions.
func (e *Executor) Reconnect() { e.disconnected = false }

// EverDisconnected reports whether the single allowed disconnect has been
// consumed at any point, even after Reconnect.
func (e *Executor) EverDisconnected() bool { return e.everDisconnected }

// FindCommand resolves a command from the fixed library.
func (p *Problem) FindCommand(name string) (Command, bool) {
	for _, c := range p.Commands {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// FindProbe resolves a probe from the fixed library.
func (p *Problem) FindProbe(name string) (Probe, bool) {
	for _, pr := range p.Probes {
		if pr.Name == name {
			return pr, true
		}
	}
	return Probe{}, false
}

// RunCommit executes a commit command transaction by transaction. The command
// fee is always charged first, matching no-refund semantics.
func (e *Executor) RunCommit(c Command) Receipt {
	e.spent += c.Cost
	txns := e.p.groupTxns(c.Group)
	attempt := txns[:c.Prefix]

	// Breakpoint before the first transaction (the "head" position).
	if e.tryBreak(c, 0) {
		return DisconnectReceipt{}
	}
	for i, t := range attempt {
		if e.bits.Contains(t.ID) {
			// Already committed: dedupe without predicate or write.
			if e.tryBreak(c, i+1) {
				return DisconnectReceipt{}
			}
			continue
		}
		if !e.p.txnReadOK(e.kv, t) {
			return RejectReceipt{RejectID: t.ID}
		}
		e.kv = applyTxn(e.kv, t)
		e.bits.add(t.ID)
		if e.tryBreak(c, i+1) {
			return DisconnectReceipt{}
		}
	}
	return OKReceipt{}
}

func (e *Executor) tryBreak(c Command, nextIndex int) bool {
	if e.disconnected || e.BreakHook == nil || !e.BreakHook(c, nextIndex) {
		return false
	}
	e.disconnected = true
	e.everDisconnected = true
	return true
}

// RunProbe answers a reliable parity probe and charges its fee.
func (e *Executor) RunProbe(pr Probe) ProbeReceipt {
	e.spent += pr.Cost
	return ProbeReceipt{Group: pr.Group, Answer: e.p.groupParity(e.bits, pr.Group)}
}

// AtTarget reports whether the replica now equals the target bitmap and
// key/value state exactly.
func (e *Executor) AtTarget() bool {
	return e.bits == e.p.TargetCommitted && kvEqual(e.kv, e.p.TargetKV)
}
