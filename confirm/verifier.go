package confirm

import (
	"errors"
	"time"

	"example.org/netsec/q0063/ocra"
)

var (
	// ErrCounterReplay is returned when the counter is not strictly greater
	// than the last accepted value, including rollback and reuse attempts.
	ErrCounterReplay = errors.New("confirm: counter must be strictly greater than the last accepted counter")
	// ErrChallengeReplay is returned when the challenge was already consumed.
	ErrChallengeReplay = errors.New("confirm: challenge has already been used")
	// ErrInvalidResponse is returned when the OCRA response does not match.
	ErrInvalidResponse = errors.New("confirm: invalid OCRA response")
)

// Request is a transaction confirmation submitted to the service.
type Request struct {
	UserID   string
	Tx       Transaction
	Counter  uint64
	Response string
	// Now optionally overrides the verification time, primarily for tests.
	Now time.Time
}

// Verifier checks OCRA transaction confirmations and enforces the
// monotonic-counter and single-use-challenge invariants.
type Verifier struct {
	suite ocra.Suite
	store Store
	// TimeWindow is the number of 1-minute steps accepted on either side of
	// the current step, i.e. total accepted steps are 2*TimeWindow+1.
	TimeWindow int64
	now        func() time.Time
}

// NewVerifier wires the deployed suite to a state store. The default time
// window tolerates two minutes of clock skew on each side.
func NewVerifier(store Store) *Verifier {
	return &Verifier{
		suite:      ocra.DefaultSuite,
		store:      store,
		TimeWindow: 2,
		now:        time.Now,
	}
}

// GenerateResponse computes the token response for a request; it mirrors the
// server-side check and is useful for token emulation and tests.
func (v *Verifier) GenerateResponse(secret []byte, counter uint64, challenge string, at time.Time) (string, error) {
	question, err := v.suite.DecodeHexQuestion(challenge)
	if err != nil {
		return "", err
	}
	return v.suite.Generate(secret, ocra.Input{
		Counter:  counter,
		Question: question,
		UnixTime: at.Unix(),
	})
}

// Verify validates a confirmation and, only on success, advances the stored
// counter and marks the challenge consumed.
func (v *Verifier) Verify(req Request) error {
	if req.UserID == "" {
		return errors.New("confirm: user id is required")
	}

	state, err := v.store.Get(req.UserID)
	if err != nil {
		return err
	}

	// Counter check first: a rolled-back or reused counter is rejected even if
	// the response itself is well formed.
	if state.CounterUsed && req.Counter <= state.LastCounter {
		return ErrCounterReplay
	}

	challenge, err := Challenge(req.Tx)
	if err != nil {
		return err
	}

	// Single-use challenge: a previously signed answer must never be accepted
	// again, regardless of which counter the attacker presents.
	if _, used := state.UsedChallenges[challenge]; used {
		return ErrChallengeReplay
	}

	question, err := v.suite.DecodeHexQuestion(challenge)
	if err != nil {
		return err
	}

	now := req.Now
	if now.IsZero() {
		now = v.now()
	}
	baseStep := now.Unix()
	matched := false
	for offset := -v.TimeWindow; offset <= v.TimeWindow; offset++ {
		ok, err := v.suite.Validate(state.Secret, ocra.Input{
			Counter:  req.Counter,
			Question: question,
			UnixTime: baseStep + offset*v.suite.TimeStep(),
		}, req.Response)
		if err != nil {
			return err
		}
		if ok {
			matched = true
			break
		}
	}
	if !matched {
		return ErrInvalidResponse
	}

	state.LastCounter = req.Counter
	state.CounterUsed = true
	state.UsedChallenges[challenge] = struct{}{}
	return v.store.Save(req.UserID, state)
}
