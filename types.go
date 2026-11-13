package netsec

import (
	"context"
	"errors"
	"time"
)

// DecodeStrategy identifies an exact decoding configuration. Requests may only
// be placed in the same batch when their strategies compare equal.
type DecodeStrategy = string

// DecodingStrategy is an alias retained for hosts that use the longer name.
type DecodingStrategy = DecodeStrategy

// Token is the ledger's token type. It is an alias, so []Token and []uint32
// are identical.
type Token = uint32

// Request is one inference request. RequestID is namespaced by TenantID.
//
// MaxOutputLength is accepted as a compatibility spelling of MaxOutputTokens.
// Callers should set only one of them. A zero output limit is valid and means
// that the model must not return any output token for the row.
type Request struct {
	TenantID  string
	RequestID string

	ModelGeneration        uint64
	InputTokens            []uint32
	MaxOutputTokens        int
	MaxOutputLength        int
	MaxNewTokens           int
	Deadline               time.Time
	CancelGeneration       uint64
	CancellationGeneration uint64
	DecodeStrategy         DecodeStrategy
	DecodingStrategy       DecodeStrategy

	// ArrivalSequence may be supplied by a host that has its own arrival clock.
	// A zero value means "use the order in which Submit was called".
	ArrivalSequence uint64
	ArrivalOrder    uint64
}

// OutputLimit returns the validated output limit for r. It is useful to
// callbacks that want to mirror ledger metadata without reimplementing the
// compatibility rule.
func (r Request) OutputLimit() int {
	if r.MaxOutputTokens != 0 {
		return r.MaxOutputTokens
	}
	if r.MaxOutputLength != 0 {
		return r.MaxOutputLength
	}
	return r.MaxNewTokens
}

// BatchRow is immutable ledger metadata for one row in a canonical batch.
// InputTokens is a private copy; mutating the slice returned to a callback
// cannot mutate the ledger.
type BatchRow struct {
	RowIndex         int
	TenantID         string
	RequestID        string
	ModelGeneration  uint64
	InputTokens      []uint32
	InputLength      int
	MaxOutputTokens  int
	Deadline         time.Time
	CancelGeneration uint64
	DecodeStrategy   DecodeStrategy
	IdentityDigest   string
}

// OutputLimit returns the immutable maximum output length for the row.
func (r BatchRow) OutputLimit() int { return r.MaxOutputTokens }

// InputLen returns the immutable input length for the row.
func (r BatchRow) InputLen() int { return r.InputLength }

// Batch is the canonical view passed to a tensor callback. Rows are sorted by
// arrival order, request ID, tenant ID, and identity digest. RowIndex is the
// row's immutable position in this and every later streaming step.
type Batch struct {
	BatchID          string
	ModelGeneration  uint64
	DecodeStrategy   DecodeStrategy
	DecodingStrategy DecodeStrategy
	Rows             []BatchRow
}

// Len returns the number of canonical rows.
func (b Batch) Len() int { return len(b.Rows) }

// Row returns a copy of row i. The batch itself is also copied when it enters
// a callback, so this method is primarily a safe convenience API.
func (b Batch) Row(i int) (BatchRow, bool) {
	if i < 0 || i >= len(b.Rows) {
		return BatchRow{}, false
	}
	return cloneBatchRow(b.Rows[i]), true
}

// TensorRow is one row returned by a tensor callback. RowIndex and
// IdentityDigest bind the output to exactly one request. They are not inferred
// from the callback's completion order or the current queue position.
type TensorRow struct {
	RowIndex       int
	IdentityDigest string
	Tokens         []uint32
	Done           bool
}

// TensorResult is the complete result of one tensor callback. Its row count
// must equal the canonical batch row count.
type TensorResult struct {
	BatchID         string
	ModelGeneration uint64
	Rows            []TensorRow
}

// Compatibility aliases for hosts that prefer different naming.
type BatchRequest = Request
type RequestSpec = Request
type BatchResult = TensorResult
type RowResult = TensorRow
type RowOutput = TensorRow
type BatchLedger = Ledger
type StreamingStep = StreamStep
type StreamingResult = StreamResult

// StreamRow is one row in a canonical streaming step.
type StreamRow struct {
	RowIndex        int
	IdentityDigest  string
	TenantID        string
	RequestID       string
	InputTokens     []uint32
	InputLength     int
	MaxOutputTokens int
	Deadline        time.Time
	OutputTokens    []uint32
	Done            bool
	Frozen          bool
	Canceled        bool
	Expired         bool
}

// StreamStep is the canonical input to a streaming callback. FrozenMask has
// one entry per row and is true exactly when that row has already completed.
// Completed rows must be returned unchanged by the callback.
type StreamStep struct {
	BatchID         string
	ModelGeneration uint64
	Step            uint64
	Rows            []StreamRow
	FrozenMask      []bool
}

// StreamResult is the result of one streaming callback. Step is optional; if
// non-zero it must equal the step for which the callback was invoked.
type StreamResult struct {
	BatchID         string
	ModelGeneration uint64
	Step            uint64
	Rows            []TensorRow
}

// Delivery is a row that is still eligible after identity, cancellation,
// deadline, and model-generation checks. Tokens is an independent copy.
type Delivery struct {
	BatchID         string
	TenantID        string
	RequestID       string
	ModelGeneration uint64
	RowIndex        int
	IdentityDigest  string
	Tokens          []uint32
	Done            bool
	Step            uint64
}

// OutputTokens returns a copy of the delivered tokens.
func (d Delivery) OutputTokens() []uint32 { return cloneTokens(d.Tokens) }

// TensorCallback is a one-shot tensor callback. The ledger never passes its
// internal batch or token slices to it.
type TensorCallback func(Batch) (TensorResult, error)

// ContextTensorCallback is the context-aware form of TensorCallback.
type ContextTensorCallback func(context.Context, Batch) (TensorResult, error)

// StreamCallback is a streaming tensor callback.
type StreamCallback func(StreamStep) (StreamResult, error)

// ContextStreamCallback is the context-aware form of StreamCallback.
type ContextStreamCallback func(context.Context, StreamStep) (StreamResult, error)

// Common ledger errors. Errors returned by the ledger always wrap one of these
// values when applicable, so callers can use errors.Is.
var (
	ErrInvalidRequest    = errors.New("netsec: invalid request")
	ErrDuplicateRequest  = errors.New("netsec: duplicate tenant/request id")
	ErrUnknownRequest    = errors.New("netsec: unknown request")
	ErrStaleCancellation = errors.New("netsec: stale cancellation generation")
	ErrNoPendingRequest  = errors.New("netsec: no pending request")
	ErrUnknownBatch      = errors.New("netsec: unknown batch")
	ErrBatchMismatch     = errors.New("netsec: result batch mismatch")
	ErrModelGeneration   = errors.New("netsec: model generation changed")
	ErrRowCountMismatch  = errors.New("netsec: row count mismatch")
	ErrRowIndex          = errors.New("netsec: invalid row index")
	ErrDuplicateRow      = errors.New("netsec: duplicate row index")
	ErrIdentityMismatch  = errors.New("netsec: row identity digest mismatch")
	ErrOutputLimit       = errors.New("netsec: row output limit exceeded")
	ErrResultConflict    = errors.New("netsec: conflicting row result")
	ErrFrozenRowModified = errors.New("netsec: completed row was modified")
	ErrStreamStep        = errors.New("netsec: streaming step mismatch")
	ErrAlreadyDelivered  = errors.New("netsec: row already delivered")
	ErrBatchNotStreaming = errors.New("netsec: batch is not streaming")
	ErrBatchClosed       = errors.New("netsec: batch is closed")
)

// Compatibility error aliases.
var (
	ErrGenerationChanged = ErrModelGeneration
	ErrRowLimitExceeded  = ErrOutputLimit
	ErrDigestMismatch    = ErrIdentityMismatch
	ErrWrongRowCount     = ErrRowCountMismatch
)

// NewTensorRow builds a correctly bound row result from canonical metadata.
func NewTensorRow(row BatchRow, tokens []uint32, done bool) TensorRow {
	return TensorRow{
		RowIndex:       row.RowIndex,
		IdentityDigest: row.IdentityDigest,
		Tokens:         cloneTokens(tokens),
		Done:           done,
	}
}

// NewTensorResult builds an empty, correctly bound result skeleton. A callback
// can fill in Tokens and Done without having to reconstruct row identities.
func NewTensorResult(batch Batch) TensorResult {
	result := TensorResult{
		BatchID:         batch.BatchID,
		ModelGeneration: batch.ModelGeneration,
		Rows:            make([]TensorRow, len(batch.Rows)),
	}
	for i, row := range batch.Rows {
		result.Rows[i] = NewTensorRow(row, nil, false)
	}
	return result
}

// NewTensorResultFromRows binds canonical-order token rows and completion
// flags to a batch.
func NewTensorResultFromRows(batch Batch, tokens [][]uint32, done []bool) (TensorResult, error) {
	if len(tokens) != len(batch.Rows) || len(done) != len(batch.Rows) {
		return TensorResult{}, ErrRowCountMismatch
	}
	result := NewTensorResult(batch)
	for i := range result.Rows {
		result.Rows[i].Tokens = cloneTokens(tokens[i])
		result.Rows[i].Done = done[i]
	}
	return result, nil
}

// NewStreamResult builds a streaming result skeleton for step.
func NewStreamResult(step StreamStep) StreamResult {
	result := StreamResult{
		BatchID:         step.BatchID,
		ModelGeneration: step.ModelGeneration,
		Step:            step.Step,
		Rows:            make([]TensorRow, len(step.Rows)),
	}
	for i, row := range step.Rows {
		result.Rows[i] = TensorRow{
			RowIndex:       row.RowIndex,
			IdentityDigest: row.IdentityDigest,
			Tokens:         cloneTokens(row.OutputTokens),
			Done:           row.Done,
		}
	}
	return result
}

func cloneTokens(tokens []uint32) []uint32 {
	if tokens == nil {
		return nil
	}
	return append([]uint32(nil), tokens...)
}

func cloneBatchRow(row BatchRow) BatchRow {
	row.InputTokens = cloneTokens(row.InputTokens)
	return row
}

func cloneBatch(batch Batch) Batch {
	out := batch
	out.Rows = make([]BatchRow, len(batch.Rows))
	for i, row := range batch.Rows {
		out.Rows[i] = cloneBatchRow(row)
	}
	return out
}
