package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
)

// QueryRecord is one charged oracle query.  S and X are canonical decimal
// integers, so a checkpoint is self-contained and easy to audit.
type QueryRecord struct {
	S        string   `json:"s"`
	X        string   `json:"x"`
	Feedback Feedback `json:"feedback"`
}

// Multiplier parses the recorded multiplier.
func (q QueryRecord) Multiplier() (*big.Int, error) {
	v, ok := new(big.Int).SetString(q.S, 10)
	if !ok {
		return nil, fmt.Errorf("%w: bad multiplier", ErrCheckpointMismatch)
	}
	return v, nil
}

// Ciphertext parses the recorded queried ciphertext.
func (q QueryRecord) Ciphertext() (*big.Int, error) {
	v, ok := new(big.Int).SetString(q.X, 10)
	if !ok {
		return nil, fmt.Errorf("%w: bad ciphertext", ErrCheckpointMismatch)
	}
	return v, nil
}

// Checkpoint is a portable recovery snapshot.  Digest binds the public key,
// target ciphertext, candidate intervals, multiplier position and every
// recorded query.
type Checkpoint struct {
	Version    int           `json:"version"`
	N          string        `json:"n"`
	E          string        `json:"e"`
	K          int           `json:"k"`
	B          string        `json:"b"`
	Ciphertext string        `json:"ciphertext"`
	Budget     int           `json:"budget"`
	Used       int           `json:"used"`
	LastS      string        `json:"last_s"`
	Intervals  []Interval    `json:"intervals"`
	Queries    []QueryRecord `json:"queries"`
	Digest     string        `json:"digest"`
}

// RecoveryCheckpoint is an alias for Checkpoint.
type RecoveryCheckpoint = Checkpoint

const checkpointVersion = 1

// Checkpoint returns a deep, sealed snapshot of the session.
func (s *Session) Checkpoint() *Checkpoint {
	cp := &Checkpoint{
		Version:    checkpointVersion,
		N:          s.key.N.String(),
		E:          s.key.E.String(),
		K:          s.key.K,
		B:          s.key.B.String(),
		Ciphertext: s.ciphertext.String(),
		Budget:     s.budget,
		Used:       s.used,
		LastS:      s.lastS.String(),
		Intervals:  CloneIntervals(s.intervals),
		Queries:    cloneQueryRecords(s.queries),
	}
	cp.Seal()
	return cp
}

// Seal recomputes the checkpoint digest.  Call it after deliberately changing
// a non-binding policy field such as Budget.
func (cp *Checkpoint) Seal() {
	if cp == nil {
		return
	}
	cp.Version = checkpointVersion
	cp.Digest = checkpointDigest(cp)
}

// Verify checks the checkpoint digest.
func (cp *Checkpoint) Verify() error {
	if cp == nil {
		return ErrCheckpointMismatch
	}
	if cp.Version != checkpointVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrCheckpointMismatch, cp.Version)
	}
	if cp.Digest == "" || cp.Digest != checkpointDigest(cp) {
		return fmt.Errorf("%w: digest mismatch", ErrCheckpointMismatch)
	}
	return nil
}

// WithBudget returns a sealed copy with a new query budget.  This is the
// supported way to extend a budget when resuming an exhausted session.
func (cp *Checkpoint) WithBudget(budget int) (*Checkpoint, error) {
	if cp == nil {
		return nil, ErrCheckpointMismatch
	}
	if err := cp.Verify(); err != nil {
		return nil, err
	}
	out := cp.Clone()
	out.Budget = budget
	out.Seal()
	return out, nil
}

// Clone returns a deep copy.
func (cp *Checkpoint) Clone() *Checkpoint {
	if cp == nil {
		return nil
	}
	out := *cp
	out.Intervals = CloneIntervals(cp.Intervals)
	out.Queries = cloneQueryRecords(cp.Queries)
	return &out
}

// Binds reports whether the checkpoint was created for key and ciphertext.
func (cp *Checkpoint) Binds(key any, ciphertext *big.Int) bool {
	if cp == nil || ciphertext == nil {
		return false
	}
	pub, err := coercePublicKey(key)
	if err != nil {
		return false
	}
	return cp.N == pub.N.String() &&
		cp.E == pub.E.String() &&
		cp.Ciphertext == ciphertext.String()
}

// RestoreSession resumes from a checkpoint using the checkpoint's budget.
func RestoreSession(cp *Checkpoint, oracle any) (*Session, error) {
	return restoreSession(cp, nil, nil, oracle, cpBudget(cp), false)
}

// RestoreSessionWithBudget resumes with a caller-supplied budget.  The
// checkpoint itself remains bound to its original recorded queries.
func RestoreSessionWithBudget(cp *Checkpoint, oracle any, budget int) (*Session, error) {
	return restoreSession(cp, nil, nil, oracle, budget, true)
}

// RestoreSessionWithRemainingBudget resumes with "remaining" additional
// queries after the checkpoint's already-charged history.  A negative value
// means unlimited.
func RestoreSessionWithRemainingBudget(cp *Checkpoint, oracle any, remaining int) (*Session, error) {
	if cp == nil {
		return nil, ErrCheckpointMismatch
	}
	total := -1
	if remaining >= 0 {
		total = cp.Used + remaining
	}
	return restoreSession(cp, nil, nil, oracle, total, true)
}

// ResumeSession verifies that cp is bound to key and ciphertext before
// resuming.  A key/ciphertext mismatch is a checkpoint error.
func ResumeSession(cp *Checkpoint, key any, ciphertext *big.Int, oracle any, budget int) (*Session, error) {
	if !cp.Binds(key, ciphertext) {
		return nil, fmt.Errorf("%w: key or ciphertext binding differs", ErrCheckpointMismatch)
	}
	return restoreSession(cp, key, ciphertext, oracle, budget, true)
}

// Restore and Resume are aliases for the checkpoint entry points.
func Restore(cp *Checkpoint, oracle any) (*Session, error) { return RestoreSession(cp, oracle) }
func Resume(cp *Checkpoint, key any, c *big.Int, oracle any, budget int) (*Session, error) {
	return ResumeSession(cp, key, c, oracle, budget)
}

func cpBudget(cp *Checkpoint) int {
	if cp == nil {
		return 0
	}
	return cp.Budget
}

func restoreSession(cp *Checkpoint, expectedKey any, expectedC *big.Int, oracle any, budget int, override bool) (*Session, error) {
	if cp == nil {
		return nil, ErrCheckpointMismatch
	}
	if err := cp.Verify(); err != nil {
		return nil, err
	}
	n, okN := new(big.Int).SetString(cp.N, 10)
	e, okE := new(big.Int).SetString(cp.E, 10)
	c, okC := new(big.Int).SetString(cp.Ciphertext, 10)
	if !okN || !okE || !okC {
		return nil, fmt.Errorf("%w: malformed key or ciphertext", ErrCheckpointMismatch)
	}
	key, err := NewPublicKey(n, e)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCheckpointMismatch, err)
	}
	if key.K != cp.K || key.B.String() != cp.B {
		return nil, fmt.Errorf("%w: K/B do not match N", ErrCheckpointMismatch)
	}
	if c.Sign() < 0 || c.Cmp(key.N) >= 0 {
		return nil, fmt.Errorf("%w: ciphertext is not canonical", ErrCheckpointMismatch)
	}
	if expectedKey != nil {
		if !cp.Binds(expectedKey, c) {
			return nil, fmt.Errorf("%w: expected key or ciphertext differs", ErrCheckpointMismatch)
		}
	}
	if expectedC != nil && cp.Ciphertext != expectedC.String() {
		return nil, fmt.Errorf("%w: expected ciphertext differs", ErrCheckpointMismatch)
	}
	fn, err := coerceOracle(oracle)
	if err != nil {
		return nil, err
	}
	if fn == nil {
		return nil, ErrInvalidOracle
	}

	s := &Session{
		key:        key,
		ciphertext: new(big.Int).Set(c),
		oracle:     fn,
		budget:     budget,
		intervals:  []Interval{key.PrefixInterval()},
		lastS:      big.NewInt(1),
		status:     StatusRunning,
	}
	if !override {
		s.budget = cp.Budget
	}

	for i, record := range cp.Queries {
		multiplier, err := record.Multiplier()
		if err != nil {
			return nil, err
		}
		if multiplier.Sign() <= 0 {
			return nil, fmt.Errorf("%w: query %d has non-positive multiplier", ErrCheckpointMismatch, i)
		}
		recordedX, err := record.Ciphertext()
		if err != nil {
			return nil, err
		}
		expectedX := s.queryCiphertext(multiplier)
		if recordedX.Cmp(expectedX) != 0 {
			return nil, fmt.Errorf("%w: query %d ciphertext does not match c*s^e mod N",
				ErrCheckpointMismatch, i)
		}
		if !record.Feedback.valid() {
			return nil, fmt.Errorf("%w: query %d has invalid feedback", ErrInconsistent, i)
		}
		if err := s.checkContradiction(multiplier, record.Feedback); err != nil {
			return nil, err
		}
		s.used++
		s.queries = append(s.queries, QueryRecord{
			S:        record.S,
			X:        record.X,
			Feedback: record.Feedback,
		})
		if record.Feedback == FeedbackUnavailable {
			continue
		}
		next, err := s.applyFeedback(multiplier, record.Feedback)
		if err != nil {
			return nil, err
		}
		s.intervals = next
		s.lastS = new(big.Int).Set(multiplier)
	}

	if s.used != cp.Used {
		return nil, fmt.Errorf("%w: used-query count differs", ErrCheckpointMismatch)
	}
	if s.lastS.String() != cp.LastS {
		return nil, fmt.Errorf("%w: last multiplier differs", ErrCheckpointMismatch)
	}
	if !intervalsEqual(s.intervals, cp.Intervals) {
		return nil, fmt.Errorf("%w: recorded queries do not reproduce the interval set", ErrCheckpointMismatch)
	}
	if len(s.intervals) == 1 && s.intervals[0].Single() {
		m := new(big.Int).Set(s.intervals[0].Lo)
		if !key.VerifyReencryption(c, m) {
			return nil, fmt.Errorf("%w: checkpoint singleton does not re-encrypt", ErrInconsistent)
		}
		s.status = StatusRecovered
		return s, nil
	}
	if s.budget >= 0 && s.used >= s.budget {
		s.status = StatusExhausted
	}
	return s, nil
}

func cloneQueryRecords(in []QueryRecord) []QueryRecord {
	if len(in) == 0 {
		return []QueryRecord{}
	}
	out := make([]QueryRecord, len(in))
	copy(out, in)
	return out
}

func intervalsEqual(a, b []Interval) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Lo == nil || a[i].Hi == nil || b[i].Lo == nil || b[i].Hi == nil {
			return false
		}
		if a[i].Lo.Cmp(b[i].Lo) != 0 || a[i].Hi.Cmp(b[i].Hi) != 0 {
			return false
		}
	}
	return true
}

func checkpointDigest(cp *Checkpoint) string {
	h := sha256.New()
	writeField := func(s string) {
		fmt.Fprintf(h, "%d:", len(s))
		h.Write([]byte(s))
	}
	writeInt := func(v int64) { writeField(strconv.FormatInt(v, 10)) }

	writeInt(int64(cp.Version))
	writeField(cp.N)
	writeField(cp.E)
	writeInt(int64(cp.K))
	writeField(cp.B)
	writeField(cp.Ciphertext)
	writeInt(int64(cp.Budget))
	writeInt(int64(cp.Used))
	writeField(cp.LastS)
	for _, iv := range cp.Intervals {
		if iv.Lo == nil || iv.Hi == nil {
			writeField("<nil>")
			continue
		}
		lo := iv.Lo.String()
		hi := iv.Hi.String()
		writeField(lo)
		writeField(hi)
	}
	for _, q := range cp.Queries {
		writeField(q.S)
		writeField(q.X)
		writeInt(int64(q.Feedback))
	}
	return hex.EncodeToString(h.Sum(nil))
}
