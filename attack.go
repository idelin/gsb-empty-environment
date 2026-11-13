package netsec

import (
	"fmt"
	"hash"
	"math/big"
)

// Config describes one local RSA parity-oracle experiment. N, E, and C are
// public values. MaxQueries is the caller's total callback-call budget,
// including temporary-unavailable and rejected attempts.
type Config struct {
	N          *big.Int
	E          *big.Int
	C          *big.Int
	MaxQueries int
	Logger     Logger
}

// Status is the terminal state of a recovery run.
type Status string

const (
	// StatusSuccess means a unique candidate was re-encrypted and matched C.
	StatusSuccess Status = "success"
	// StatusBudgetExhausted means the query budget ran out after at least one
	// usable parity response. A resumable checkpoint is attached.
	StatusBudgetExhausted Status = "budget_exhausted"
	// StatusNoUsableOracle means the budget ran out without any even/odd
	// response. A resumable checkpoint is still attached.
	StatusNoUsableOracle Status = "no_usable_oracle"
)

// Outcome is returned by Run. Recovered is non-nil only for StatusSuccess and
// is always accompanied by Verified=true.
type Outcome struct {
	Status           Status
	Recovered        *big.Int
	Verified         bool
	QueriesUsed      int
	UsableResponses  int
	Checkpoint       *Checkpoint
	TranscriptDigest string
	History          []QueryRecord
}

// Succeeded reports whether an integer candidate was recovered and verified.
func (o *Outcome) Succeeded() bool {
	return o != nil && o.Status == StatusSuccess && o.Verified && o.Recovered != nil
}

// Session holds exact recovery state. A session may be checkpointed and
// resumed without revealing or retaining private-key material.
type Session struct {
	n       *big.Int
	e       *big.Int
	c       *big.Int
	pow2e   *big.Int
	lo      *big.Int
	hi      *big.Int
	step    int
	pending *big.Int
	used    int
	max     int
	usable  int
	history []QueryRecord
	digest  hash.Hash
	logger  Logger
}

// NewSession validates public parameters and creates fresh exact-integer
// state. It has no access to a private key.
func NewSession(cfg Config) (*Session, error) {
	n, e, c, err := validateConfig(cfg)
	if err != nil {
		return nil, err
	}
	s := &Session{
		n:       n,
		e:       e,
		c:       c,
		pow2e:   new(big.Int).Exp(big.NewInt(2), e, n),
		lo:      new(big.Int),
		hi:      new(big.Int).Set(n),
		pending: new(big.Int),
		max:     cfg.MaxQueries,
		digest:  newTranscriptDigest(n, e, c),
		logger:  cfg.Logger,
	}
	s.pending.Mul(c, s.pow2e)
	s.pending.Mod(s.pending, n)
	return s, nil
}

// Recover runs a fresh recovery session. It is a convenience wrapper around
// NewSession followed by Run.
func Recover(cfg Config, oracle ParityOracle) (*Outcome, error) {
	s, err := NewSession(cfg)
	if err != nil {
		return nil, err
	}
	return s.Run(oracle)
}

// RecoverFromCheckpoint continues a checkpointed session with a new total
// query budget. It is a convenience wrapper around ResumeSession followed by
// Run.
func RecoverFromCheckpoint(cfg Config, cp *Checkpoint, oracle ParityOracle) (*Outcome, error) {
	s, err := ResumeSession(cfg, cp)
	if err != nil {
		return nil, err
	}
	return s.Run(oracle)
}

func validateConfig(cfg Config) (*big.Int, *big.Int, *big.Int, error) {
	if cfg.N == nil || cfg.N.Sign() <= 0 || cfg.N.Bit(0) == 0 || cfg.N.Cmp(big.NewInt(3)) < 0 {
		return nil, nil, nil, fmt.Errorf("%w: N must be an odd integer >= 3", ErrInvalidConfig)
	}
	if cfg.E == nil || cfg.E.Sign() <= 0 || cfg.E.Bit(0) == 0 || cfg.E.Cmp(big.NewInt(3)) < 0 {
		return nil, nil, nil, fmt.Errorf("%w: E must be an odd integer >= 3", ErrInvalidConfig)
	}
	if cfg.C == nil || cfg.C.Sign() < 0 || cfg.C.Cmp(cfg.N) >= 0 {
		return nil, nil, nil, fmt.Errorf("%w: C must satisfy 0 <= C < N", ErrInvalidConfig)
	}
	if cfg.MaxQueries <= 0 {
		return nil, nil, nil, fmt.Errorf("%w: MaxQueries must be positive", ErrInvalidConfig)
	}
	return new(big.Int).Set(cfg.N), new(big.Int).Set(cfg.E), new(big.Int).Set(cfg.C), nil
}

// Run drives the oracle until success or budget exhaustion. Temporary
// unavailability and uniform rejection consume budget but leave the current
// query and interval unchanged, so the same query is retried.
func (s *Session) Run(oracle ParityOracle) (*Outcome, error) {
	if oracle == nil {
		return nil, fmt.Errorf("%w: nil oracle", ErrInvalidInput)
	}

	// RSA maps 0 to 0, so the only allowed plaintext for ciphertext 0 is 0.
	if s.c.Sign() == 0 {
		if !s.verify(big.NewInt(0)) {
			return nil, fmt.Errorf("%w: 0 does not re-encrypt to 0", ErrVerificationFailed)
		}
		return s.success(new(big.Int)), nil
	}

	for {
		candidate, unique := s.uniqueCandidate()
		if unique {
			if !s.verify(candidate) {
				return nil, fmt.Errorf("%w: unique candidate %s does not satisfy m^E mod N = C", ErrVerificationFailed, candidate.String())
			}
			return s.success(candidate), nil
		}

		if s.used >= s.max {
			status := StatusBudgetExhausted
			if s.usable == 0 {
				status = StatusNoUsableOracle
			}
			return s.budgetOutcome(status), nil
		}

		query := new(big.Int).Set(s.pending)
		result, err := oracle.Query(new(big.Int).Set(query))
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrCallback, err)
		}
		if !result.valid() {
			return nil, fmt.Errorf("%w: %d", ErrInvalidOracleResult, uint8(result))
		}

		s.used++
		rec := QueryRecord{
			Attempt: s.used,
			Step:    s.step,
			Query:   query.Text(16),
			Result:  result.String(),
		}
		s.history = append(s.history, rec)
		writeQueryRecord(s.digest, rec)

		switch result {
		case ResultEven:
			s.applyBit(false)
			s.usable++
			s.advanceQuery()
		case ResultOdd:
			s.applyBit(true)
			s.usable++
			s.advanceQuery()
		case ResultUnavailable, ResultRejected:
			// Keep both the pending query and the interval unchanged.
		}
		s.emit("oracle_query", rec, result)
	}
}

// applyBit performs one exact midpoint contraction. Both endpoints and the
// denominator are integers; no floating-point value is ever formed.
//
//	even: [L/D, (L+U)/(2D)]
//	odd:  [(L+U)/(2D), U/D]
func (s *Session) applyBit(odd bool) {
	sum := new(big.Int).Add(s.lo, s.hi)
	if odd {
		hi := new(big.Int).Lsh(s.hi, 1)
		s.lo = sum
		s.hi = hi
	} else {
		lo := new(big.Int).Lsh(s.lo, 1)
		s.hi = sum
		s.lo = lo
	}
	s.step++
}

func (s *Session) advanceQuery() {
	s.pending.Mul(s.pending, s.pow2e)
	s.pending.Mod(s.pending, s.n)
}

// uniqueCandidate returns the sole integer in the current closed rational
// interval, if one exists. The interval is [lo/2^step, hi/2^step].
func (s *Session) uniqueCandidate() (*big.Int, bool) {
	den := new(big.Int).Lsh(big.NewInt(1), uint(s.step))
	low := new(big.Int).Add(s.lo, new(big.Int).Sub(den, big.NewInt(1)))
	low.Div(low, den)
	high := new(big.Int).Div(new(big.Int).Set(s.hi), den)
	if low.Cmp(high) != 0 {
		return nil, false
	}
	return low, true
}

func (s *Session) verify(m *big.Int) bool {
	if m == nil || m.Sign() < 0 || m.Cmp(s.n) >= 0 {
		return false
	}
	got := new(big.Int).Exp(m, s.e, s.n)
	return got.Cmp(s.c) == 0
}

func (s *Session) success(m *big.Int) *Outcome {
	return &Outcome{
		Status:           StatusSuccess,
		Recovered:        new(big.Int).Set(m),
		Verified:         true,
		QueriesUsed:      s.used,
		UsableResponses:  s.usable,
		TranscriptDigest: s.digestHex(),
		History:          cloneHistory(s.history),
	}
}

func (s *Session) budgetOutcome(status Status) *Outcome {
	return &Outcome{
		Status:           status,
		QueriesUsed:      s.used,
		UsableResponses:  s.usable,
		Checkpoint:       s.Checkpoint(),
		TranscriptDigest: s.digestHex(),
		History:          cloneHistory(s.history),
	}
}

// History returns a copy of the observable query/result transcript.
func (s *Session) History() []QueryRecord {
	return cloneHistory(s.history)
}

// UsableResponses reports how many even/odd answers have been observed.
func (s *Session) UsableResponses() int {
	return s.usable
}

// QueriesUsed reports the number of callback attempts, including unavailable
// and rejected attempts.
func (s *Session) QueriesUsed() int {
	return s.used
}

func cloneHistory(in []QueryRecord) []QueryRecord {
	if len(in) == 0 {
		return []QueryRecord{}
	}
	out := make([]QueryRecord, len(in))
	copy(out, in)
	return out
}
