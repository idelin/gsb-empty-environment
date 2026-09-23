package twopc

import (
	"fmt"
	"sort"

	"twopc-lab/sim"
)

// Participant state for a single transaction.
const (
	pUnknown   = iota
	pPreparing // received PREPARE, not yet voted
	pPrepared  // durable YES vote; locks held; outcome unknown
	pCommitted
	pAborted
)

// SafetyEvent records a protocol violation observed locally. The simulator
// never produces one in correct runs; tests assert the slice stays empty.
type SafetyEvent struct {
	At   int64
	Node string
	Msg  string
}

func (e SafetyEvent) String() string {
	return fmt.Sprintf("t=%d node=%s SAFETY: %s", e.At, e.Node, e.Msg)
}

// ParticipantConfig parameterises timing of a participant.
type ParticipantConfig struct {
	QueryDelay int64 // retry interval for a decision query while PREPARED
}

func defaultParticipantConfig(c ParticipantConfig) ParticipantConfig {
	if c.QueryDelay <= 0 {
		c.QueryDelay = 8_000_000
	}
	return c
}

// Participant is a 2PC cohort and the owner of a shard of the key-value
// resource. Locks are taken in phase 1 and released by COMMIT (after apply)
// or ABORT.
type Participant struct {
	ID          string
	Coordinator string
	Cfg         ParticipantConfig

	ctx *sim.NodeContext

	// txns holds every transaction ever seen (including decided ones,
	// which are needed to make re-delivered decisions idempotent).
	txns map[string]*pView

	committed map[string]bool // committed transaction ids (for tests)
	aborted   map[string]bool // aborted transaction ids (for tests)
	safety    []SafetyEvent

	// Resource manager state (volatile; rebuilt from the WAL on boot):
	data  map[string]string
	locks map[string]string // key -> holder txn
}

type pView struct {
	state  int
	keys   []string
	values []string
	query  *sim.Timer
}

// NewParticipant constructs an un-deployed participant.
func NewParticipant(id, coordinator string, cfg ParticipantConfig) *Participant {
	return &Participant{
		ID:          id,
		Coordinator: coordinator,
		Cfg:         defaultParticipantConfig(cfg),
		txns:        map[string]*pView{},
		committed:   map[string]bool{},
		aborted:     map[string]bool{},
		data:        map[string]string{},
		locks:       map[string]string{},
	}
}

// Start reboots the participant, replaying durable records. Locks for
// transactions found in the PREPARED state are recovered.
func (p *Participant) Start(ctx *sim.NodeContext) {
	p.ctx = ctx
	recs, err := DecodeAll(ctx.Stable().Read())
	if err != nil {
		p.violation("unreadable WAL tail: " + err.Error())
	}
	for _, r := range recs {
		switch r.Kind {
		case RecPrepared:
			p.installPrepared(r)
		case RecCommitted:
			p.applyCommitted(r)
		case RecAbort:
			p.applyAborted(r)
		default:
			p.violation(fmt.Sprintf("unexpected %s record %d for %s", p.ID, r.Kind, r.Txn))
		}
	}
	// Every PREPARED transaction surviving recovery is uncertain: ask the
	// coordinator for the outcome. Iterate in sorted order so runs are
	// deterministic.
	ids := make([]string, 0, len(p.txns))
	for id := range p.txns {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if v := p.txns[id]; v.state == pPrepared {
			p.scheduleQuery(id, v, 0)
		}
	}
}

// Handle dispatches a network message.
func (p *Participant) Handle(ctx *sim.NodeContext, env sim.Envelope) {
	p.ctx = ctx
	switch m := env.Body.(type) {
	case Prepare:
		p.onPrepare(m)
	case Commit:
		p.onCommit(m.Txn, env.From)
	case Abort:
		p.onAbort(m.Txn, env.From)
	case DecisionQuery:
		// Only a coordinator would be addressed as a participant here;
		// participants ignore it.
	}
}

func (p *Participant) view(txn string) *pView {
	v := p.txns[txn]
	if v == nil {
		v = &pView{state: pUnknown}
		p.txns[txn] = v
	}
	return v
}

func (p *Participant) onPrepare(m Prepare) {
	v := p.view(m.Txn)
	switch v.state {
	case pCommitted:
		// Idempotent duplicate: the global outcome is already commit.
		return
	case pAborted:
		// A late or repeated PREPARE for an aborted transaction must not
		// re-acquire anything. Reaffirm the NO so the coordinator cannot
		// ever count a YES.
		p.ctx.Send(p.Coordinator, NoVote{Txn: m.Txn, From: p.ID, Reason: "txn already aborted here"})
		return
	case pPrepared:
		// Duplicated PREPARE before the decision: repeat the durable vote.
		p.ctx.Send(p.Coordinator, Prepared{Txn: m.Txn})
		return
	case pPreparing:
		// Duplicated PREPARE while the first is still being handled does
		// not occur in the single-threaded simulator, but treat it as a
		// request to complete the vote below.
	}

	keys := append([]string(nil), m.Keys...)
	values := append([]string(nil), m.Values...)

	// Phase-1 guard: every key must be free. A single conflict votes NO;
	// nothing is locked for this transaction.
	var conflict string
	for _, k := range keys {
		if holder, busy := p.locks[k]; busy && holder != m.Txn {
			conflict = k
			break
		}
	}
	if conflict != "" {
		p.persist(Rec{Kind: RecAbort, Txn: m.Txn, Coordinator: p.Coordinator,
			Keys: keys, Values: values, Reason: "lock conflict on " + conflict})
		v.state = pAborted
		v.keys = keys
		v.values = values
		p.aborted[m.Txn] = true
		p.ctx.Send(p.Coordinator, NoVote{Txn: m.Txn, From: p.ID, Reason: "lock conflict on " + conflict})
		return
	}

	// Durable YES: force the PREPARED record (which includes the locks'
	// identity) BEFORE replying.
	p.persist(Rec{Kind: RecPrepared, Txn: m.Txn, Coordinator: p.Coordinator,
		Keys: keys, Values: values})
	for _, k := range keys {
		p.locks[k] = m.Txn
	}
	v.state = pPrepared
	v.keys = keys
	v.values = values
	p.ctx.Send(p.Coordinator, Prepared{Txn: m.Txn})
}

func (p *Participant) onCommit(txn, from string) {
	if from != p.Coordinator {
		p.violation(fmt.Sprintf("COMMIT for %s from non-coordinator %s", txn, from))
		return
	}
	v := p.view(txn)
	switch v.state {
	case pCommitted:
		// Repeated COMMIT is idempotent; just re-ack.
		p.ctx.Send(p.Coordinator, Ack{Txn: txn, From: p.ID, Decision: DecisionCommit})
		return
	case pAborted:
		p.violation("COMMIT for locally-aborted txn " + txn)
		return
	case pUnknown, pPreparing:
		// COMMIT without a local PREPARED record must not apply: the
		// participant never voted, so a correct coordinator cannot have
		// decided commit. It reports the inconsistency rather than
		// guessing.
		p.violation("COMMIT for unprepared txn " + txn)
		return
	}
	// pPrepared: apply, force a terminal record, release locks.
	p.stopQuery(v)
	p.persist(Rec{Kind: RecCommitted, Txn: txn, Coordinator: p.Coordinator,
		Keys: append([]string(nil), v.keys...), Values: append([]string(nil), v.values...)})
	p.applyWrite(v.keys, v.values)
	for _, k := range v.keys {
		if p.locks[k] == txn {
			delete(p.locks, k)
		}
	}
	v.state = pCommitted
	p.committed[txn] = true
	p.ctx.Send(p.Coordinator, Ack{Txn: txn, From: p.ID, Decision: DecisionCommit})
}

func (p *Participant) onAbort(txn, from string) {
	if from != p.Coordinator {
		p.violation(fmt.Sprintf("ABORT for %s from non-coordinator %s", txn, from))
		return
	}
	v := p.view(txn)
	switch v.state {
	case pAborted:
		// Idempotent repeated ABORT, including the "late PREPARE already
		// voted NO" case.
		p.ctx.Send(p.Coordinator, Ack{Txn: txn, From: p.ID, Decision: DecisionAbort})
		return
	case pCommitted:
		p.violation("ABORT for already-committed txn " + txn)
		return
	}
	// Unknown / preparing / prepared: a late PREPARE arriving after this
	// ABORT finds an aborted view and can never take the locks (they are
	// not held here at all).
	p.stopQuery(v)
	if v.state != pUnknown {
		for _, k := range v.keys {
			if p.locks[k] == txn {
				delete(p.locks, k)
			}
		}
	}
	p.persist(Rec{Kind: RecAbort, Txn: txn, Coordinator: p.Coordinator,
		Keys: append([]string(nil), v.keys...), Values: append([]string(nil), v.values...),
		Reason: "coordinator decision"})
	v.state = pAborted
	p.aborted[txn] = true
	p.ctx.Send(p.Coordinator, Ack{Txn: txn, From: p.ID, Decision: DecisionAbort})
}

func (p *Participant) scheduleQuery(txnID string, v *pView, delay int64) {
	if delay <= 0 {
		delay = p.Cfg.QueryDelay
	}
	v.query = p.ctx.After(delay, func() {
		if v.state != pPrepared || !p.ctx.Alive() {
			return
		}
		p.ctx.Send(p.Coordinator, DecisionQuery{Txn: txnID})
		p.scheduleQuery(txnID, v, p.Cfg.QueryDelay)
	})
}

func (p *Participant) stopQuery(v *pView) {
	if v.query != nil {
		v.query.Stop()
		v.query = nil
	}
}

func (p *Participant) installPrepared(r Rec) {
	v := p.view(r.Txn)
	v.keys = append([]string(nil), r.Keys...)
	v.values = append([]string(nil), r.Values...)
	// Recovery replay is ordered; a later terminal record supersedes.
	if v.state == pUnknown || v.state == pPreparing {
		v.state = pPrepared
	}
	for _, k := range v.keys {
		p.locks[k] = r.Txn
	}
}

func (p *Participant) applyCommitted(r Rec) {
	v := p.view(r.Txn)
	v.keys = append([]string(nil), r.Keys...)
	v.values = append([]string(nil), r.Values...)
	if v.state == pPrepared {
		for _, k := range v.keys {
			if p.locks[k] == r.Txn {
				delete(p.locks, k)
			}
		}
	}
	p.applyWrite(v.keys, v.values)
	v.state = pCommitted
	p.committed[r.Txn] = true
	p.stopQuery(v)
}

func (p *Participant) applyAborted(r Rec) {
	v := p.view(r.Txn)
	if len(v.keys) == 0 {
		v.keys = append([]string(nil), r.Keys...)
		v.values = append([]string(nil), r.Values...)
	}
	for _, k := range v.keys {
		if p.locks[k] == r.Txn {
			delete(p.locks, k)
		}
	}
	v.state = pAborted
	p.aborted[r.Txn] = true
	p.stopQuery(v)
}

func (p *Participant) applyWrite(keys, values []string) {
	for i, k := range keys {
		if i < len(values) && values[i] != "" {
			p.data[k] = values[i]
		} else {
			delete(p.data, k)
		}
	}
}

func (p *Participant) persist(r Rec) {
	b, err := r.Encode()
	if err != nil {
		panic(err)
	}
	p.ctx.Stable().Append(b)
}

func (p *Participant) violation(msg string) {
	p.safety = append(p.safety, SafetyEvent{At: p.ctx.Now(), Node: p.ID, Msg: msg})
}

// ---- read-only accessors used by tests and the demo ----

// SafetyViolations returns protocol violations observed at this participant.
func (p *Participant) SafetyViolations() []SafetyEvent {
	return append([]SafetyEvent(nil), p.safety...)
}

// IsCommitted reports whether the transaction locally committed.
func (p *Participant) IsCommitted(txn string) bool { return p.committed[txn] }

// IsAborted reports whether the transaction locally aborted.
func (p *Participant) IsAborted(txn string) bool { return p.aborted[txn] }

// LockHolder reports which transaction holds key, or "".
func (p *Participant) LockHolder(key string) string { return p.locks[key] }

// HoldsLock reports whether txn currently holds key.
func (p *Participant) HoldsLock(txn, key string) bool { return p.locks[key] == txn }

// Get returns the committed value of a key.
func (p *Participant) Get(key string) (string, bool) {
	v, ok := p.data[key]
	return v, ok
}

// TxnState returns the local state constant for a transaction.
func (p *Participant) TxnState(txn string) int {
	if v := p.txns[txn]; v != nil {
		return v.state
	}
	return pUnknown
}

// State constants exported for tests.
const (
	StateUnknown   = pUnknown
	StatePreparing = pPreparing
	StatePrepared  = pPrepared
	StateCommitted = pCommitted
	StateAborted   = pAborted
)
