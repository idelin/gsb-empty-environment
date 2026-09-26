package sim

// Durable ledger records, one per committed state transition. A transition
// is appended and fsynced before any external effect (sending a message or
// returning success), which gives the atomic write-ahead-log semantics of a
// real ledger. In the simulator "disk" survives crashes while process memory
// does not; nothing else persists across a Crash.
type opType int

const (
	opGenesis opType = iota // initial credit granted from the global quota
	opSpend                 // local offline deduction
	opOpen                  // transfer initiated: funds move to escrow
	opDeliver               // receiver credited (durable dedup key added)
	opAck                   // sender closes the transfer (escrow finalized)
)

type op struct {
	Seq    int64
	Kind   opType
	ID     TransferID
	Peer   int64
	Amount int64
}

// Disk is a fsync-able write-ahead log plus a durable dedup table.
type Disk struct {
	records []op
	// seen stores ids of deliver/ack records only. An open record must not
	// occupy the key: after a crash the sender replays its log and still
	// needs to accept the ack for that same id.
	seen map[TransferID]bool
}

func newDisk() *Disk { return &Disk{seen: make(map[TransferID]bool)} }

// Append atomically commits one record and marks it durable.
func (d *Disk) Append(o op) {
	o.Seq = int64(len(d.records)) + 1
	d.records = append(d.records, o)
	if o.Kind == opDeliver || o.Kind == opAck {
		d.seen[o.ID] = true
	}
}

// Fsync models the durability barrier. In a single-process simulator the
// record is already in heap memory that survives Crash (only process state is
// wiped); this hook makes the write-ahead-log ordering explicit.
func (d *Disk) Fsync() {}

// Records returns a copy of the durable log, in commit order.
func (d *Disk) Records() []op {
	out := make([]op, len(d.records))
	copy(out, d.records)
	return out
}

// Seen reports whether an id is already durably delivered or acked.
func (d *Disk) Seen(id TransferID) bool { return d.seen[id] }
