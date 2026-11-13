package netsec

import (
	"errors"
	"fmt"
	"math/big"
	"sync/atomic"
)

var (
	// ErrInvalidConfig reports an unusable RSA public input or recovery budget.
	ErrInvalidConfig = errors.New("netsec: invalid recovery configuration")
	// ErrInvalidInput reports a callback argument outside 0 <= x < N.
	ErrInvalidInput = errors.New("netsec: invalid oracle input")
	// ErrInvalidOracleResult reports a callback result outside the defined enum.
	ErrInvalidOracleResult = errors.New("netsec: invalid oracle result")
	// ErrVerificationFailed reports that the unique integer candidate did not
	// re-encrypt to the target ciphertext.
	ErrVerificationFailed = errors.New("netsec: candidate verification failed")
	// ErrCheckpointInvalid reports malformed or internally inconsistent state.
	ErrCheckpointInvalid = errors.New("netsec: invalid checkpoint")
	// ErrCheckpointMismatch reports a checkpoint used with different public
	// parameters.
	ErrCheckpointMismatch = errors.New("netsec: checkpoint parameter mismatch")
	// ErrCallback reports an error returned by the host callback.
	ErrCallback = errors.New("netsec: oracle callback failed")
)

// OracleResult is the only information a host callback is allowed to expose.
// Rejected is deliberately distinct from Even: a uniform authentication
// rejection must never be interpreted as a parity bit.
type OracleResult uint8

const (
	// ResultUnavailable means the service is temporarily unable to answer.
	ResultUnavailable OracleResult = iota
	// ResultEven means the decrypted query integer is even.
	ResultEven
	// ResultOdd means the decrypted query integer is odd.
	ResultOdd
	// ResultRejected means the request was uniformly rejected before any
	// decryption, for example because authentication failed.
	ResultRejected
)

// String returns the stable transcript spelling of an oracle result.
func (r OracleResult) String() string {
	switch r {
	case ResultUnavailable:
		return "unavailable"
	case ResultEven:
		return "even"
	case ResultOdd:
		return "odd"
	case ResultRejected:
		return "rejected"
	default:
		return fmt.Sprintf("invalid(%d)", uint8(r))
	}
}

func (r OracleResult) usable() bool {
	return r == ResultEven || r == ResultOdd
}

func (r OracleResult) valid() bool {
	return r <= ResultRejected
}

// ParityOracle is a local test callback. It receives x with 0 <= x < N and
// must return only the parity of the raw RSA decryption of x, or a temporary
// unavailability/rejection indication. The package never receives a private
// key.
type ParityOracle interface {
	Query(x *big.Int) (OracleResult, error)
}

// ParityOracleFunc adapts a function to ParityOracle.
type ParityOracleFunc func(x *big.Int) (OracleResult, error)

// Query implements ParityOracle.
func (f ParityOracleFunc) Query(x *big.Int) (OracleResult, error) {
	return f(x)
}

// CheckQuery validates the callback domain 0 <= x < N. Host callbacks may use
// it to reject malformed local requests before touching key material.
func CheckQuery(n, x *big.Int) error {
	if n == nil || n.Sign() <= 0 || n.Bit(0) == 0 || n.Cmp(big.NewInt(3)) < 0 {
		return fmt.Errorf("%w: modulus must be an odd integer >= 3", ErrInvalidInput)
	}
	if x == nil || x.Sign() < 0 || x.Cmp(n) >= 0 {
		return fmt.Errorf("%w: query must satisfy 0 <= x < N", ErrInvalidInput)
	}
	return nil
}

// DecryptParityFunc returns the parity of a raw RSA decryption. It is supplied
// by the host and is only invoked after authentication succeeds.
type DecryptParityFunc func(x *big.Int) (OracleResult, error)

// Authenticator reports whether the current request is authenticated.
type Authenticator func() bool

// AuthenticatedOracle is the fixed callback shape: authentication is checked
// before the decrypt/parity function is reached. An unauthenticated request
// returns ResultRejected uniformly, without decrypting anything.
type AuthenticatedOracle struct {
	n             *big.Int
	authenticated Authenticator
	decryptParity DecryptParityFunc
	decryptions   atomic.Uint64
}

// NewAuthenticatedOracle builds a callback that never decrypts before
// authentication. n is the public odd modulus; the host retains the private
// key behind decryptParity.
func NewAuthenticatedOracle(n *big.Int, authenticated Authenticator, decryptParity DecryptParityFunc) (*AuthenticatedOracle, error) {
	if n == nil || n.Sign() <= 0 || n.Bit(0) == 0 || n.Cmp(big.NewInt(3)) < 0 {
		return nil, fmt.Errorf("%w: N must be an odd integer >= 3", ErrInvalidConfig)
	}
	if decryptParity == nil {
		return nil, fmt.Errorf("%w: nil decrypt callback", ErrInvalidConfig)
	}
	return &AuthenticatedOracle{
		n:             new(big.Int).Set(n),
		authenticated: authenticated,
		decryptParity: decryptParity,
	}, nil
}

// Query implements ParityOracle. Domain validation happens before the
// authentication check, but no decryption or parity computation occurs unless
// authentication succeeds.
func (o *AuthenticatedOracle) Query(x *big.Int) (OracleResult, error) {
	if o == nil || o.n == nil || o.decryptParity == nil {
		return ResultRejected, fmt.Errorf("%w: uninitialized oracle", ErrInvalidConfig)
	}
	if err := CheckQuery(o.n, x); err != nil {
		return ResultRejected, err
	}
	if o.authenticated == nil || !o.authenticated() {
		return ResultRejected, nil
	}
	o.decryptions.Add(1)
	got, err := o.decryptParity(new(big.Int).Set(x))
	if err != nil {
		return ResultUnavailable, fmt.Errorf("%w: %w", ErrCallback, err)
	}
	if !got.valid() {
		return ResultUnavailable, fmt.Errorf("%w: %d", ErrInvalidOracleResult, uint8(got))
	}
	return got, nil
}

// Decryptions returns how many times the decrypt/parity function was reached.
// It is intended for regression tests proving that rejection happens first.
func (o *AuthenticatedOracle) Decryptions() uint64 {
	if o == nil {
		return 0
	}
	return o.decryptions.Load()
}
