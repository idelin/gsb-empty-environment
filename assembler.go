package pgoutput

import (
	"errors"
	"fmt"
	"time"
)

// Transaction is a fully assembled, committed logical replication transaction.
// Transactions are handed to the consumer only after Commit / Stream Commit,
// in the order their commit messages arrive on the wire.
type Transaction struct {
	XID        uint32
	CommitLSN  LSN
	EndLSN     LSN
	CommitTime time.Time
	Origin     string
	Changes    []Change
	// Messages are transactional logical decoding messages ('M').
	Messages []LogicalMessage
}

// streamMessage is a transactional logical message buffered inside a stream.
type streamMessage struct {
	subXID uint32
	msg    LogicalMessage
}

// streamBuf holds one in-flight streamed transaction across all of its
// segments. Changes are appended in wire order and tagged with the
// subtransaction (or 0 for the top-level transaction) that produced them.
type streamBuf struct {
	xid      uint32
	changes  []Change
	messages []streamMessage
	// subChanges maps subXID -> indices into changes, zero meaning top-level.
	subChanges map[uint32][]int
}

func newStreamBuf(xid uint32) *streamBuf {
	return &streamBuf{
		xid:        xid,
		subChanges: make(map[uint32][]int),
	}
}

// txnState is a fully received but uncommitted non-streamed transaction.
type txnState struct {
	xid        uint32
	finalLSN   LSN
	commitTime time.Time
	origin     string
	changes    []Change
	messages   []LogicalMessage
}

// Result contains everything published by one Assembler.Handle call. A single
// wire message can finalize at most one transaction, but transactions stay
// queued in commit order and are drained together.
type Result struct {
	// Transactions are committed transactions, strictly in commit (wire) order.
	Transactions []*Transaction
	// Messages are non-transactional logical decoding messages ('m').
	Messages []LogicalMessage
	// Keepalives are primary keepalive submessages ('k').
	Keepalives []Keepalive
}

// Assembler decodes pgoutput messages into committed Transactions. It is
// intended for a single replication connection: feed messages sequentially.
//
// Guarantees:
//   - Relation ('R') and Type ('Y') messages are cached and used to resolve
//     and validate every subsequent row change.
//   - Unchanged TOAST columns ('u') in old tuples are merged from the new row
//     and never surface as NULL.
//   - Streamed transactions (protocol v2, plus v4 subtransaction streams) are
//     buffered across Stream Start/Stop segments until Stream Commit; Stream
//     Abort drops the whole transaction or only the aborted subtransaction.
//   - Transactions are published only after commit, in commit order.
//   - SafeLSN never advances past an unpublished transaction, so a consumer
//     cannot acknowledge WAL that would be replayed after a reconnect.
type Assembler struct {
	catalog *Catalog

	// non-streamed open transaction
	current *txnState

	// streamed transactions keyed by top-level XID
	streams map[uint32]*streamBuf
	// activeXID is the XID of the stream currently receiving row messages,
	// zero when no stream segment is open.
	activeXID uint32
	// activeSubXID is the subtransaction context of the active segment
	// (0 = top-level).
	activeSubXID uint32

	// committed but not yet returned to the consumer
	ready []*Transaction
	// highest EndLSN of a transaction already published to the consumer
	publishedLSN LSN
}

// NewAssembler creates an Assembler with an empty catalog.
func NewAssembler() *Assembler {
	return &Assembler{
		catalog: NewCatalog(),
		streams: make(map[uint32]*streamBuf),
	}
}

// Catalog exposes the relation/type cache (for inspection or warm-up).
func (a *Assembler) Catalog() *Catalog { return a.catalog }

// SafeLSN returns the largest LSN that may be acknowledged back to the
// server. It is the EndLSN of the last transaction already published to the
// consumer; LSNs belonging to transactions still being assembled are held
// back even if a keepalive advertises a higher value.
func (a *Assembler) SafeLSN() LSN { return a.publishedLSN }

// CheckAck reports whether acknowledging lsn is safe. An LSN at or below
// SafeLSN is safe; anything higher would skip an unpublished transaction.
func (a *Assembler) CheckAck(lsn LSN) error {
	if lsn > a.SafeLSN() {
		return fmt.Errorf(
			"pgoutput: refusing to acknowledge %s: safe LSN is %s, unpublished transactions exist",
			lsn, a.SafeLSN())
	}
	return nil
}

// Handle processes one decoded pgoutput message (as returned by Parse) and
// returns any transactions or out-of-band messages it publishes.
func (a *Assembler) Handle(msg any) (*Result, error) {
	res := &Result{}
	var err error
	switch m := msg.(type) {
	case Relation:
		a.catalog.AddRelation(m)
	case Type:
		a.catalog.AddType(m)
	case Begin:
		err = a.handleBegin(m)
	case Commit:
		err = a.handleCommit(m)
	case Origin:
		err = a.handleOrigin(m)
	case Insert:
		err = a.handleInsert(m)
	case Update:
		err = a.handleUpdate(m)
	case Delete:
		err = a.handleDelete(m)
	case Truncate:
		err = a.handleTruncate(m)
	case LogicalMessage:
		err = a.handleLogicalMessage(m, res)
	case StreamStart:
		err = a.handleStreamStart(m)
	case StreamStop:
		err = a.handleStreamStop()
	case StreamCommit:
		err = a.handleStreamCommit(m)
	case StreamAbort:
		err = a.handleStreamAbort(m)
	case Keepalive:
		res.Keepalives = append(res.Keepalives, m)
	default:
		err = fmt.Errorf("pgoutput: assembler cannot handle message type %T", msg)
	}
	if err != nil {
		return nil, err
	}
	if len(a.ready) > 0 {
		res.Transactions = a.ready
		for _, txn := range a.ready {
			a.publishedLSN = txn.EndLSN
		}
		a.ready = nil
	}
	return res, nil
}

func (a *Assembler) handleBegin(b Begin) error {
	if a.current != nil {
		return fmt.Errorf("pgoutput: Begin for XID %d while XID %d is still open", b.XID, a.current.xid)
	}
	if a.activeXID != 0 {
		return fmt.Errorf("pgoutput: Begin for XID %d interleaves active stream of XID %d", b.XID, a.activeXID)
	}
	a.current = &txnState{
		xid:        b.XID,
		finalLSN:   b.FinalLSN,
		commitTime: b.CommitTime,
	}
	return nil
}

func (a *Assembler) handleCommit(c Commit) error {
	if a.current == nil {
		return fmt.Errorf("pgoutput: Commit without an open transaction (LSN %s)", c.EndLSN)
	}
	txn := &Transaction{
		XID:        a.current.xid,
		CommitLSN:  c.CommitLSN,
		EndLSN:     c.EndLSN,
		CommitTime: c.CommitTime,
		Origin:     a.current.origin,
		Changes:    a.current.changes,
		Messages:   a.current.messages,
	}
	a.current = nil
	a.ready = append(a.ready, txn)
	return nil
}

func (a *Assembler) handleOrigin(o Origin) error {
	if a.current == nil {
		return fmt.Errorf("pgoutput: Origin %q received outside of a transaction", o.Name)
	}
	a.current.origin = o.Name
	return nil
}

func (a *Assembler) relationFor(id uint32) (*Relation, error) {
	rel, ok := a.catalog.Relation(id)
	if !ok {
		return nil, fmt.Errorf("pgoutput: unknown relation ID %d (no Relation message seen)", id)
	}
	return &rel, nil
}

func (a *Assembler) handleInsert(in Insert) error {
	rel, err := a.relationFor(in.RelationID)
	if err != nil {
		return err
	}
	if len(in.Row) != len(rel.Columns) {
		return fmt.Errorf("pgoutput: insert tuple width %d != relation %s column count %d",
			len(in.Row), rel.NamespacedName(), len(rel.Columns))
	}
	return a.appendChange(Change{Op: OpInsert, Relation: rel, New: in.Row})
}

func (a *Assembler) handleUpdate(u Update) error {
	rel, err := a.relationFor(u.RelationID)
	if err != nil {
		return err
	}
	if len(u.New) != len(rel.Columns) {
		return fmt.Errorf("pgoutput: update new tuple width %d != relation %s column count %d",
			len(u.New), rel.NamespacedName(), len(rel.Columns))
	}
	ch := Change{Op: OpUpdate, Relation: rel}
	if u.Old != nil {
		old := u.Old
		if u.OldKey {
			old = expandKeyTuple(rel, old)
		}
		ch.Old, err = mergeToast(rel, old, u.New)
		if err != nil {
			return err
		}
	}
	ch.New = u.New
	return a.appendChange(ch)
}

func (a *Assembler) handleDelete(d Delete) error {
	rel, err := a.relationFor(d.RelationID)
	if err != nil {
		return err
	}
	old := d.Old
	if d.OldKey {
		old = expandKeyTuple(rel, old)
	}
	if len(old) != len(rel.Columns) {
		return fmt.Errorf("pgoutput: delete tuple width %d != relation %s column count %d",
			len(old), rel.NamespacedName(), len(rel.Columns))
	}
	return a.appendChange(Change{Op: OpDelete, Relation: rel, Old: old})
}

func (a *Assembler) handleTruncate(t Truncate) error {
	for _, id := range t.RelationIDs {
		rel, err := a.relationFor(id)
		if err != nil {
			return err
		}
		if err := a.appendChange(Change{Op: OpTruncate, Relation: rel}); err != nil {
			return err
		}
	}
	return nil
}

func (a *Assembler) handleLogicalMessage(m LogicalMessage, res *Result) error {
	if !m.Transactional {
		res.Messages = append(res.Messages, m)
		return nil
	}
	if a.activeXID != 0 {
		buf := a.streams[a.activeXID]
		buf.messages = append(buf.messages, streamMessage{subXID: a.activeSubXID, msg: m})
		return nil
	}
	if a.current == nil {
		return fmt.Errorf("pgoutput: transactional logical message %q outside of a transaction", m.Prefix)
	}
	a.current.messages = append(a.current.messages, m)
	return nil
}

var errNoOpenStream = errors.New("pgoutput: Stream Stop without an open stream segment")
