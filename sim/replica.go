package sim

import "fmt"

// Sentinel errors returned by client operations.
var (
	ErrInvalidAmount  = fmt.Errorf("quota: amount must be positive")
	ErrOverdraft      = fmt.Errorf("quota: insufficient local credit")
	ErrReplicaDown    = fmt.Errorf("quota: replica is down")
	ErrUnknownReplica = fmt.Errorf("quota: unknown target replica")
	ErrNoSelfTransfer = fmt.Errorf("quota: cannot transfer credit to self")
)

// pendingT is an in-flight transfer whose ack has not been durably received.
type pendingT struct {
	amount  int64
	target  int64
	retries int
}

// Replica is one quota holder. Its spendable balance plus its escrowed
// (in-flight) funds equal its durable credit; escrowed funds cannot be
// spent or re-sent.
type Replica struct {
	id   int64
	sim  *Simulator
	disk *Disk

	balance int64 // spendable credit only (escrow excluded)
	spent   int64 // total locally consumed credit
	pending map[TransferID]*pendingT
	nextSeq int64
	down    bool

	// ResendInterval is the retransmission timeout. Timers live only in
	// volatile memory and are re-armed on restart.
	ResendInterval int64

	transferDups int
	ackDups      int
}

func newReplica(id int64, s *Simulator) *Replica {
	return &Replica{
		id:             id,
		sim:            s,
		disk:           newDisk(),
		pending:        make(map[TransferID]*pendingT),
		ResendInterval: 20,
	}
}

// bootstrap records the genesis grant out of the global quota.
func (r *Replica) bootstrap(credit int64) {
	if credit < 0 {
		panic("quota: negative initial credit")
	}
	r.persist(op{Kind: opGenesis, Amount: credit})
}

// persist appends a durable record, waits for the fsync barrier and only then
// applies it to memory. Every external effect (message send, success return)
// happens strictly after this returns.
func (r *Replica) persist(o op) {
	r.disk.Append(o)
	r.disk.Fsync()
	r.apply(o)
}

func (r *Replica) apply(o op) {
	switch o.Kind {
	case opGenesis:
		r.balance += o.Amount
	case opSpend:
		r.balance -= o.Amount
		r.spent += o.Amount
	case opOpen:
		// Funds leave the spendable balance and enter escrow immediately;
		// no later step ever moves the same units again.
		r.balance -= o.Amount
		r.pending[o.ID] = &pendingT{amount: o.Amount, target: o.Peer}
	case opDeliver:
		r.balance += o.Amount
	case opAck:
		delete(r.pending, o.ID)
	}
	if o.ID.From == r.id && o.ID.Seq > r.nextSeq {
		r.nextSeq = o.ID.Seq
	}
}

// crash wipes process memory; the disk log is retained.
func (r *Replica) crash() {
	r.down = true
	r.balance = 0
	r.spent = 0
	r.pending = nil
	r.nextSeq = 0
}

// restart rebuilds all volatile state by replaying the durable log and
// re-arms retransmission timers for still-open transfers.
func (r *Replica) restart() {
	r.down = false
	r.balance = 0
	r.spent = 0
	r.pending = make(map[TransferID]*pendingT)
	r.nextSeq = 0
	for _, o := range r.disk.Records() {
		r.apply(o)
	}
	for id := range r.pending {
		pid := id
		r.sim.After(r.ResendInterval, func(int64) { r.tick(pid) })
	}
}

// ID returns the replica identifier.
func (r *Replica) ID() int64 { return r.id }

// Balance is the spendable credit (escrow excluded).
func (r *Replica) Balance() int64 { return r.balance }

// Spent is the total credit consumed by local offline deductions.
func (r *Replica) Spent() int64 { return r.spent }

// InFlight is credit escrowed on transfers not yet acked.
func (r *Replica) InFlight() int64 {
	var sum int64
	for _, p := range r.pending {
		sum += p.amount
	}
	return sum
}

// PendingCount is the number of un-acked outgoing transfers.
func (r *Replica) PendingCount() int { return len(r.pending) }

// Down reports whether the replica is currently crashed.
func (r *Replica) Down() bool { return r.down }

// TransferDuplicates counts duplicate transfer copies rejected on receive.
func (r *Replica) TransferDuplicates() int { return r.transferDups }

// AckDuplicates counts duplicate acknowledgements rejected.
func (r *Replica) AckDuplicates() int { return r.ackDups }

// Spend deducts amount from local credit without contacting anyone. It is the
// offline consumption path: it can never overdraw and can never consume more
// than the global quota, because every spendable unit originates from the
// single quota partition and is either present here, escrowed or spent.
func (r *Replica) Spend(amount int64) error {
	if r.down {
		return ErrReplicaDown
	}
	if amount <= 0 {
		return ErrInvalidAmount
	}
	if amount > r.balance {
		return ErrOverdraft
	}
	r.persist(op{Kind: opSpend, Amount: amount})
	r.sim.logf("R%d spent %d (balance %d)", r.id, amount, r.balance)
	return nil
}

// Transfer atomically escrows amount and initiates a credit transfer. It
// returns the globally unique id used for all retries; the transfer itself
// completes asynchronously when the ack is durably recorded.
func (r *Replica) Transfer(target, amount int64) (TransferID, error) {
	if r.down {
		return TransferID{}, ErrReplicaDown
	}
	if amount <= 0 {
		return TransferID{}, ErrInvalidAmount
	}
	if amount > r.balance {
		return TransferID{}, ErrOverdraft
	}
	if target == r.id {
		return TransferID{}, ErrNoSelfTransfer
	}
	if to, ok := r.sim.replicas[target]; !ok || to == nil {
		return TransferID{}, ErrUnknownReplica
	}
	id := TransferID{From: r.id, Seq: r.nextSeq + 1}
	r.persist(op{Kind: opOpen, ID: id, Peer: target, Amount: amount})
	r.sim.logf("R%d -> R%d transfer %d opened (id %d#%d)", r.id, target, amount, id.From, id.Seq)
	r.sendTransfer(id, target, amount)
	r.sim.After(r.ResendInterval, func(int64) { r.tick(id) })
	return id, nil
}

func (r *Replica) sendTransfer(id TransferID, target, amount int64) {
	r.sim.Send(Message{
		Kind:   MsgTransfer,
		ID:     id,
		Amount: amount,
		Sender: r.id,
		Target: target,
	})
}

func (r *Replica) sendAck(id TransferID, to int64) {
	r.sim.Send(Message{
		Kind:   MsgAck,
		ID:     id,
		Sender: r.id,
		Target: to,
	})
}

// tick is the timeout handler for one open transfer.
func (r *Replica) tick(id TransferID) {
	if r.down {
		return
	}
	p, ok := r.pending[id]
	if !ok {
		return // already completed; timer is garbage
	}
	p.retries++
	r.sim.logf("R%d timeout: resending transfer %d#%d -> R%d (attempt %d)",
		r.id, id.From, id.Seq, p.target, p.retries+1)
	r.sendTransfer(id, p.target, p.amount)
	r.sim.After(r.ResendInterval, func(int64) { r.tick(id) })
}

// handle processes one delivered message. It is single-threaded with all
// timers, so the durable check + record sequence is atomic relative to every
// other state transition.
func (r *Replica) handle(m Message) {
	switch m.Kind {
	case MsgTransfer:
		if r.disk.Seen(m.ID) {
			// Retried or network-duplicated copy. No credit is created:
			// just help the sender finish by echoing the ack again.
			r.transferDups++
			r.sim.logf("R%d DUP transfer %d#%d from R%d: no new credit, re-acking",
				r.id, m.ID.From, m.ID.Seq, m.Sender)
			r.sendAck(m.ID, m.Sender)
			return
		}
		// First delivery: credit exactly once, durably keyed by the id.
		r.persist(op{Kind: opDeliver, ID: m.ID, Peer: m.Sender, Amount: m.Amount})
		r.sim.logf("R%d credited %d from R%d (id %d#%d, balance %d)",
			r.id, m.Amount, m.Sender, m.ID.From, m.ID.Seq, r.balance)
		r.sendAck(m.ID, m.Sender)
	case MsgAck:
		if r.disk.Seen(m.ID) {
			r.ackDups++
			r.sim.logf("R%d DUP ack for %d#%d from R%d: ignored",
				r.id, m.ID.From, m.ID.Seq, m.Sender)
			return
		}
		p, ok := r.pending[m.ID]
		if !ok {
			// Ack for a transfer this replica never opened: stale garbage.
			return
		}
		// Final step: escrow is finalized. The units are already absent
		// from the sender's balance, so only the pending entry disappears.
		r.persist(op{Kind: opAck, ID: m.ID, Peer: m.Sender, Amount: p.amount})
		r.sim.logf("R%d acked transfer %d#%d by R%d (complete)", r.id, m.ID.From, m.ID.Seq, m.Sender)
	}
}
