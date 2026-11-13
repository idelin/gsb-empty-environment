package netsec

import (
	"errors"
	"math/big"
)

// Feedback is the only information a prefix oracle is allowed to return.
// The oracle does not reveal a decryption and does not perform a complete
// padding check.
type Feedback int8

const (
	// FeedbackNo means that Dec(x) is outside the interval [2B, 3B).
	FeedbackNo Feedback = iota
	// FeedbackYes means that Dec(x) is inside the interval [2B, 3B).
	FeedbackYes
	// FeedbackUnavailable means that no answer was obtained.  A query that
	// receives this feedback still consumes one unit of the query budget, but
	// it must not change the candidate set.
	FeedbackUnavailable
)

// Common aliases used by callers.
const (
	No          = FeedbackNo
	Yes         = FeedbackYes
	Unavailable = FeedbackUnavailable

	ReplyNo          = FeedbackNo
	ReplyYes         = FeedbackYes
	ReplyUnavailable = FeedbackUnavailable

	OracleNo          = FeedbackNo
	OracleYes         = FeedbackYes
	OracleUnavailable = FeedbackUnavailable

	AnswerNo          = FeedbackNo
	AnswerYes         = FeedbackYes
	AnswerUnavailable = FeedbackUnavailable
)

func (f Feedback) String() string {
	switch f {
	case FeedbackNo:
		return "no"
	case FeedbackYes:
		return "yes"
	case FeedbackUnavailable:
		return "unavailable"
	default:
		return "invalid"
	}
}

func (f Feedback) valid() bool {
	return f >= FeedbackNo && f <= FeedbackUnavailable
}

// OracleFunc is the callback used by a recovery session.  x is always
// c*s^e mod N for a positive multiplier s.
type OracleFunc func(x *big.Int) (Feedback, error)

// Oracle is the interface form of OracleFunc.
type Oracle interface {
	Query(x *big.Int) (Feedback, error)
}

// PredicateOracle adapts a two-result callback.  The second result tells
// whether the service answered at all; false maps to FeedbackUnavailable.
func PredicateOracle(fn func(x *big.Int) (yes, available bool)) OracleFunc {
	if fn == nil {
		return nil
	}
	return func(x *big.Int) (Feedback, error) {
		yes, available := fn(new(big.Int).Set(x))
		if !available {
			return FeedbackUnavailable, nil
		}
		if yes {
			return FeedbackYes, nil
		}
		return FeedbackNo, nil
	}
}

// AvailabilityOracle adapts a callback whose first result is availability and
// whose second result is the predicate.
func AvailabilityOracle(fn func(x *big.Int) (available, yes bool)) OracleFunc {
	if fn == nil {
		return nil
	}
	return func(x *big.Int) (Feedback, error) {
		available, yes := fn(new(big.Int).Set(x))
		if !available {
			return FeedbackUnavailable, nil
		}
		if yes {
			return FeedbackYes, nil
		}
		return FeedbackNo, nil
	}
}

// Query adapts OracleFunc to Oracle.
func (f OracleFunc) Query(x *big.Int) (Feedback, error) { return f(x) }

// coerceOracle accepts the most common callback shapes.  The typed constructors
// below are recommended, but accepting these forms keeps the package easy to
// use from a regression fixture.
func coerceOracle(v any) (OracleFunc, error) {
	switch f := v.(type) {
	case nil:
		return nil, ErrInvalidOracle
	case OracleFunc:
		return f, nil
	case func(*big.Int) (Feedback, error):
		return OracleFunc(f), nil
	case func(*big.Int) Feedback:
		return func(x *big.Int) (Feedback, error) { return f(x), nil }, nil
	case Oracle:
		return func(x *big.Int) (Feedback, error) { return f.Query(x) }, nil
	case func(*big.Int) (bool, bool):
		return PredicateOracle(f), nil
	case func(*big.Int) bool:
		return func(x *big.Int) (Feedback, error) {
			if f(x) {
				return FeedbackYes, nil
			}
			return FeedbackNo, nil
		}, nil
	case func(*big.Int) (bool, error):
		return func(x *big.Int) (Feedback, error) {
			yes, err := f(x)
			if err != nil {
				if errors.Is(err, ErrOracleUnavailable) {
					return FeedbackUnavailable, nil
				}
				return FeedbackNo, err
			}
			if yes {
				return FeedbackYes, nil
			}
			return FeedbackNo, nil
		}, nil
	case func(*big.Int) (int, error):
		return func(x *big.Int) (Feedback, error) {
			n, err := f(x)
			if err != nil {
				if errors.Is(err, ErrOracleUnavailable) {
					return FeedbackUnavailable, nil
				}
				return FeedbackNo, err
			}
			feedback := Feedback(n)
			if !feedback.valid() {
				return FeedbackNo, ErrInvalidFeedback
			}
			return feedback, nil
		}, nil
	case func(*big.Int) int:
		return func(x *big.Int) (Feedback, error) {
			feedback := Feedback(f(x))
			if !feedback.valid() {
				return FeedbackNo, ErrInvalidFeedback
			}
			return feedback, nil
		}, nil
	default:
		return nil, ErrInvalidOracle
	}
}
