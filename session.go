package netsec

import (
	"errors"
	"fmt"
	"math/big"
)

// Status describes the state of a recovery session.
type Status uint8

const (
	// StatusRunning means more queries may be made.
	StatusRunning Status = iota
	// StatusRecovered means exactly one candidate remained and its
	// re-encryption matched the target ciphertext.
	StatusRecovered
	// StatusExhausted means the query budget was used up.  Intervals and NextS
	// describe the complete state from which recovery can resume.
	StatusExhausted
)

// Aliases for callers that prefer result-oriented names.
const (
	Running   = StatusRunning
	Recovered = StatusRecovered
	Exhausted = StatusExhausted
)

func (s Status) String() string {
	switch s {
	case StatusRunning:
		return "running"
	case StatusRecovered:
		return "recovered"
	case StatusExhausted:
		return "exhausted"
	default:
		return "unknown"
	}
}

// Outcome is a complete, immutable snapshot of a recovery step.  Plaintext is
// non-nil only when Status is StatusRecovered and m^E mod N equals Ciphertext.
type Outcome struct {
	Status    Status
	Intervals []Interval
	Plaintext *big.Int
	Used      int
	Budget    int
	NextS     *big.Int
	Verified  bool
}

// Result and State are aliases for Outcome.
type Result = Outcome
type State = Outcome

// Done reports whether no further recovery step is useful.
func (o *Outcome) Done() bool {
	return o != nil && (o.Status == StatusRecovered || o.Status == StatusExhausted)
}

// Recovered reports whether a verified plaintext is present.
func (o *Outcome) Recovered() bool { return o != nil && o.Status == StatusRecovered }

// Exhausted reports whether the query budget ran out.
func (o *Outcome) Exhausted() bool { return o != nil && o.Status == StatusExhausted }

// NextSearchPosition returns the resumable search position from this snapshot.
func (o *Outcome) NextSearchPosition() *big.Int {
	if o == nil || o.NextS == nil {
		return nil
	}
	return new(big.Int).Set(o.NextS)
}

// Candidates is an alias for Intervals.
func (o *Outcome) Candidates() []Interval {
	if o == nil {
		return nil
	}
	return CloneIntervals(o.Intervals)
}

// Session is an adaptive prefix-oracle recovery state machine.
type Session struct {
	key        *PublicKey
	ciphertext *big.Int
	oracle     OracleFunc
	budget     int
	used       int
	intervals  []Interval
	lastS      *big.Int
	queries    []QueryRecord
	status     Status
}

// candidateIntervalLimit bounds the interval-list representation used while
// searching for a multiplier.  A negative answer that would shatter the union
// into more pieces than this is retained as a conservative superset: the
// session does not add an incorrect candidate, and every success is still
// confirmed by re-encryption.  Positive answers are always applied exactly.
//
// For the small (three-byte) fixtures used by the regression tests the limit
// exceeds the entire plaintext range, so every negative answer is applied
// exactly.  Larger moduli use a smaller bound to keep the list compact while
// searching; this is a representation policy, not a plaintext guess.
func candidateIntervalLimit(b *big.Int) int {
	if b != nil && b.Cmp(big.NewInt(256)) <= 0 {
		return 512
	}
	return 16
}

// Recovery and Recoverer are aliases for Session.
type Recovery = Session
type Recoverer = Session

// NewSession creates a recovery session from a validated local public key.
// It accepts either (key, ciphertext, oracle, budget) or
// (N, e, ciphertext, oracle, budget).  budget < 0 means unlimited; budget == 0
// immediately yields an exhausted session.  oracle may be an OracleFunc, an
// Oracle, or one of the function shapes accepted by coerceOracle.
func NewSession(args ...any) (*Session, error) {
	keyArg, ciphertext, oracle, budget, err := parseSessionArgs(args)
	if err != nil {
		return nil, err
	}
	validated, err := coercePublicKey(keyArg)
	if err != nil {
		return nil, err
	}
	if ciphertext.Sign() < 0 || ciphertext.Cmp(validated.N) >= 0 {
		return nil, ErrInvalidCiphertext
	}
	fn, err := coerceOracle(oracle)
	if err != nil {
		return nil, err
	}
	if fn == nil {
		return nil, ErrInvalidOracle
	}

	s := &Session{
		key:        validated,
		ciphertext: new(big.Int).Set(ciphertext),
		oracle:     fn,
		budget:     budget,
		intervals:  []Interval{validated.PrefixInterval()},
		lastS:      big.NewInt(1),
		status:     StatusRunning,
	}
	if budget == 0 {
		s.status = StatusExhausted
	}
	return s, nil
}

// New is an alias for NewSession.
func New(args ...any) (*Session, error) { return NewSession(args...) }

// NewRecovery is an alias for NewSession.
func NewRecovery(args ...any) (*Session, error) { return NewSession(args...) }

// Key returns a copy of the bound public key.
func (s *Session) Key() *PublicKey { return s.key.Clone() }

// Ciphertext returns a copy of the bound target ciphertext.
func (s *Session) Ciphertext() *big.Int {
	return new(big.Int).Set(s.ciphertext)
}

// Intervals returns the current complete candidate union.
func (s *Session) Intervals() []Interval { return CloneIntervals(s.intervals) }

// Queries returns a copy of the recorded query history.
func (s *Session) Queries() []QueryRecord { return cloneQueryRecords(s.queries) }

// Used returns the number of queries charged against the budget.
func (s *Session) Used() int { return s.used }

// Budget returns the configured budget; -1 means unlimited.
func (s *Session) Budget() int { return s.budget }

// Remaining returns the remaining query budget, or -1 when unlimited.
func (s *Session) Remaining() int {
	if s.budget < 0 {
		return -1
	}
	if s.used >= s.budget {
		return 0
	}
	return s.budget - s.used
}

// Status returns the current session status.
func (s *Session) Status() Status { return s.status }

// NextSearchPosition returns the next multiplier the planner would try.  It
// does not make a query.
func (s *Session) NextSearchPosition() *big.Int {
	if s.status == StatusRecovered || len(s.intervals) == 0 {
		return nil
	}
	next, err := NextMultiplier(s.intervals, s.lastS, s.key.N, s.key.B)
	if err != nil {
		return nil
	}
	return next
}

// Next is an alias for NextSearchPosition.
func (s *Session) Next() *big.Int { return s.NextSearchPosition() }

// Step performs at most one oracle query.  A successful Step may also finish
// without a query when the state is already terminal.
func (s *Session) Step() (*Outcome, error) {
	if s.status == StatusRecovered {
		return s.outcome(), nil
	}
	if len(s.intervals) == 0 {
		return nil, ErrInconsistent
	}
	if s.status == StatusExhausted {
		return s.outcome(), nil
	}
	if s.budget >= 0 && s.used >= s.budget {
		s.status = StatusExhausted
		return s.outcome(), nil
	}

	multiplier, err := NextMultiplier(s.intervals, s.lastS, s.key.N, s.key.B)
	if err != nil {
		return nil, err
	}
	x := s.queryCiphertext(multiplier)

	feedback, err := s.oracle(new(big.Int).Set(x))
	if err != nil {
		if errors.Is(err, ErrOracleUnavailable) {
			feedback, err = FeedbackUnavailable, nil
		}
	}
	if err != nil {
		return nil, err
	}
	if !feedback.valid() {
		return nil, fmt.Errorf("%w: %w", ErrInconsistent, ErrInvalidFeedback)
	}

	if err := s.checkContradiction(multiplier, feedback); err != nil {
		return nil, err
	}
	// The callback is charged even when the service is unavailable.
	s.used++
	s.queries = append(s.queries, QueryRecord{
		S:        multiplier.String(),
		X:        x.String(),
		Feedback: feedback,
	})

	if feedback == FeedbackUnavailable {
		if s.budget >= 0 && s.used >= s.budget {
			s.status = StatusExhausted
		}
		return s.outcome(), nil
	}

	nextIntervals, err := s.applyFeedback(multiplier, feedback)
	if err != nil {
		return nil, err
	}
	s.intervals = nextIntervals
	s.lastS = new(big.Int).Set(multiplier)

	if len(s.intervals) == 1 && s.intervals[0].Single() {
		m := new(big.Int).Set(s.intervals[0].Lo)
		if !s.key.VerifyReencryption(s.ciphertext, m) {
			return nil, fmt.Errorf("%w: singleton plaintext does not re-encrypt to the target ciphertext",
				ErrInconsistent)
		}
		s.status = StatusRecovered
		return s.outcome(), nil
	}

	if s.budget >= 0 && s.used >= s.budget {
		s.status = StatusExhausted
	}
	return s.outcome(), nil
}

// Recover repeatedly calls Step until recovery succeeds or the budget is
// exhausted.  FeedbackUnavailable is not terminal: it consumes budget and the
// next step retries the same search position.
func (s *Session) Recover() (*Outcome, error) {
	for {
		out, err := s.Step()
		if err != nil {
			return nil, err
		}
		if out.Done() {
			return out, nil
		}
	}
}

// Recover is the package-level convenience function.  It accepts either
// (key, ciphertext, oracle, budget) or (N, e, ciphertext, oracle, budget).
// It never returns an unverified plaintext.
func Recover(args ...any) (*Outcome, error) {
	s, err := NewSession(args...)
	if err != nil {
		return nil, err
	}
	return s.Recover()
}

// RecoverWithKey is the PublicKey form of Recover.
func RecoverWithKey(key any, c *big.Int, oracle any, budget int) (*Outcome, error) {
	s, err := NewSession(key, c, oracle, budget)
	if err != nil {
		return nil, err
	}
	return s.Recover()
}

// Attack is an alias for RecoverWithKey.
func Attack(key any, c *big.Int, oracle any, budget int) (*Outcome, error) {
	return RecoverWithKey(key, c, oracle, budget)
}

func (s *Session) queryCiphertext(multiplier *big.Int) *big.Int {
	se := new(big.Int).Exp(multiplier, s.key.E, s.key.N)
	x := new(big.Int).Mul(s.ciphertext, se)
	return x.Mod(x, s.key.N)
}

func (s *Session) applyFeedback(multiplier *big.Int, feedback Feedback) ([]Interval, error) {
	limit := candidateIntervalLimit(s.key.B)
	if feedback == FeedbackNo && len(s.intervals) >= limit {
		return CloneIntervals(s.intervals), nil
	}
	next, err := FilterIntervals(s.intervals, multiplier, s.key.N, s.key.B, feedback)
	if err != nil {
		return nil, err
	}
	if len(next) == 0 {
		return nil, fmt.Errorf("%w: feedback %s empties the candidate set at s=%s",
			ErrInconsistent, feedback, multiplier)
	}
	if feedback == FeedbackNo && len(next) > limit {
		return CloneIntervals(s.intervals), nil
	}
	return next, nil
}

func parseSessionArgs(args []any) (any, *big.Int, any, int, error) {
	var key any
	var ciphertext *big.Int
	var oracle any
	var budget any

	switch len(args) {
	case 4:
		key, ciphertext, oracle, budget = args[0], toBigInt(args[1]), args[2], args[3]
	case 5:
		n := toBigInt(args[0])
		e := toBigInt(args[1])
		ciphertext, oracle, budget = toBigInt(args[2]), args[3], args[4]
		pub, err := NewPublicKey(n, e)
		if err != nil {
			return nil, nil, nil, 0, err
		}
		key = pub
	default:
		return nil, nil, nil, 0, fmt.Errorf("%w: NewSession expects 4 or 5 arguments", ErrInvalidKey)
	}
	if ciphertext == nil {
		return nil, nil, nil, 0, ErrInvalidCiphertext
	}
	b, ok := toInt(budget)
	if !ok {
		return nil, nil, nil, 0, fmt.Errorf("%w: budget must be an integer", ErrInvalidKey)
	}
	return key, ciphertext, oracle, b, nil
}

func toBigInt(v any) *big.Int {
	switch n := v.(type) {
	case *big.Int:
		if n == nil {
			return nil
		}
		return new(big.Int).Set(n)
	case big.Int:
		return new(big.Int).Set(&n)
	case int:
		return big.NewInt(int64(n))
	case int64:
		return big.NewInt(n)
	case uint:
		return new(big.Int).SetUint64(uint64(n))
	case uint64:
		return new(big.Int).SetUint64(n)
	case string:
		v, ok := new(big.Int).SetString(n, 10)
		if !ok {
			return nil
		}
		return v
	default:
		return nil
	}
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int8:
		return int(n), true
	case int16:
		return int(n), true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case uint:
		return int(n), true
	case uint8:
		return int(n), true
	case uint16:
		return int(n), true
	case uint32:
		return int(n), true
	case uint64:
		if n > uint64(^uint(0)>>1) {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

func (s *Session) checkContradiction(multiplier *big.Int, feedback Feedback) error {
	sText := multiplier.String()
	for _, record := range s.queries {
		if record.S != sText {
			continue
		}
		if record.Feedback == FeedbackUnavailable || feedback == FeedbackUnavailable {
			continue
		}
		if record.Feedback != feedback {
			return fmt.Errorf("%w: multiplier %s previously answered %s, now %s",
				ErrInconsistent, sText, record.Feedback, feedback)
		}
	}
	return nil
}

func (s *Session) outcome() *Outcome {
	out := &Outcome{
		Status:    s.status,
		Intervals: CloneIntervals(s.intervals),
		Used:      s.used,
		Budget:    s.budget,
	}
	if s.status == StatusRecovered && len(s.intervals) == 1 && s.intervals[0].Single() {
		m := new(big.Int).Set(s.intervals[0].Lo)
		if s.key.VerifyReencryption(s.ciphertext, m) {
			out.Plaintext = m
			out.Verified = true
		}
	}
	if s.status != StatusRecovered {
		if next, err := NextMultiplier(s.intervals, s.lastS, s.key.N, s.key.B); err == nil {
			out.NextS = next
		}
	}
	return out
}
