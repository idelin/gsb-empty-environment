package twopc

import (
	"fmt"
	"sort"

	"twopc-lab/sim"
)

// CoordinatorConfig parameterises coordinator timing.
type CoordinatorConfig struct {
	VoteTimeout int64 // wait for all votes before unilateral abort
	RetryDelay  int64 // decision retransmission interval
}

func defaultCoordinatorConfig(c CoordinatorConfig) CoordinatorConfig {
	if c.VoteTimeout <= 0 {
		c.VoteTimeout = 20_000_000
	}
	if c.RetryDelay <= 0 {
		c.RetryDelay = 8_000_000
	}
	return c
}

type cView struct {
	txn          string
	participants []string
	keys         []string
	values       []string

	decision Decision
	yesVotes map[string]bool
	noVotes  map[string]bool

	ackedCommit map[string]bool
	ackedAbort  map[string]bool

	voteTimer  *sim.Timer
	retryTimer *sim.Timer
	retryArmed bool
}

func (v *cView) decided() bool { return v.decision != DecisionPending }

// Coordinator runs two-phase commit for any number of transactions.
type Coordinator struct {
	ID  string
	Cfg CoordinatorConfig

	// Fault-injection hooks (nil = disabled). They are consulted at the
	// exact commit point, letting a test/demo deterministically crash the
	// coordinator after all YES votes arrived but around the decision
	// record boundary.
	BeforeDecisionLogged func(txn string) bool
	AfterDecisionLogged  func(txn string) bool

	ctx *sim.NodeContext

	// txns is rebuilt from the durable log on every boot.
	txns map[string]*cView

	// startedInRun tracks transactions first seen during this uptime and is
	// therefore volatile; it is only used to gate crash hooks so recovery
	// itself cannot recrash on an old transaction.
	startedInRun map[string]bool

	safety []SafetyEvent
}

// NewCoordinator constructs an un-deployed coordinator.
func NewCoordinator(id string, cfg CoordinatorConfig) *Coordinator {
	return &Coordinator{
		ID:           id,
		Cfg:          defaultCoordinatorConfig(cfg),
		txns:         map[string]*cView{},
		startedInRun: map[string]bool{},
	}
}

// Start reboots the coordinator and rebuilds state by replaying its WAL.
//
// Recovery rule (presumed abort):
//   - a RecCommit record forces COMMIT;
//   - a RecAbort record forces ABORT;
//   - a RecBegin record with no decision yet forces a durable ABORT.
//
// In particular a coordinator that crashed after all participants prepared
// but before logging any decision can never guess COMMIT: it aborts.
func (c *Coordinator) Start(ctx *sim.NodeContext) {
	c.ctx = ctx
	c.startedInRun = map[string]bool{}

	recs, err := DecodeAll(ctx.Stable().Read())
	if err != nil {
		c.violation("unreadable WAL tail: " + err.Error())
	}
	order := []string{}
	seen := map[string]bool{}
	for _, r := range recs {
		switch r.Kind {
		case RecBegin:
			c.installBegin(r)
			if !seen[r.Txn] {
				seen[r.Txn] = true
				order = append(order, r.Txn)
			}
		case RecCommit:
			v := c.view(r.Txn)
			v.decision = DecisionCommit
		case RecAbort:
			v := c.view(r.Txn)
			v.decision = DecisionAbort
		default:
			c.violation(fmt.Sprintf("unexpected participant record kind %d for %s", r.Kind, r.Txn))
		}
	}

	// Resolve every recovered transaction according to the log.
	for _, id := range order {
		v := c.txns[id]
		switch v.decision {
		case DecisionPending:
			// No decision record survived: presumed abort. Force the
			// decision durably BEFORE telling anyone.
			c.finalizeAbort(v, "recovery: no commit record")
		case DecisionCommit, DecisionAbort:
			c.broadcastDecision(v)
			c.scheduleRetry(v, c.Cfg.RetryDelay)
		}
	}
}

// Handle dispatches a network message.
func (c *Coordinator) Handle(ctx *sim.NodeContext, env sim.Envelope) {
	c.ctx = ctx
	switch m := env.Body.(type) {
	case Prepared:
		c.onPrepared(m, env.From)
	case NoVote:
		c.onNoVote(m, env.From)
	case Ack:
		c.onAck(m)
	case DecisionQuery:
		c.onQuery(m, env.From)
	}
}

// RunTxn starts a transaction from the coordinator itself. In a real system
// phase 0 is a client request; the simulator injects it as a planned action.
func (c *Coordinator) RunTxn(txn string, participants, keys, values []string) {
	if !c.ctx.Alive() {
		return
	}
	if _, exists := c.txns[txn]; exists {
		panic("coordinator: duplicate transaction id " + txn)
	}
	// Force the begin record BEFORE any PREPARE leaves the machine, so the
	// participant set survives a crash.
	rec := Rec{Kind: RecBegin, Txn: txn, Coordinator: c.ID,
		Participants: append([]string(nil), participants...),
		Keys:         append([]string(nil), keys...),
		Values:       append([]string(nil), values...)}
	c.persist(rec)
	v := c.installBegin(rec)
	c.startedInRun[txn] = true
	c.sendPrepares(v)
	c.armVoteTimer(v)
}

func (c *Coordinator) installBegin(r Rec) *cView {
	v := c.view(r.Txn)
	v.participants = append([]string(nil), r.Participants...)
	v.keys = append([]string(nil), r.Keys...)
	v.values = append([]string(nil), r.Values...)
	return v
}

func (c *Coordinator) view(txn string) *cView {
	v := c.txns[txn]
	if v == nil {
		v = &cView{
			txn:         txn,
			yesVotes:    map[string]bool{},
			noVotes:     map[string]bool{},
			ackedCommit: map[string]bool{},
			ackedAbort:  map[string]bool{},
		}
		c.txns[txn] = v
	}
	return v
}

func (c *Coordinator) sendPrepares(v *cView) {
	for _, p := range v.participants {
		c.ctx.Send(p, Prepare{Txn: v.txn, Keys: v.keys, Values: v.values})
	}
}

func (c *Coordinator) armVoteTimer(v *cView) {
	txnID := v.txn
	v.voteTimer = c.ctx.After(c.Cfg.VoteTimeout, func() {
		vv := c.txns[txnID]
		if vv == nil || vv.decided() {
			return
		}
		// Liveness while the coordinator is up: missing/NO votes abort.
		c.finalizeAbort(vv, "vote timeout")
	})
}

func (c *Coordinator) onPrepared(m Prepared, from string) {
	v := c.txns[m.Txn]
	if v == nil {
		// Spurious vote for a transaction this coordinator never began:
		// presumed abort tells the participant to release everything.
		c.ctx.Send(from, Abort{Txn: m.Txn})
		return
	}
	if v.decided() {
		// Late vote after the decision: tell the lagging participant the
		// outcome so a later PREPARE cannot leave it uncertain.
		c.unicastDecision(v, from)
		return
	}
	v.yesVotes[from] = true
	if c.allVotesIn(v) {
		c.tryCommit(v)
	}
}

func (c *Coordinator) onNoVote(m NoVote, from string) {
	v := c.txns[m.Txn]
	if v == nil {
		c.ctx.Send(from, Abort{Txn: m.Txn})
		return
	}
	v.noVotes[from] = true
	if !v.decided() {
		c.finalizeAbort(v, "NO vote from "+from+": "+m.Reason)
		return
	}
	c.unicastDecision(v, from)
}

func (c *Coordinator) onAck(m Ack) {
	v := c.txns[m.Txn]
	if v == nil || !v.decided() {
		return
	}
	if m.Decision != v.decision {
		c.violation(fmt.Sprintf("ACK for %s disagrees with decision %s", m.Txn, v.decision))
		return
	}
	switch v.decision {
	case DecisionCommit:
		v.ackedCommit[m.From] = true
	case DecisionAbort:
		v.ackedAbort[m.From] = true
	}
	// The single retransmission tick observes these maps and stops itself
	// once every participant acknowledged; a stray late ACK cannot restart it.
}

func (c *Coordinator) onQuery(m DecisionQuery, from string) {
	v := c.txns[m.Txn]
	switch {
	case v == nil:
		// Unknown transaction: presumed abort.
		c.ctx.Send(from, Abort{Txn: m.Txn})
	case v.decision == DecisionCommit:
		c.ctx.Send(from, Commit{Txn: m.Txn})
	case v.decision == DecisionAbort:
		c.ctx.Send(from, Abort{Txn: m.Txn})
		// PENDING: reply nothing. The participant retries; the vote
		// timeout or an outstanding vote resolves it shortly.
	}
}

func (c *Coordinator) allVotesIn(v *cView) bool {
	for _, p := range v.participants {
		if !v.yesVotes[p] {
			return false
		}
	}
	return true
}

func (c *Coordinator) allAcked(v *cView, set map[string]bool) bool {
	for _, p := range v.participants {
		if !set[p] {
			return false
		}
	}
	return true
}

// tryCommit is invoked exactly when every participant has voted YES, which
// is also the only moment (besides a NO vote or timeout) the vote timer may
// be cancelled.
func (c *Coordinator) tryCommit(v *cView) {
	// Deterministic fault: crash in the narrow window between the last YES
	// vote and the COMMIT record. Recovery then finds no decision and must
	// abort — it must not guess commit.
	if c.startedInRun[v.txn] && c.BeforeDecisionLogged != nil && c.BeforeDecisionLogged(v.txn) {
		if v.voteTimer != nil {
			v.voteTimer.Stop()
			v.voteTimer = nil
		}
		c.ctx.Sched.Fail(c.ID)
		return
	}
	c.finalizeCommit(v)
}

func (c *Coordinator) finalizeCommit(v *cView) {
	if v.voteTimer != nil {
		v.voteTimer.Stop()
		v.voteTimer = nil
	}
	c.persist(Rec{Kind: RecCommit, Txn: v.txn})
	v.decision = DecisionCommit
	if c.startedInRun[v.txn] && c.AfterDecisionLogged != nil && c.AfterDecisionLogged(v.txn) {
		// Decision is durable: even this crash recovers to COMMIT.
		c.ctx.Sched.Fail(c.ID)
		return
	}
	c.broadcastDecision(v)
	c.scheduleRetry(v, c.Cfg.RetryDelay)
}

func (c *Coordinator) finalizeAbort(v *cView, reason string) {
	if v.voteTimer != nil {
		v.voteTimer.Stop()
		v.voteTimer = nil
	}
	c.persist(Rec{Kind: RecAbort, Txn: v.txn, Reason: reason})
	v.decision = DecisionAbort
	c.broadcastDecision(v)
	c.scheduleRetry(v, c.Cfg.RetryDelay)
}

func (c *Coordinator) broadcastDecision(v *cView) {
	for _, p := range v.participants {
		c.unicastDecision(v, p)
	}
}

func (c *Coordinator) unicastDecision(v *cView, p string) {
	switch v.decision {
	case DecisionCommit:
		c.ctx.Send(p, Commit{Txn: v.txn})
	case DecisionAbort:
		c.ctx.Send(p, Abort{Txn: v.txn})
	}
}

// scheduleRetry arms the decision-retransmission tick for a decided
// transaction. Only one tick series exists per transaction per uptime; the
// tick resends the decision solely to participants that have not acked and
// perpetuates itself until all have acknowledged.
func (c *Coordinator) scheduleRetry(v *cView, delay int64) {
	if v.retryArmed {
		return
	}
	v.retryArmed = true
	if delay <= 0 {
		delay = c.Cfg.RetryDelay
	}
	txnID := v.txn
	var tick func(wait int64)
	tick = func(wait int64) {
		v.retryTimer = c.ctx.After(wait, func() {
			vv := c.txns[txnID]
			if vv == nil || !vv.decided() || !c.ctx.Alive() {
				return
			}
			acked := vv.ackedCommit
			if vv.decision == DecisionAbort {
				acked = vv.ackedAbort
			}
			allDone := true
			for _, p := range vv.participants {
				if !acked[p] {
					allDone = false
					c.unicastDecision(vv, p)
				}
			}
			if allDone {
				vv.retryArmed = false
				vv.retryTimer = nil
				return
			}
			tick(c.Cfg.RetryDelay)
		})
	}
	tick(delay)
}

func (c *Coordinator) persist(r Rec) {
	b, err := r.Encode()
	if err != nil {
		panic(err)
	}
	c.ctx.Stable().Append(b)
}

func (c *Coordinator) violation(msg string) {
	c.safety = append(c.safety, SafetyEvent{At: c.ctx.Now(), Node: c.ID, Msg: msg})
}

// ---- read-only accessors ----

// DecisionOf returns the coordinator's current decision for a transaction.
func (c *Coordinator) DecisionOf(txn string) Decision {
	if v := c.txns[txn]; v != nil {
		return v.decision
	}
	return DecisionPending
}

// SafetyViolations returns protocol violations observed at the coordinator.
func (c *Coordinator) SafetyViolations() []SafetyEvent {
	return append([]SafetyEvent(nil), c.safety...)
}

// ParticipantsOf returns the durable participant set for a transaction.
func (c *Coordinator) ParticipantsOf(txn string) []string {
	if v := c.txns[txn]; v != nil {
		return append([]string(nil), v.participants...)
	}
	return nil
}

// TxnIDs returns transaction ids in sorted order (tests/demo).
func (c *Coordinator) TxnIDs() []string {
	ids := make([]string, 0, len(c.txns))
	for id := range c.txns {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
