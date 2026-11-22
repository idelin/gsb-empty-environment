package lease

import (
	"errors"
	"sync"
)

// ErrRecordNotFound is returned when a delivery record is unknown to the
// receiver's persistent store.
var ErrRecordNotFound = errors.New("lease: unknown delivery record")

// RecordKey identifies one logical datagram for the commit protocol. The
// business identity is (tenant, authorization, logical sequence); it is
// independent of connection, stream and generation so that dual-path
// retransmissions collapse onto the same record.
type RecordKey struct {
	Tenant TenantID
	Auth   AuthID
	Seq    uint64
}

// DeliveryRecord is one logical datagram handed to the business side.
type DeliveryRecord struct {
	Key     RecordKey
	Payload []byte
}

// CommitState is the three-state result of the acknowledgement protocol
// that joins the receiver's persistent state with business delivery.
type CommitState int

const (
	// StatePending means the record is staged locally and the protocol
	// is still progressing (delivery or commit may be retried).
	StatePending CommitState = iota
	// StateCommitted means the host's idempotent inbox gave a definite
	// answer and the local store marked the record complete.
	StateCommitted
	// StateUnknown means a crash between delivery and receipt left the
	// outcome indeterminate: the inbox has not (yet) given a definite
	// answer, so the record must not be treated as complete.
	StateUnknown
)

func (s CommitState) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateCommitted:
		return "committed"
	case StateUnknown:
		return "unknown"
	}
	return "invalid"
}

// LocalState is the persisted receiver-side state of a record.
type LocalState int

const (
	// LocalStaged means the record is durably staged but not committed.
	LocalStaged LocalState = iota
	// LocalCommitted means the record is durably complete.
	LocalCommitted
)

// Store is the receiver's persistent write-ahead state. Implementations
// must survive process crashes; every method is a persistent write point.
type Store interface {
	// PutStaged durably stages a record (upsert by key).
	PutStaged(rec DeliveryRecord) error
	// MarkCommitted durably marks a staged record complete.
	MarkCommitted(key RecordKey) error
	// State returns the local state of a record.
	State(key RecordKey) (LocalState, bool, error)
	// Staged lists all records that are not yet committed.
	Staged() ([]DeliveryRecord, error)
}

// Inbox is the host's idempotent business inbox. Delivery and receipt
// both flow through it; it is the only source of definite answers after
// a crash between delivery and receipt.
type Inbox interface {
	// Put delivers a record idempotently. already is true when the
	// record was definitely present before this call. An error means
	// the outcome is indeterminate and the call should be retried.
	Put(rec DeliveryRecord) (already bool, err error)
	// Lookup returns the stored record and true when it is definitely
	// present, or false when it is definitely absent. An error means
	// the inbox cannot give a definite answer right now.
	Lookup(key RecordKey) (DeliveryRecord, bool, error)
}

// Receiver runs the retryable acknowledgement protocol between the
// persistent Store and the host's idempotent Inbox.
type Receiver struct {
	store Store
	inbox Inbox

	mu sync.Mutex
	// unknown holds keys whose last reconciliation attempt could not
	// obtain a definite answer from the inbox.
	unknown map[RecordKey]struct{}
}

// NewReceiver wires a Receiver to durable state and the host inbox.
func NewReceiver(store Store, inbox Inbox) *Receiver {
	return &Receiver{store: store, inbox: inbox, unknown: make(map[RecordKey]struct{})}
}

// Deliver stages a record, delivers it to the business inbox and commits
// it locally. The three persistent write points are, in order:
// Store.PutStaged, Inbox.Put, Store.MarkCommitted. A failure leaves the
// record pending; Deliver and Reconcile are safe to retry, and a record
// that is already committed is a no-op, so duplicates never deliver the
// same payload twice.
func (r *Receiver) Deliver(rec DeliveryRecord) error {
	st, ok, err := r.store.State(rec.Key)
	if err != nil {
		return err
	}
	if ok && st == LocalCommitted {
		return nil
	}
	if err := r.store.PutStaged(rec); err != nil {
		return err
	}
	if _, err := r.inbox.Put(rec); err != nil {
		return err
	}
	if err := r.store.MarkCommitted(rec.Key); err != nil {
		return err
	}
	r.clearUnknown(rec.Key)
	return nil
}

// Reconcile walks every staged (not committed) record and asks the inbox
// for a definite answer. A definite hit commits the record; a definite
// miss redelivers it and commits; an indeterminate answer marks the
// record StateUnknown and leaves it for a later retry. Only a definite
// inbox answer can move a crashed record to StateCommitted.
func (r *Receiver) Reconcile() error {
	recs, err := r.store.Staged()
	if err != nil {
		return err
	}
	var firstErr error
	for _, rec := range recs {
		_, found, err := r.inbox.Lookup(rec.Key)
		if err != nil {
			r.markUnknown(rec.Key)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !found {
			if _, err := r.inbox.Put(rec); err != nil {
				r.markUnknown(rec.Key)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
		}
		if err := r.store.MarkCommitted(rec.Key); err != nil {
			return err
		}
		r.clearUnknown(rec.Key)
	}
	return firstErr
}

// State reports the three-state view of a record: committed only when
// the local store says so, unknown when a reconciliation attempt failed
// to get a definite inbox answer, pending otherwise.
func (r *Receiver) State(key RecordKey) (CommitState, error) {
	st, ok, err := r.store.State(key)
	if err != nil {
		return StatePending, err
	}
	if !ok {
		return StatePending, ErrRecordNotFound
	}
	if st == LocalCommitted {
		return StateCommitted, nil
	}
	r.mu.Lock()
	_, unknown := r.unknown[key]
	r.mu.Unlock()
	if unknown {
		return StateUnknown, nil
	}
	return StatePending, nil
}

// Confirm applies a business control receipt. Receipts are idempotent:
// confirming an already committed record is a no-op, so duplicate
// control receipts never cause a second delivery or an error.
func (r *Receiver) Confirm(key RecordKey) error {
	st, ok, err := r.store.State(key)
	if err != nil {
		return err
	}
	if !ok {
		return ErrRecordNotFound
	}
	if st == LocalCommitted {
		return nil
	}
	if err := r.store.MarkCommitted(key); err != nil {
		return err
	}
	r.clearUnknown(key)
	return nil
}

func (r *Receiver) markUnknown(key RecordKey) {
	r.mu.Lock()
	r.unknown[key] = struct{}{}
	r.mu.Unlock()
}

func (r *Receiver) clearUnknown(key RecordKey) {
	r.mu.Lock()
	delete(r.unknown, key)
	r.mu.Unlock()
}
