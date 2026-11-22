package takeover

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// JournalPhase is the durable phase of one control attempt.
type JournalPhase string

const (
	JournalPrepared   JournalPhase = "prepared"
	JournalDispatched JournalPhase = "dispatched"
	JournalConfirmed  JournalPhase = "confirmed"
	JournalInDoubt    JournalPhase = "in_doubt"
	JournalFailed     JournalPhase = "failed"
)

// JournalRecord is the write-ahead state for one operation.
type JournalRecord struct {
	OperationID string         `json:"operation_id"`
	Request     ControlRequest `json:"request"`
	Phase       JournalPhase   `json:"phase"`
	Attempts    uint32         `json:"attempts"`
	Receipt     EffectReceipt  `json:"receipt,omitempty"`
	Detail      string         `json:"detail,omitempty"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// Journal is a small durable map keyed by operation ID.
type Journal interface {
	Put(record JournalRecord) error
	Get(operationID string) (JournalRecord, bool, error)
	List() ([]JournalRecord, error)
}

// MemoryJournal is a concurrency-safe in-memory Journal.
type MemoryJournal struct {
	mu      sync.Mutex
	records map[string]JournalRecord
}

// NewMemoryJournal constructs an empty journal.
func NewMemoryJournal() *MemoryJournal {
	return &MemoryJournal{records: make(map[string]JournalRecord)}
}

// Put stores a record.
func (j *MemoryJournal) Put(record JournalRecord) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.records == nil {
		j.records = make(map[string]JournalRecord)
	}
	j.records[record.OperationID] = record
	return nil
}

// Get loads a record.
func (j *MemoryJournal) Get(operationID string) (JournalRecord, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	record, ok := j.records[operationID]
	return record, ok, nil
}

// List returns records sorted by operation ID.
func (j *MemoryJournal) List() ([]JournalRecord, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]JournalRecord, 0, len(j.records))
	for _, record := range j.records {
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OperationID < out[j].OperationID })
	return out, nil
}

// SignalTransport delivers a control attempt and exposes the receiver's
// idempotent receipt lookup. Deliver must be safe to repeat with the same
// OperationID.
type SignalTransport interface {
	Deliver(ctx context.Context, request ControlRequest) (EffectReceipt, error)
	Lookup(ctx context.Context, operationID string) (EffectReceipt, bool, error)
}

// ControlGuard is the local authorization side of emission. Host implements
// it.
type ControlGuard interface {
	AuthorizeControl(request ControlRequest) (EffectReceipt, error)
	RecordEffect(receipt EffectReceipt) error
}

// RecoveryPolicy controls what may be retried after a restart. A dispatched
// attempt is never re-sent by default because the local process cannot prove
// that the signal did not already reach the receiver.
type RecoveryPolicy struct {
	RetryPrepared bool
	RetryInDoubt  bool
}

// DefaultRecoveryPolicy retries only attempts that were durably prepared but
// not dispatched.
func DefaultRecoveryPolicy() RecoveryPolicy {
	return RecoveryPolicy{RetryPrepared: true, RetryInDoubt: false}
}

// RecoveryEntry describes one operation examined during recovery.
type RecoveryEntry struct {
	OperationID string        `json:"operation_id"`
	Phase       JournalPhase  `json:"phase"`
	Recovered   bool          `json:"recovered"`
	InDoubt     bool          `json:"in_doubt"`
	Receipt     EffectReceipt `json:"receipt,omitempty"`
	Err         error         `json:"-"`
}

// RecoveryReport summarizes recovery.
type RecoveryReport struct {
	Scanned   int             `json:"scanned"`
	Confirmed int             `json:"confirmed"`
	Recovered int             `json:"recovered"`
	InDoubt   int             `json:"in_doubt"`
	Entries   []RecoveryEntry `json:"entries"`
}

// Emitter is a write-ahead signal emitter. It journals intent before
// authorizing locally and before handing the signal to the transport.
type Emitter struct {
	journal   Journal
	transport SignalTransport
	guard     ControlGuard
	policy    RecoveryPolicy
	now       func() time.Time

	mu      sync.Mutex
	counter uint64
}

// EmitterOption configures an Emitter.
type EmitterOption func(*Emitter)

// WithRecoveryPolicy sets the restart policy.
func WithRecoveryPolicy(policy RecoveryPolicy) EmitterOption {
	return func(emitter *Emitter) { emitter.policy = policy }
}

// WithEmitterClock overrides the clock.
func WithEmitterClock(now func() time.Time) EmitterOption {
	return func(emitter *Emitter) { emitter.now = now }
}

// NewEmitter constructs an emitter. guard may be nil when the caller performs
// authorization elsewhere.
func NewEmitter(journal Journal, transport SignalTransport, guard ControlGuard, options ...EmitterOption) (*Emitter, error) {
	if journal == nil {
		return nil, fmt.Errorf("takeover: nil journal")
	}
	if transport == nil {
		return nil, fmt.Errorf("takeover: nil transport")
	}
	emitter := &Emitter{
		journal:   journal,
		transport: transport,
		guard:     guard,
		policy:    DefaultRecoveryPolicy(),
		now:       time.Now,
	}
	for _, option := range options {
		if option != nil {
			option(emitter)
		}
	}
	return emitter, nil
}

// Emit sends one control attempt. It first queries the journal so a repeated
// call for the same OperationID cannot consume another sequence.
func (e *Emitter) Emit(ctx context.Context, request ControlRequest) (EffectReceipt, error) {
	if request.OperationID == "" {
		request.OperationID = e.newOperationID()
	}
	record, exists, err := e.journal.Get(request.OperationID)
	if err != nil {
		return EffectReceipt{}, err
	}
	if exists {
		if record.Request.GrantID != request.GrantID ||
			record.Request.ProcessStartID != request.ProcessStartID ||
			record.Request.SessionRoot != request.SessionRoot ||
			record.Request.Sequence != request.Sequence ||
			record.Request.Signal != request.Signal ||
			(!request.Dimensions.IsEmpty() && !record.Request.Dimensions.Equal(request.Dimensions)) {
			return EffectReceipt{}, ErrReceiptConflict
		}
		switch record.Phase {
		case JournalConfirmed:
			return record.Receipt, nil
		case JournalInDoubt:
			if receipt, ok, lookupErr := e.lookup(ctx, request.OperationID); lookupErr == nil && ok {
				return e.confirm(record, receipt)
			}
			return EffectReceipt{}, ErrDispatchInDoubt
		case JournalPrepared:
			return e.dispatchPrepared(ctx, record)
		case JournalDispatched:
			receipt, ok, lookupErr := e.lookup(ctx, request.OperationID)
			if lookupErr == nil && ok {
				return e.confirm(record, receipt)
			}
			return EffectReceipt{}, ErrDispatchInDoubt
		default:
			return EffectReceipt{}, ErrDispatchInDoubt
		}
	}

	record = JournalRecord{
		OperationID: request.OperationID,
		Request:     request,
		Phase:       JournalPrepared,
		UpdatedAt:   e.now(),
	}
	if err := e.journal.Put(record); err != nil {
		return EffectReceipt{}, err
	}
	return e.dispatchPrepared(ctx, record)
}

func (e *Emitter) dispatchPrepared(ctx context.Context, record JournalRecord) (EffectReceipt, error) {
	if e.guard != nil {
		local, err := e.guard.AuthorizeControl(record.Request)
		if err != nil {
			record.Phase = JournalFailed
			record.Detail = err.Error()
			record.UpdatedAt = e.now()
			_ = e.journal.Put(record)
			return EffectReceipt{}, err
		}
		_ = local
	}

	record.Phase = JournalDispatched
	record.Attempts++
	record.UpdatedAt = e.now()
	if err := e.journal.Put(record); err != nil {
		return EffectReceipt{}, err
	}

	receipt, deliverErr := e.transport.Deliver(ctx, record.Request)
	if deliverErr != nil {
		if receipt.OperationID != "" {
			if _, confirmErr := e.confirm(record, receipt); confirmErr == nil {
				return receipt, nil
			}
		}
		// Delivery errors are ambiguous: the receiver may have observed the
		// signal. Query once, then park the operation as in-doubt rather than
		// replaying it.
		if lookedUp, ok, lookupErr := e.lookup(ctx, record.OperationID); lookupErr == nil && ok {
			if confirmed, confirmErr := e.confirm(record, lookedUp); confirmErr == nil {
				return confirmed, nil
			}
		}
		record.Phase = JournalInDoubt
		record.Detail = deliverErr.Error()
		record.UpdatedAt = e.now()
		_ = e.journal.Put(record)
		return EffectReceipt{}, fmt.Errorf("%w: %v", ErrDispatchInDoubt, deliverErr)
	}
	return e.confirm(record, receipt)
}

func (e *Emitter) confirm(record JournalRecord, receipt EffectReceipt) (EffectReceipt, error) {
	if e.guard != nil {
		if err := e.guard.RecordEffect(receipt); err != nil && err != ErrEffectTransition {
			return EffectReceipt{}, err
		}
	}
	if receipt.Final() {
		record.Phase = JournalConfirmed
		record.Detail = ""
	} else {
		// The receiver accepted the operation but has not settled the effect.
		// Keep the record dispatched so recovery queries the receipt rather
		// than sending the signal again.
		record.Phase = JournalDispatched
		record.Detail = "effect pending"
	}
	record.Receipt = receipt
	record.UpdatedAt = e.now()
	if err := e.journal.Put(record); err != nil {
		return EffectReceipt{}, err
	}
	return receipt, nil
}

func (e *Emitter) lookup(ctx context.Context, operationID string) (EffectReceipt, bool, error) {
	return e.transport.Lookup(ctx, operationID)
}

// Recover scans the journal after a restart. Prepared attempts may be sent;
// dispatched or in-doubt attempts are only queried unless the policy opts into
// an unsafe replay.
func (e *Emitter) Recover(ctx context.Context) (RecoveryReport, error) {
	records, err := e.journal.List()
	if err != nil {
		return RecoveryReport{}, err
	}
	report := RecoveryReport{}
	for _, record := range records {
		report.Scanned++
		entry := RecoveryEntry{OperationID: record.OperationID, Phase: record.Phase}
		switch record.Phase {
		case JournalConfirmed:
			report.Confirmed++
			entry.Receipt = record.Receipt
			entry.Recovered = true
		case JournalPrepared:
			if !e.policy.RetryPrepared {
				entry.InDoubt = true
				report.InDoubt++
				break
			}
			receipt, emitErr := e.dispatchPrepared(ctx, record)
			entry.Err = emitErr
			if emitErr == nil {
				entry.Receipt = receipt
				if receipt.Final() {
					entry.Phase = JournalConfirmed
					entry.Recovered = true
					report.Recovered++
				} else {
					entry.Phase = JournalDispatched
					entry.InDoubt = true
					report.InDoubt++
				}
			} else if errors.Is(emitErr, ErrDispatchInDoubt) {
				entry.InDoubt = true
				report.InDoubt++
			}
		case JournalDispatched, JournalInDoubt:
			receipt, ok, lookupErr := e.lookup(ctx, record.OperationID)
			switch {
			case lookupErr != nil:
				entry.Err = lookupErr
				entry.InDoubt = true
				report.InDoubt++
			case ok:
				_, confirmErr := e.confirm(record, receipt)
				entry.Err = confirmErr
				if confirmErr == nil {
					entry.Receipt = receipt
					if receipt.Final() {
						entry.Phase = JournalConfirmed
						entry.Recovered = true
						report.Recovered++
					} else {
						entry.InDoubt = true
						report.InDoubt++
					}
				}
			case e.policy.RetryInDoubt:
				retry := record
				retry.Phase = JournalPrepared
				receipt, emitErr := e.dispatchPrepared(ctx, retry)
				entry.Err = emitErr
				if emitErr == nil {
					entry.Receipt = receipt
					if receipt.Final() {
						entry.Phase = JournalConfirmed
						entry.Recovered = true
						report.Recovered++
					} else {
						entry.Phase = JournalDispatched
						entry.InDoubt = true
						report.InDoubt++
					}
				} else {
					entry.InDoubt = true
					report.InDoubt++
				}
			default:
				record.Phase = JournalInDoubt
				record.UpdatedAt = e.now()
				_ = e.journal.Put(record)
				entry.Phase = JournalInDoubt
				entry.InDoubt = true
				report.InDoubt++
			}
		case JournalFailed:
			entry.InDoubt = true
			report.InDoubt++
		}
		report.Entries = append(report.Entries, entry)
	}
	return report, nil
}

func (e *Emitter) newOperationID() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.counter++
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Sprintf("op-%d-%d", e.now().UnixNano(), e.counter)
	}
	return fmt.Sprintf("%d-%s", e.counter, hex.EncodeToString(random[:]))
}
