// Package parity implements an exact RSA least-significant-bit / parity-oracle
// attack verifier intended to be driven by a local Go cryptographic regression
// host.
//
// The package never reads an RSA private key. The host generates the key,
// derives the public odd modulus N and exponent e, and supplies a Callback
// that performs the private operation locally and reports only the parity of
// the decrypted integer.
//
// All interval contraction uses exact math/big rational arithmetic; no floating
// point values are used. When exactly one integer candidate remains it is only
// accepted after checking m^e mod N == c.
package parity

import (
	"errors"
	"math/big"
)

// Parity is the only positive information leaked by a vulnerable callback.
//
// It is deliberately a distinct type so that a returned error or a uniform
// rejection can never silently be interpreted as ParityEven.
type Parity int

const (
	// ParityEven means the decrypted integer is even (LSB 0).
	ParityEven Parity = 0
	// ParityOdd means the decrypted integer is odd (LSB 1).
	ParityOdd Parity = 1
	// ParityRejected is returned alongside a rejection error. It is deliberately
	// neither even nor odd so an ignored error can never read as ParityEven.
	ParityRejected Parity = -1
)

// Int returns the canonical 0/1 representation.
func (p Parity) Int() int { return int(p) }

// String renders the observable parity result.
func (p Parity) String() string {
	switch p {
	case ParityEven:
		return "even"
	case ParityOdd:
		return "odd"
	case ParityRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

// Callback is the local test oracle contract.
//
// The callback receives x with 0 <= x < N and must return exactly one of:
//   - a non-nil parity (ParityEven or ParityOdd) with nil error;
//   - an error wrapping ErrUnavailable (temporarily unavailable / rate limit);
//   - an error wrapping ErrVersionMismatch (key rotation version mismatch).
//
// Any other non-nil error is treated as a permanent oracle failure and is
// never converted into a parity bit.
type Callback func(x *big.Int) (Parity, error)

// QueryFunc is the most generic host callback signature: the parity is a plain
// 0/1 int. Use AdaptFunc to convert it to a Callback.
type QueryFunc func(x *big.Int) (parityBit int, err error)

// AdaptFunc adapts a generic func(*big.Int) (int, error) to a Callback.
//
// Only 0 and 1 are accepted as parity bits; any other value yields
// ErrInvalidParityBit.
func AdaptFunc(f QueryFunc) Callback {
	return func(x *big.Int) (Parity, error) {
		bit, err := f(x)
		if err != nil {
			return ParityEven, err
		}
		switch bit {
		case 0:
			return ParityEven, nil
		case 1:
			return ParityOdd, nil
		default:
			return ParityEven, ErrInvalidParityBit
		}
	}
}

// Oracle is an optional interface form of Callback for hosts that prefer
// methods.
type Oracle interface {
	QueryParity(x *big.Int) (Parity, error)
}

// AdaptOracle adapts an Oracle value to a Callback.
func AdaptOracle(o Oracle) Callback { return o.QueryParity }

// Sentinel errors classify callback outcomes and recovery termination.
var (
	// ErrUnavailable maps to the callback's "temporarily unavailable" state.
	ErrUnavailable = errors.New("parity oracle: temporarily unavailable")

	// ErrVersionMismatch maps to the callback's key-rotation mismatch state.
	ErrVersionMismatch = errors.New("parity oracle: key version mismatch")

	// ErrVersionChanged is returned when the bound key version changes. It
	// wraps ErrVersionMismatch so errors.Is classifies it as a mismatch.
	ErrVersionChanged = wrapSentinel(ErrVersionMismatch, "key version changed during recovery")

	// ErrInvalidQuery indicates the package or host produced x outside [0,N).
	ErrInvalidQuery = errors.New("parity oracle: query x must satisfy 0 <= x < N")

	// ErrInvalidInput indicates invalid recovery parameters.
	ErrInvalidInput = errors.New("parity oracle: invalid recovery parameters")

	// ErrInvalidParityBit indicates an adapted callback returned a bit other
	// than 0 or 1.
	ErrInvalidParityBit = errors.New("parity oracle: parity bit must be 0 or 1")

	// ErrBudgetExhausted indicates the caller's query budget was consumed. A
	// serializable checkpoint is available on the returned Result.
	ErrBudgetExhausted = errors.New("parity oracle: query budget exhausted")

	// ErrNoOracle means no usable parity oracle was available (for example a
	// fixed callback that uniformly rejects before authentication).
	ErrNoOracle = errors.New("parity oracle: no usable oracle")

	// ErrAuthRequired is the uniform rejection produced by FixedCallback. It
	// wraps ErrNoOracle and is never classified as even.
	ErrAuthRequired = wrapSentinel(ErrNoOracle, "authentication required before decryption")

	// ErrBindingMismatch is returned on resume when N/e/c/keyVersion do not
	// match the checkpoint binding.
	ErrBindingMismatch = errors.New("parity oracle: checkpoint binding mismatch")

	// ErrDigestMismatch is returned when the query history digest does not
	// match the bound digest.
	ErrDigestMismatch = errors.New("parity oracle: query history digest mismatch")

	// ErrVerificationFailed means the sole remaining candidate does not satisfy
	// m^e mod N = c. Success must not be reported.
	ErrVerificationFailed = errors.New("parity oracle: candidate failed c == m^e mod N verification")

	// ErrIntervalBudget means exact interval expansion exceeded the configured
	// safety cap (only relevant to aggressive per-query blinding).
	ErrIntervalBudget = errors.New("parity oracle: feasible interval count exceeded cap")

	// ErrCanceled is returned when the supplied context is canceled.
	ErrCanceled = errors.New("parity oracle: canceled")
)

func wrapSentinel(target error, msg string) error {
	return &wrappedError{target: target, msg: msg}
}

type wrappedError struct {
	target error
	msg    string
}

func (e *wrappedError) Error() string { return e.msg }
func (e *wrappedError) Unwrap() error { return e.target }
func (e *wrappedError) Is(target error) bool {
	return target == e.target
}

// Status is the machine-readable termination state of a recovery attempt.
type Status string

const (
	// StatusRecovered means m was uniquely recovered and c-verified.
	StatusRecovered Status = "recovered"
	// StatusBudgetExhausted means the query budget was reached; cp is resumable.
	StatusBudgetExhausted Status = "budget_exhausted"
	// StatusVersionInterrupted means the key version changed; abort immediately.
	StatusVersionInterrupted Status = "version_switch_interrupted"
	// StatusNoOracle means every probe was uniformly rejected / no parity.
	StatusNoOracle Status = "no_usable_oracle"
	// StatusInvalidInput means N/e/c or options were invalid.
	StatusInvalidInput Status = "invalid_input"
	// StatusVerificationFailed means a unique candidate failed c-verification.
	StatusVerificationFailed Status = "verification_failed"
	// StatusIntervalBudget means the exact interval cap was exceeded.
	StatusIntervalBudget Status = "interval_budget"
	// StatusCanceled means context cancellation stopped the run.
	StatusCanceled Status = "canceled"
)

// Binding is the public key/target identity that a checkpoint and resume must
// be bound to. It contains no private material.
type Binding struct {
	N          *big.Int `json:"n"`
	E          *big.Int `json:"e"`
	C          *big.Int `json:"c"`
	KeyVersion string   `json:"key_version,omitempty"`
}

// Params is the explicit host-facing recovery parameter set.
type Params struct {
	// N is the public odd RSA modulus.
	N *big.Int
	// E is the public exponent. If nil, EInt is used.
	E *big.Int
	// EInt is a convenience for callers holding rsa.PublicKey.E (an int).
	EInt int
	// C is the target ciphertext c = m^e mod N.
	C *big.Int
	// KeyVersion is the host's key-rotation version bound to this run.
	KeyVersion string
}

func (p Params) bindingE() *big.Int {
	if p.E != nil {
		return new(big.Int).Set(p.E)
	}
	return big.NewInt(int64(p.EInt))
}
