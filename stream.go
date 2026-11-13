package netsec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// Stream invokes cb once for a batch. The callback receives the next canonical
// step; row indices remain stable for the lifetime of the batch.
func (l *Ledger) Stream(batchID string, cb StreamCallback) ([]Delivery, error) {
	step, err := l.PrepareStreamStep(batchID)
	if err != nil {
		return nil, err
	}
	result, err := cb(step)
	if err != nil {
		return nil, err
	}
	return l.ApplyStreamResult(result)
}

// StreamStep is an alias for Stream. It is named for symmetry with the
// StreamStep value passed to callbacks.
func (l *Ledger) StreamStep(batchID string, cb StreamCallback) ([]Delivery, error) {
	return l.Stream(batchID, cb)
}

// StreamContext is the context-aware form of Stream.
func (l *Ledger) StreamContext(ctx context.Context, batchID string, cb ContextStreamCallback) ([]Delivery, error) {
	step, err := l.PrepareStreamStep(batchID)
	if err != nil {
		return nil, err
	}
	result, err := cb(ctx, step)
	if err != nil {
		return nil, err
	}
	return l.ApplyStreamResult(result)
}

// PrepareStreamStep returns the next step without committing it. A host can
// use this when its callback needs to run asynchronously and call
// ApplyStreamResult later.
func (l *Ledger) PrepareStreamStep(batchID string) (StreamStep, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	state, ok := l.batches[batchID]
	if !ok {
		return StreamStep{}, fmt.Errorf("%w: batch=%q", ErrUnknownBatch, batchID)
	}
	if err := l.validateBatchLocked(state); err != nil {
		return StreamStep{}, err
	}
	if state.nonStreamStarted {
		return StreamStep{}, fmt.Errorf("%w: batch=%q", ErrBatchClosed, batchID)
	}

	stepNumber := state.streamStep + 1
	step := StreamStep{
		BatchID:         state.batch.BatchID,
		ModelGeneration: state.batch.ModelGeneration,
		Step:            stepNumber,
		Rows:            make([]StreamRow, len(state.batch.Rows)),
		FrozenMask:      make([]bool, len(state.batch.Rows)),
	}
	now := l.clock()
	for i, row := range state.batch.Rows {
		entry := state.entries[i]
		prior := state.rows[i]
		expired := entry.expired || deadlinePassed(row.Deadline, now)
		canceled := entry.status == entryCanceled || entry.status == entryExpired || entry.status == entryInvalid
		step.Rows[i] = StreamRow{
			RowIndex:        i,
			IdentityDigest:  row.IdentityDigest,
			TenantID:        row.TenantID,
			RequestID:       row.RequestID,
			InputTokens:     cloneTokens(row.InputTokens),
			InputLength:     row.InputLength,
			MaxOutputTokens: row.MaxOutputTokens,
			Deadline:        row.Deadline,
			OutputTokens:    cloneTokens(prior.tokens),
			Done:            prior.done,
			Frozen:          prior.done,
			Canceled:        canceled,
			Expired:         expired,
		}
		step.FrozenMask[i] = prior.done
	}
	state.streaming = true
	return step, nil
}

// ApplyStreamResult validates and commits a streaming step atomically. A
// callback may return rows in any order, but each row must carry the matching
// rowIndex and identity digest. Completed ("frozen") rows must be byte-for-byte
// unchanged, otherwise the entire step is rejected.
func (l *Ledger) ApplyStreamResult(result StreamResult) ([]Delivery, error) {
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
	if !state.streaming {
		return nil, fmt.Errorf("%w: batch=%q", ErrBatchNotStreaming, result.BatchID)
	}
	if result.ModelGeneration != state.batch.ModelGeneration {
		return nil, fmt.Errorf("%w: result=%d batch=%d", ErrModelGeneration, result.ModelGeneration, state.batch.ModelGeneration)
	}

	expectedStep := state.streamStep + 1
	if result.Step != 0 && result.Step != expectedStep {
		if result.Step == state.streamStep && streamDigest(result) == state.lastStreamDigest {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: got=%d want=%d", ErrStreamStep, result.Step, expectedStep)
	}
	if len(result.Rows) != len(state.batch.Rows) {
		return nil, fmt.Errorf("%w: got=%d want=%d", ErrRowCountMismatch, len(result.Rows), len(state.batch.Rows))
	}

	planned := make([]TensorRow, len(state.batch.Rows))
	seen := make([]bool, len(state.batch.Rows))
	now := l.clock()
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
		if prior.done {
			if !row.Done || !sameTokens(prior.tokens, row.Tokens) {
				return nil, fmt.Errorf("%w: row=%d", ErrFrozenRowModified, row.RowIndex)
			}
		} else if len(row.Tokens) < len(prior.tokens) || !hasTokenPrefix(row.Tokens, prior.tokens) {
			return nil, fmt.Errorf("%w: row=%d", ErrResultConflict, row.RowIndex)
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
				Step:            expectedStep,
			})
		}
		// A prior partial delivery does not make the entry terminal, but once a
		// row is completed it can never be changed or delivered again as new.
		if row.Done && eligible && entry.status == entryBatched {
			entry.status = entryDelivered
		}
	}

	state.streamStep = expectedStep
	state.lastStreamDigest = streamDigest(result)
	sortDeliveries(deliveries)
	return deliveries, nil
}

func sortDeliveries(deliveries []Delivery) {
	for i := 1; i < len(deliveries); i++ {
		for j := i; j > 0 && deliveries[j].RowIndex < deliveries[j-1].RowIndex; j-- {
			deliveries[j], deliveries[j-1] = deliveries[j-1], deliveries[j]
		}
	}
}

func streamDigest(result StreamResult) string {
	rows := make([]TensorRow, len(result.Rows))
	copy(rows, result.Rows)
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j].RowIndex < rows[j-1].RowIndex; j-- {
			rows[j], rows[j-1] = rows[j-1], rows[j]
		}
	}
	h := sha256.New()
	writeString(h, result.BatchID)
	writeUint64(h, result.ModelGeneration)
	writeUint64(h, result.Step)
	writeUint64(h, uint64(len(rows)))
	for _, row := range rows {
		writeUint64(h, uint64(row.RowIndex))
		writeString(h, row.IdentityDigest)
		if row.Done {
			writeUint64(h, 1)
		} else {
			writeUint64(h, 0)
		}
		writeUint64(h, uint64(len(row.Tokens)))
		for _, token := range row.Tokens {
			writeUint32(h, token)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// StreamResultFromStep is an alias helper that creates a stream result with
// the current row tokens. It is equivalent to NewStreamResult.
func StreamResultFromStep(step StreamStep) StreamResult { return NewStreamResult(step) }
