package netsec

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"
)

// LedgerConfig configures a Ledger. Zero fields use safe defaults.
type LedgerConfig struct {
	// Clock is used for deadline checks. It defaults to time.Now.
	Clock func() time.Time
	// BatchID returns a fresh, unpredictable identifier. It defaults to a
	// cryptographically random 128-bit value.
	BatchID func() (string, error)
	// MaxBatchSize limits the number of rows in one batch. Zero means no
	// ledger-imposed limit.
	MaxBatchSize int
}

// Ledger is a concurrency-safe batch ledger. It owns request identity, row
// metadata, delivery eligibility, and model-generation state. A callback only
// ever receives copies of this state.
type Ledger struct {
	mu sync.Mutex

	clock         func() time.Time
	newBatchID    func() (string, error)
	maxBatchSize  int
	nextArrival   uint64
	entries       map[requestKey]*ledgerEntry
	pending       []*ledgerEntry
	batches       map[string]*batchState
	currentGen    uint64
	generationSet bool
}

type requestKey struct {
	tenantID  string
	requestID string
}

type entryStatus uint8

const (
	entryPending entryStatus = iota
	entryBatched
	entryDelivered
	entryCanceled
	entryExpired
	entryInvalid
)

type ledgerEntry struct {
	key      requestKey
	request  Request
	arrival  uint64
	status   entryStatus
	batchID  string
	rowIndex int
	expired  bool
}

type rowState struct {
	tokens          []uint32
	done            bool
	deliveredTokens []uint32
	deliveredDone   bool
	everDelivered   bool
}

type batchState struct {
	batch   Batch
	entries []*ledgerEntry
	rows    []rowState

	invalid          bool
	nonStreamStarted bool
	streaming        bool
	streamStep       uint64
	lastStreamDigest string
}

// NewLedger creates a ledger. Multiple configs are accepted for convenience;
// later configs override earlier non-zero fields.
func NewLedger(configs ...LedgerConfig) *Ledger {
	l := &Ledger{
		clock:      time.Now,
		newBatchID: randomBatchID,
		entries:    make(map[requestKey]*ledgerEntry),
		batches:    make(map[string]*batchState),
	}
	for _, cfg := range configs {
		if cfg.Clock != nil {
			l.clock = cfg.Clock
		}
		if cfg.BatchID != nil {
			l.newBatchID = cfg.BatchID
		}
		if cfg.MaxBatchSize > 0 {
			l.maxBatchSize = cfg.MaxBatchSize
		}
	}
	return l
}

// NewBatchLedger is an alias for NewLedger.
func NewBatchLedger(configs ...LedgerConfig) *Ledger { return NewLedger(configs...) }

// NewLedgerWithClock creates a ledger with a deterministic clock. It is
// primarily useful for tests and scheduling searches.
func NewLedgerWithClock(clock func() time.Time) *Ledger {
	return NewLedger(LedgerConfig{Clock: clock})
}

// Submit validates and enqueues a request. A tenant-local request ID cannot
// refer to two simultaneously active requests. Reusing an ID after a terminal
// state requires a newer cancellation generation.
func (l *Ledger) Submit(req Request) error {
	req, err := normalizeRequest(req)
	if err != nil {
		return err
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	key := requestKey{tenantID: req.TenantID, requestID: req.RequestID}
	if old, ok := l.entries[key]; ok {
		if !isTerminal(old.status) || req.CancelGeneration <= old.request.CancelGeneration {
			return fmt.Errorf("%w: tenant=%q request=%q", ErrDuplicateRequest, key.tenantID, key.requestID)
		}
	}

	l.nextArrival++
	arrival := req.ArrivalSequence
	if arrival == 0 {
		arrival = l.nextArrival
	}
	entry := &ledgerEntry{
		key:      key,
		request:  req,
		arrival:  arrival,
		status:   entryPending,
		rowIndex: -1,
	}
	l.entries[key] = entry
	l.pending = append(l.pending, entry)
	// Once a host has declared an active generation, submitting a newer one is
	// a model switch. Without a declared generation, mixed generations remain
	// queueable and are still batched separately.
	if l.generationSet && req.ModelGeneration > l.currentGen {
		l.setGenerationLocked(req.ModelGeneration)
	}
	return nil
}

// SubmitRequest is an alias for Submit.
func (l *Ledger) SubmitRequest(req Request) error { return l.Submit(req) }

// NextBatch forms and reserves one canonical batch. It returns ErrNoPendingRequest
// when there is no eligible pending request.
func (l *Ledger) NextBatch() (Batch, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock()
	l.expirePendingLocked(now)

	candidates := make([]*ledgerEntry, 0, len(l.pending))
	for _, entry := range l.pending {
		if entry.status != entryPending {
			continue
		}
		if l.generationSet && entry.request.ModelGeneration != l.currentGen {
			entry.status = entryInvalid
			continue
		}
		candidates = append(candidates, entry)
	}
	if len(candidates) == 0 {
		return Batch{}, ErrNoPendingRequest
	}
	sort.Slice(candidates, func(i, j int) bool {
		return lessEntry(candidates[i], candidates[j])
	})
	first := candidates[0]

	group := make([]*ledgerEntry, 0, len(l.pending))
	for _, entry := range candidates {
		if entry.request.ModelGeneration != first.request.ModelGeneration ||
			entry.request.DecodeStrategy != first.request.DecodeStrategy {
			continue
		}
		group = append(group, entry)
	}
	if l.maxBatchSize > 0 && len(group) > l.maxBatchSize {
		group = group[:l.maxBatchSize]
	}

	batch, err := l.makeBatchLocked(group)
	if err != nil {
		return Batch{}, err
	}
	return cloneBatch(batch), nil
}

// BuildBatch is an alias for NextBatch.
func (l *Ledger) BuildBatch() (Batch, error) { return l.NextBatch() }

// FormBatch is an alias for NextBatch.
func (l *Ledger) FormBatch() (Batch, error) { return l.NextBatch() }

func (l *Ledger) makeBatchLocked(group []*ledgerEntry) (Batch, error) {
	var batchID string
	for attempts := 0; attempts < 8; attempts++ {
		id, err := l.newBatchID()
		if err != nil {
			return Batch{}, fmt.Errorf("netsec: generate batch id: %w", err)
		}
		if id == "" {
			continue
		}
		if _, exists := l.batches[id]; !exists {
			batchID = id
			break
		}
	}
	if batchID == "" {
		return Batch{}, fmt.Errorf("netsec: could not generate a unique batch id")
	}

	batch := Batch{
		BatchID:          batchID,
		ModelGeneration:  group[0].request.ModelGeneration,
		DecodeStrategy:   group[0].request.DecodeStrategy,
		DecodingStrategy: group[0].request.DecodeStrategy,
		Rows:             make([]BatchRow, len(group)),
	}
	state := &batchState{
		batch:   batch,
		entries: make([]*ledgerEntry, len(group)),
		rows:    make([]rowState, len(group)),
	}
	for i, entry := range group {
		req := entry.request
		row := BatchRow{
			RowIndex:         i,
			TenantID:         req.TenantID,
			RequestID:        req.RequestID,
			ModelGeneration:  req.ModelGeneration,
			InputTokens:      cloneTokens(req.InputTokens),
			InputLength:      len(req.InputTokens),
			MaxOutputTokens:  req.OutputLimit(),
			Deadline:         req.Deadline,
			CancelGeneration: req.CancelGeneration,
			DecodeStrategy:   req.DecodeStrategy,
			IdentityDigest:   IdentityDigest(req),
		}
		batch.Rows[i] = row
		state.entries[i] = entry
		entry.status = entryBatched
		entry.batchID = batchID
		entry.rowIndex = i
	}
	state.batch = batch
	l.batches[batchID] = state
	return batch, nil
}

func lessEntry(a, b *ledgerEntry) bool {
	if a.arrival != b.arrival {
		return a.arrival < b.arrival
	}
	if a.request.RequestID != b.request.RequestID {
		return a.request.RequestID < b.request.RequestID
	}
	if a.request.TenantID != b.request.TenantID {
		return a.request.TenantID < b.request.TenantID
	}
	da := IdentityDigest(a.request)
	db := IdentityDigest(b.request)
	if da != db {
		return da < db
	}
	return a.key.tenantID < b.key.tenantID
}

// Batch returns an immutable copy of an outstanding or completed batch.
func (l *Ledger) Batch(batchID string) (Batch, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	state, ok := l.batches[batchID]
	if !ok {
		return Batch{}, false
	}
	return cloneBatch(state.batch), true
}

// PendingCount reports requests that are still waiting for a batch.
func (l *Ledger) PendingCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, entry := range l.pending {
		if entry.status == entryPending {
			n++
		}
	}
	return n
}

// BatchCount reports the number of batches created by the ledger.
func (l *Ledger) BatchCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.batches)
}

// Cancel revokes only the matching row's future delivery eligibility. It does
// not remove the row or renumber any other row. A cancellation generation
// must match the request's generation exactly; replays from an older or newer
// incarnation are rejected as stale.
func (l *Ledger) Cancel(tenantID, requestID string, cancelGeneration uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.entries[requestKey{tenantID: tenantID, requestID: requestID}]
	if !ok {
		return fmt.Errorf("%w: tenant=%q request=%q", ErrUnknownRequest, tenantID, requestID)
	}
	if cancelGeneration != entry.request.CancelGeneration {
		return fmt.Errorf("%w: got=%d want=%d", ErrStaleCancellation, cancelGeneration, entry.request.CancelGeneration)
	}
	switch entry.status {
	case entryCanceled, entryExpired, entryInvalid:
		return nil
	case entryDelivered:
		return fmt.Errorf("%w: tenant=%q request=%q", ErrAlreadyDelivered, tenantID, requestID)
	}
	entry.status = entryCanceled
	return nil
}

// CancelRequest is an alias for Cancel.
func (l *Ledger) CancelRequest(tenantID, requestID string, cancelGeneration uint64) error {
	return l.Cancel(tenantID, requestID, cancelGeneration)
}

// SetModelGeneration changes the current model generation. Work and in-flight
// batches from another generation become permanently ineligible.
func (l *Ledger) SetModelGeneration(generation uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.generationSet && generation == l.currentGen {
		return
	}
	l.setGenerationLocked(generation)
}

// AdvanceModelGeneration moves to the next model generation and invalidates
// all batches from the previous generation.
func (l *Ledger) AdvanceModelGeneration() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.generationSet {
		l.setGenerationLocked(1)
		return 1
	}
	l.setGenerationLocked(l.currentGen + 1)
	return l.currentGen
}

// InvalidateModelGeneration is an alias for SetModelGeneration.
func (l *Ledger) InvalidateModelGeneration(generation uint64) {
	l.SetModelGeneration(generation)
}

// AdvanceGeneration is an alias for AdvanceModelGeneration.
func (l *Ledger) AdvanceGeneration() uint64 { return l.AdvanceModelGeneration() }

// SwitchModelGeneration is an alias for SetModelGeneration.
func (l *Ledger) SwitchModelGeneration(generation uint64) { l.SetModelGeneration(generation) }

// CurrentModelGeneration reports the active generation.
func (l *Ledger) CurrentModelGeneration() (uint64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.currentGen, l.generationSet
}

func (l *Ledger) setGenerationLocked(generation uint64) {
	l.currentGen = generation
	l.generationSet = true
	for _, entry := range l.pending {
		if entry.status == entryPending && entry.request.ModelGeneration != generation {
			entry.status = entryInvalid
		}
	}
	for _, state := range l.batches {
		if state.batch.ModelGeneration != generation {
			state.invalid = true
			for _, entry := range state.entries {
				if entry.status == entryBatched {
					entry.status = entryInvalid
				}
			}
		}
	}
}

func (l *Ledger) expirePendingLocked(now time.Time) {
	for _, entry := range l.pending {
		if entry.status != entryPending {
			continue
		}
		if deadlinePassed(entry.request.Deadline, now) {
			entry.status = entryExpired
			entry.expired = true
		}
	}
}

// Execute forms one batch, invokes cb with a copy, and atomically applies the
// returned result. It returns the canonical batch and the delivered rows.
func (l *Ledger) Execute(cb TensorCallback) (Batch, []Delivery, error) {
	batch, err := l.NextBatch()
	if err != nil {
		return Batch{}, nil, err
	}
	result, err := cb(cloneBatch(batch))
	if err != nil {
		return batch, nil, err
	}
	deliveries, err := l.ApplyResult(result)
	return batch, deliveries, err
}

// ExecuteContext is the context-aware form of Execute.
func (l *Ledger) ExecuteContext(ctx context.Context, cb ContextTensorCallback) (Batch, []Delivery, error) {
	batch, err := l.NextBatch()
	if err != nil {
		return Batch{}, nil, err
	}
	result, err := cb(ctx, cloneBatch(batch))
	if err != nil {
		return batch, nil, err
	}
	deliveries, err := l.ApplyResult(result)
	return batch, deliveries, err
}

// ApplyResult validates and applies one tensor callback result. Validation is
// atomic: if any row is malformed, no row is committed. Repeating an identical
// result is idempotent and produces no duplicate deliveries.
func (l *Ledger) ApplyResult(result TensorResult) ([]Delivery, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if result.BatchID == "" {
		return nil, fmt.Errorf("%w: empty batch id", ErrBatchMismatch)
	}
	state, ok := l.batches[result.BatchID]
	if !ok {
		return nil, fmt.Errorf("%w: batch=%q", ErrUnknownBatch, result.BatchID)
	}
	if err := l.validateBatchLocked(state); err != nil {
		return nil, err
	}
	if result.ModelGeneration != state.batch.ModelGeneration {
		return nil, fmt.Errorf("%w: result=%d batch=%d", ErrModelGeneration, result.ModelGeneration, state.batch.ModelGeneration)
	}
	if state.streaming {
		return nil, fmt.Errorf("%w: batch=%q", ErrBatchNotStreaming, result.BatchID)
	}
	if state.nonStreamStarted && len(result.Rows) != len(state.batch.Rows) {
		return nil, fmt.Errorf("%w: got=%d want=%d", ErrRowCountMismatch, len(result.Rows), len(state.batch.Rows))
	}
	if len(result.Rows) != len(state.batch.Rows) {
		return nil, fmt.Errorf("%w: got=%d want=%d", ErrRowCountMismatch, len(result.Rows), len(state.batch.Rows))
	}

	now := l.clock()
	planned := make([]TensorRow, len(state.batch.Rows))
	seen := make([]bool, len(state.batch.Rows))
	for _, row := range result.Rows {
		if row.RowIndex < 0 || row.RowIndex >= len(state.batch.Rows) {
			return nil, fmt.Errorf("%w: row=%d", ErrRowIndex, row.RowIndex)
		}
		if seen[row.RowIndex] {
			return nil, fmt.Errorf("%w: row=%d", ErrDuplicateRow, row.RowIndex)
		}
		seen[row.RowIndex] = true
		expected := state.batch.Rows[row.RowIndex]
		if !digestEqual(row.IdentityDigest, expected.IdentityDigest) {
			return nil, fmt.Errorf("%w: row=%d", ErrIdentityMismatch, row.RowIndex)
		}
		if len(row.Tokens) > expected.MaxOutputTokens {
			return nil, fmt.Errorf("%w: row=%d got=%d limit=%d", ErrOutputLimit, row.RowIndex, len(row.Tokens), expected.MaxOutputTokens)
		}
		prior := state.rows[row.RowIndex]
		if err := validateOutputProgress(prior.tokens, prior.done, row.Tokens, row.Done); err != nil {
			return nil, fmt.Errorf("row %d: %w", row.RowIndex, err)
		}
		planned[row.RowIndex] = TensorRow{
			RowIndex:       row.RowIndex,
			IdentityDigest: expected.IdentityDigest,
			Tokens:         cloneTokens(row.Tokens),
			Done:           row.Done,
		}
	}
	for i, ok := range seen {
		if !ok {
			return nil, fmt.Errorf("%w: missing row=%d", ErrRowCountMismatch, i)
		}
	}

	deliveries := make([]Delivery, 0, len(planned))
	for i, row := range planned {
		entry := state.entries[i]
		metadata := state.batch.Rows[i]
		prior := state.rows[i]

		eligible := entry.status != entryCanceled &&
			entry.status != entryExpired &&
			entry.status != entryInvalid &&
			!state.invalid &&
			!entry.expired
		if deadlinePassed(metadata.Deadline, now) {
			eligible = false
			entry.expired = true
			if entry.status == entryBatched {
				entry.status = entryExpired
			}
		}

		hasOutput := len(row.Tokens) != 0 || row.Done
		newlyDeliverable := eligible &&
			(!prior.everDelivered ||
				!sameTokens(prior.deliveredTokens, row.Tokens) ||
				prior.deliveredDone != row.Done) &&
			(prior.everDelivered || hasOutput)

		state.rows[i].tokens = cloneTokens(row.Tokens)
		state.rows[i].done = row.Done
		if newlyDeliverable {
			state.rows[i].deliveredTokens = cloneTokens(row.Tokens)
			state.rows[i].deliveredDone = row.Done
			state.rows[i].everDelivered = true
			deliveries = append(deliveries, Delivery{
				BatchID:         state.batch.BatchID,
				TenantID:        metadata.TenantID,
				RequestID:       metadata.RequestID,
				ModelGeneration: state.batch.ModelGeneration,
				RowIndex:        i,
				IdentityDigest:  metadata.IdentityDigest,
				Tokens:          cloneTokens(row.Tokens),
				Done:            row.Done,
				Step:            0,
			})
		}
		if row.Done {
			if eligible && entry.status == entryBatched {
				entry.status = entryDelivered
			}
		}
	}
	state.nonStreamStarted = true
	sort.Slice(deliveries, func(i, j int) bool { return deliveries[i].RowIndex < deliveries[j].RowIndex })
	return deliveries, nil
}

// ProcessResult applies a result and, when the callback did not copy the
// batch ID, fills it from the explicit batchID argument.
func (l *Ledger) ProcessResult(batchID string, result TensorResult) ([]Delivery, error) {
	if result.BatchID == "" {
		result.BatchID = batchID
	}
	return l.ApplyResult(result)
}

// Complete is an alias for ApplyResult.
func (l *Ledger) Complete(result TensorResult) ([]Delivery, error) {
	return l.ApplyResult(result)
}

// DeliverResult is an alias for ApplyResult.
func (l *Ledger) DeliverResult(result TensorResult) ([]Delivery, error) {
	return l.ApplyResult(result)
}

// Apply is a batch-ID-explicit form of ApplyResult.
func (l *Ledger) Apply(batchID string, result TensorResult) ([]Delivery, error) {
	return l.ProcessResult(batchID, result)
}

// AcceptResult is an alias for ApplyResult.
func (l *Ledger) AcceptResult(result TensorResult) ([]Delivery, error) {
	return l.ApplyResult(result)
}

func (l *Ledger) validateBatchLocked(state *batchState) error {
	if state.invalid {
		return fmt.Errorf("%w: batch=%q", ErrModelGeneration, state.batch.BatchID)
	}
	if l.generationSet && state.batch.ModelGeneration != l.currentGen {
		state.invalid = true
		return fmt.Errorf("%w: batch=%q", ErrModelGeneration, state.batch.BatchID)
	}
	return nil
}

func validateOutputProgress(prior []uint32, priorDone bool, next []uint32, nextDone bool) error {
	if priorDone {
		if !nextDone || !sameTokens(prior, next) {
			return ErrFrozenRowModified
		}
		return nil
	}
	if len(next) < len(prior) || !hasTokenPrefix(next, prior) {
		return ErrResultConflict
	}
	return nil
}

func sameTokens(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hasTokenPrefix(tokens, prefix []uint32) bool {
	if len(tokens) < len(prefix) {
		return false
	}
	for i := range prefix {
		if tokens[i] != prefix[i] {
			return false
		}
	}
	return true
}

func isTerminal(status entryStatus) bool {
	switch status {
	case entryDelivered, entryCanceled, entryExpired, entryInvalid:
		return true
	default:
		return false
	}
}

func randomBatchID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "batch-" + hex.EncodeToString(raw[:]), nil
}
