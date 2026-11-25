package parity

import (
	"context"
	"errors"
	"fmt"
	"math/big"
)

// DefaultQueryBudget caps a fresh run when Options.QueryBudget is unset. It is
// generously above the ~bitlen(N) queries required by the dyadic attack.
const DefaultQueryBudget = 100000

// DefaultMaxIntervals caps exact interval-set expansion.
const DefaultMaxIntervals = 8192

// DefaultSetVerifyLimit bounds fragmented-set candidate verification.
const DefaultSetVerifyLimit = 4096

// pruneIntervalThreshold triggers exact integer pruning of fragmented sets.
const pruneIntervalThreshold = 128

// Additional termination states and errors.
var (
	ErrOracleFailure              = errors.New("parity oracle: callback returned a non-classified error")
	ErrUnavailableLimit           = errors.New("parity oracle: consecutive unavailable limit reached")
	StatusOracleError      Status = "oracle_error"
	StatusUnavailableLimit Status = "unavailable_limit"
)

// Options configures a recovery or resume run.
type Options struct {
	// Logger receives only safe observable events. Defaults to NopLogger.
	Logger Logger

	// QueryBudget is the maximum number of NEW callback invocations for this
	// invocation. Zero means DefaultQueryBudget.
	QueryBudget int

	// MaxQueries is the TOTAL cumulative callback-invocation cap including
	// checkpoint history. It is the idiomatic "caller total query budget" and
	// takes precedence over QueryBudget.
	MaxQueries int

	// TotalQueryBudget, when > 0, caps the CUMULATIVE callback invocations
	// including checkpoint history. It takes precedence over QueryBudget.
	TotalQueryBudget int

	// MaxConsecutiveUnavailable stops a run after this many consecutive
	// unavailable outcomes. Zero means unlimited (the query budget still caps).
	MaxConsecutiveUnavailable int

	// Blinding selects the rerandomization policy. Defaults to BlindRetries.
	Blinding BlindPolicy

	// Blind produces r when blinding is used. Defaults to a small-factor,
	// crypto-random coprime blinder.
	Blind BlindFunc

	// BaseBlind enables classic persistent base blinding: choose random
	// coprime r, recover m' = r*m mod N with a single exact dyadic interval on
	// c*r^e, then unblind m = m'*r^{-1} mod N and re-verify against c. This
	// rerandomizes the whole query chain and scales to large keys.
	BaseBlind bool

	// BaseBlindR optionally supplies the base-blinding factor generator.
	BaseBlindR BlindFunc

	// MaxIntervals caps exact interval-set expansion from aggressive blinding.
	// Zero means DefaultMaxIntervals.
	MaxIntervals int

	// SetVerifyLimit is the maximum number of integer candidates enumerated to
	// c-verify a fragmented (blinded) feasible set. It does not change the
	// canonical single-interval unique-candidate rule. Zero means
	// DefaultSetVerifyLimit.
	SetVerifyLimit int

	// ExpectedHistoryDigest optionally pins the resume to an externally stored
	// digest of the complete query history.
	ExpectedHistoryDigest string
}

// Result is the outcome of Recover or Resume.
type Result struct {
	// Status is the machine-readable termination state.
	Status Status
	// Plaintext is the recovered and c-verified m when Status == recovered.
	Plaintext *big.Int
	// Binding is the N/e/c/keyVersion identity of this run.
	Binding Binding
	// Queries is the cumulative callback invocation count (history length).
	Queries int
	// Unavailable is the cumulative number of unavailable outcomes.
	Unavailable int
	// Step is the committed logical contraction step.
	Step int
	// Lower and Upper are the exact rational hull endpoints at termination.
	Lower, Upper *big.Rat
	// Intervals is the exact feasible rational interval set.
	Intervals []RatInterval
	// Candidates is the exact integer candidate count at termination.
	Candidates *big.Int
	// History is the complete ordered observable query history.
	History []QueryRecord
	// HistoryDigest is the SHA-256 digest of History.
	HistoryDigest string
	// Checkpoint is set for resumable terminations (budget / limits / cancel).
	Checkpoint *Checkpoint
}

// Recover runs the exact parity-oracle attack against a host Callback.
//
// It is context-free; see RecoverContext for cancellation.
func Recover(p Params, cb Callback, opts Options) (*Result, error) {
	return RecoverContext(context.Background(), p, cb, opts)
}

// RecoverFunc is the generic-int-callback convenience entry point.
func RecoverFunc(N *big.Int, e int, c *big.Int, f QueryFunc, opts Options) (*Result, error) {
	return Recover(Params{N: N, EInt: e, C: c}, AdaptFunc(f), opts)
}

// RecoverContext is the context-aware primary entry point.
func RecoverContext(ctx context.Context, p Params, cb Callback, opts Options) (*Result, error) {
	bind, err := normalizeBinding(p)
	if err != nil {
		return invalidResult(p, err), err
	}
	r, err := newRunner(bind, cb, opts)
	if err != nil {
		st := StatusInvalidInput
		if errors.Is(err, ErrNoOracle) {
			st = StatusNoOracle
		}
		return &Result{Status: st, Binding: bind}, err
	}
	return r.run(ctx, nil)
}

// Resume validates cp against the supplied binding and history digest, replays
// the complete history to rebuild exact state, and continues recovery.
func Resume(ctx context.Context, p Params, cp *Checkpoint, cb Callback, opts Options) (*Result, error) {
	if cp == nil {
		err := fmt.Errorf("%w: nil checkpoint", ErrInvalidInput)
		return invalidResult(p, err), err
	}
	bind, err := normalizeBinding(p)
	if err != nil {
		return &Result{Status: StatusInvalidInput}, err
	}
	if err := cp.Verify(bind, opts.ExpectedHistoryDigest); err != nil {
		return &Result{Status: StatusInvalidInput, Binding: bind}, err
	}
	r, err := newRunner(bind, cb, opts)
	if err != nil {
		return &Result{Status: StatusInvalidInput, Binding: bind}, err
	}
	return r.run(ctx, cp)
}

func normalizeBinding(p Params) (Binding, error) {
	if p.N == nil || p.N.Sign() <= 0 {
		return Binding{}, fmt.Errorf("%w: N must be a positive modulus", ErrInvalidInput)
	}
	if p.N.Bit(0) == 0 {
		return Binding{}, fmt.Errorf("%w: N must be odd", ErrInvalidInput)
	}
	e := p.bindingE()
	if e == nil || e.Sign() <= 0 {
		return Binding{}, fmt.Errorf("%w: e must be positive", ErrInvalidInput)
	}
	if p.C == nil || p.C.Sign() < 0 || p.C.Cmp(p.N) >= 0 {
		return Binding{}, fmt.Errorf("%w: c must satisfy 0 <= c < N", ErrInvalidInput)
	}
	return Binding{N: new(big.Int).Set(p.N), E: e, C: new(big.Int).Set(p.C), KeyVersion: p.KeyVersion}, nil
}

func invalidResult(p Params, err error) *Result {
	b := Binding{KeyVersion: p.KeyVersion}
	if p.N != nil {
		b.N = new(big.Int).Set(p.N)
	}
	if p.E != nil {
		b.E = new(big.Int).Set(p.E)
	} else {
		b.E = big.NewInt(int64(p.EInt))
	}
	if p.C != nil {
		b.C = new(big.Int).Set(p.C)
	}
	return &Result{Status: StatusInvalidInput, Binding: b}
}

type runner struct {
	bind Binding
	cb   Callback
	opt  Options
	log  Logger

	N, E, C *big.Int
	twoE    *big.Int

	baseR  *big.Int // non-nil under classic base blinding
	outerC *big.Int // original c (equals C unless base-blinded)

	intervals []RatInterval
	q         *big.Int // last committed wire query (multiplier T)
	T         *big.Int // exact committed plaintext multiplier
	step      int

	queryCap int // fixed cumulative callback-invocation cap for this invocation

	usedX map[string]struct{}

	history []QueryRecord
}

func newRunner(bind Binding, cb Callback, opt Options) (*runner, error) {
	if cb == nil {
		return nil, fmt.Errorf("%w: nil callback", ErrNoOracle)
	}
	if opt.Logger == nil {
		opt.Logger = NopLogger()
	}
	if opt.QueryBudget <= 0 {
		opt.QueryBudget = DefaultQueryBudget
	}
	if opt.MaxIntervals <= 0 {
		opt.MaxIntervals = DefaultMaxIntervals
	}
	if opt.Blind == nil {
		opt.Blind = NewSmallFactorBlind(64, nil)
	}
	r := &runner{
		bind: bind,
		cb:   cb,
		opt:  opt,
		log:  opt.Logger,
		N:    new(big.Int).Set(bind.N),
		E:    new(big.Int).Set(bind.E),
		C:    new(big.Int).Set(bind.C),
	}
	r.twoE = PowerTwoE(r.E, r.N)
	r.intervals = []RatInterval{{Lo: big.NewRat(0, 1), Hi: new(big.Rat).SetInt(r.N)}}
	r.q = new(big.Int).Set(r.C)
	r.T = big.NewInt(1)
	r.outerC = new(big.Int).Set(r.C)
	r.usedX = map[string]struct{}{}
	return r, nil
}

func (r *runner) run(ctx context.Context, cp *Checkpoint) (*Result, error) {
	if err := r.resumeFrom(cp); err != nil {
		return r.result(StatusInvalidInput, nil), err
	}
	if cp == nil && r.opt.BaseBlind {
		if err := r.initBaseBlind(); err != nil {
			return r.stop(StatusInvalidInput, err)
		}
	}
	startQueries := len(r.history)
	switch {
	case r.opt.MaxQueries > 0:
		r.queryCap = r.opt.MaxQueries
	case r.opt.TotalQueryBudget > 0:
		r.queryCap = r.opt.TotalQueryBudget
	default:
		r.queryCap = startQueries + r.opt.QueryBudget
	}

	totalUnavailable := countOutcome(r.history, OutcomeUnavailable)
	consecutiveUnavailable := 0
	successfulParities := countOutcome(r.history, OutcomeEven) + countOutcome(r.history, OutcomeOdd)

	for {
		if err := ctx.Err(); err != nil {
			return r.stop(StatusCanceled, ErrCanceled)
		}

		// Candidate / verification gate before spending another query.
		count, unique := countCandidates(r.intervals, r.N)
		r.emitDerivation(count)
		if unique != nil {
			if plain, ok := r.finalize(unique); ok {
				return r.recoverReturn(plain)
			}
			return r.stop(StatusVerificationFailed, ErrVerificationFailed)
		}
		// Fragmented (blinded) state may retain several exact branches. Once
		// the candidate set is small, verify each against c and accept only the
		// unique preimage. The canonical single interval is left to narrow to
		// one candidate exactly as specified.
		if len(r.intervals) > 1 && count != nil && count.Cmp(big.NewInt(int64(r.setVerifyLimit()))) <= 0 {
			if cand := r.enumerateVerified(r.setVerifyLimit()); cand != nil {
				if plain, ok := r.finalize(cand); ok {
					return r.recoverReturn(plain)
				}
			}
		}

		// Budget gate.
		if len(r.history) >= r.queryCap {
			return r.budgetStop()
		}

		// Next canonical power-of-two query from committed state.
		qNext := new(big.Int).Mul(r.q, r.twoE)
		qNext.Mod(qNext, r.N)
		tNext := new(big.Int).Mul(r.T, big.NewInt(2))
		stepNext := r.step + 1

		useBlind := r.opt.Blinding == BlindEveryQuery

		// Retry loop for the SAME logical pending step. Unavailable outcomes do
		// not commit q/T/step and do not alter intervals.
		for {
			if err := ctx.Err(); err != nil {
				return r.stop(StatusCanceled, ErrCanceled)
			}
			if len(r.history) >= r.queryCap {
				return r.budgetStop()
			}

			wireX := new(big.Int).Set(qNext)
			wireT := new(big.Int).Set(tNext)
			var blind MetaBlind
			kind := "power2"
			if useBlind {
				kind = "blind"
				var lastR *big.Int
				var lastFactors []*big.Int
				chosen := false
				for attempt := 0; attempt < 32; attempt++ {
					b, berr := r.opt.Blind(r.N)
					if berr != nil {
						return r.stop(StatusInvalidInput, berr)
					}
					candX := BlindQuery(qNext, b.R, r.E, r.N)
					lastR, lastFactors = b.R, b.Factors
					if _, used := r.usedX[candX.Text(16)]; !used {
						wireX = candX
						chosen = true
						break
					}
				}
				if !chosen {
					wireX = BlindQuery(qNext, lastR, r.E, r.N)
				}
				blind = MetaBlind{R: lastR, Factors: lastFactors}
				wireT.Mul(tNext, lastR)
			}

			if wireX.Sign() < 0 || wireX.Cmp(r.N) >= 0 {
				// Defensive: should never happen because BlindQuery reduces mod N.
				err := fmt.Errorf("%w: computed x out of range", ErrInvalidQuery)
				r.record(stepNext, kind, wireX, wireT, blind, OutcomeInvalidQuery, nil)
				return r.stop(StatusInvalidInput, err)
			}

			seq := len(r.history)
			r.log.LogEvent(Event{Kind: EventQuery, Seq: seq, Step: stepNext, X: wireX.Text(16), Message: kind})

			p, err := r.cb(new(big.Int).Set(wireX))

			switch {
			case err == nil && (p == ParityEven || p == ParityOdd):
				bit := p.Int()
				r.record(stepNext, kind, wireX, wireT, blind, outcomeFor(p), &bit)
				next, ierr := applyParity(r.intervals, r.N, wireT, p, r.opt.MaxIntervals)
				if ierr != nil {
					if cand := r.enumerateVerified(r.setVerifyLimit()); cand != nil {
						if plain, ok := r.finalize(cand); ok {
							return r.recoverReturn(plain)
						}
					}
					return r.stop(StatusIntervalBudget, ierr)
				}
				if len(next) > pruneIntervalThreshold {
					next = pruneToIntegerRanges(next, r.N)
				}
				r.intervals = next
				r.q = new(big.Int).Set(wireX)
				r.T = new(big.Int).Set(wireT)
				r.step = stepNext
				successfulParities++
				consecutiveUnavailable = 0
				r.log.LogEvent(Event{Kind: EventOutcome, Seq: seq, Step: stepNext, X: wireX.Text(16), Outcome: p.String()})
				break // exits switch; trailing break exits retry loop

			case errors.Is(err, ErrVersionMismatch):
				r.record(stepNext, kind, wireX, wireT, blind, OutcomeVersionMismatch, nil)
				werr := errors.Join(ErrVersionChanged, err)
				res := r.result(StatusVersionInterrupted, r.buildCheckpoint(StatusVersionInterrupted, "key version changed"))
				r.log.LogEvent(Event{Kind: EventTerminate, Seq: seq, Message: string(StatusVersionInterrupted)})
				return res, werr

			case errors.Is(err, ErrUnavailable):
				r.record(stepNext, kind, wireX, wireT, blind, OutcomeUnavailable, nil)
				totalUnavailable++
				consecutiveUnavailable++
				r.log.LogEvent(Event{Kind: EventOutcome, Seq: seq, Step: stepNext, X: wireX.Text(16), Outcome: string(OutcomeUnavailable)})
				if r.opt.MaxConsecutiveUnavailable > 0 && consecutiveUnavailable >= r.opt.MaxConsecutiveUnavailable {
					res := r.result(StatusUnavailableLimit, r.buildCheckpoint(StatusUnavailableLimit, "unavailable limit"))
					return res, errors.Join(ErrUnavailableLimit, err)
				}
				// Keep the logical query but rerandomize the wire point on retry
				// when blinding-retries is enabled.
				useBlind = r.opt.Blinding != BlindNone
				continue

			case errors.Is(err, ErrInvalidQuery):
				r.record(stepNext, kind, wireX, wireT, blind, OutcomeInvalidQuery, nil)
				return r.stop(StatusInvalidInput, errors.Join(ErrInvalidQuery, err))

			default:
				// Uniform rejection / unclassified failure. An error is NEVER
				// interpreted as even. With zero parity observations there is no
				// usable oracle.
				r.record(stepNext, kind, wireX, wireT, blind, OutcomeOracleError, nil)
				if successfulParities == 0 {
					res := r.result(StatusNoOracle, r.buildCheckpoint(StatusNoOracle, "all probes rejected"))
					return res, errors.Join(ErrNoOracle, err)
				}
				res := r.result(StatusOracleError, r.buildCheckpoint(StatusOracleError, "callback error"))
				return res, errors.Join(ErrOracleFailure, err)
			}
			// Only the successful-parity case reaches here: unavailable continues
			// and all terminal cases return. Exit the retry loop.
			break
		}
	}
}

// MetaBlind is the serializable blinding metadata attached to a record.
type MetaBlind struct {
	R       *big.Int
	Factors []*big.Int
}

func outcomeFor(p Parity) QueryOutcome {
	if p == ParityOdd {
		return OutcomeOdd
	}
	return OutcomeEven
}

func (r *runner) record(step int, kind string, x, T *big.Int, b MetaBlind, outcome QueryOutcome, parity *int) {
	if r.usedX != nil {
		r.usedX[x.Text(16)] = struct{}{}
	}
	rec := QueryRecord{
		Seq:        len(r.history),
		Step:       step,
		Kind:       kind,
		X:          x.Text(16),
		Multiplier: T.Text(16),
		Outcome:    outcome,
		Parity:     parity,
	}
	if b.R != nil {
		rec.R = b.R.Text(16)
		for _, f := range b.Factors {
			rec.RFactors = append(rec.RFactors, f.Text(16))
		}
	}
	r.history = append(r.history, rec)
}

func (r *runner) verify(m *big.Int) bool {
	if m == nil || m.Sign() < 0 || m.Cmp(r.N) >= 0 {
		return false
	}
	got := new(big.Int).Exp(m, r.E, r.N)
	return got.Cmp(r.C) == 0
}

func (r *runner) verifyOuter(m *big.Int) bool {
	if m == nil || m.Sign() < 0 || m.Cmp(r.N) >= 0 {
		return false
	}
	return new(big.Int).Exp(m, r.E, r.N).Cmp(r.outerC) == 0
}

func (r *runner) setVerifyLimit() int {
	if r.opt.SetVerifyLimit > 0 {
		return r.opt.SetVerifyLimit
	}
	return DefaultSetVerifyLimit
}

// finalize verifies the working-domain candidate against the working target
// and, under base blinding, unblinds and verifies against the outer c.
func (r *runner) finalize(working *big.Int) (*big.Int, bool) {
	if !r.verify(working) {
		return nil, false
	}
	if r.baseR == nil {
		return new(big.Int).Set(working), true
	}
	inv := new(big.Int).ModInverse(r.baseR, r.N)
	if inv == nil {
		return nil, false
	}
	m := new(big.Int).Mod(new(big.Int).Mul(working, inv), r.N)
	if !r.verifyOuter(m) {
		return nil, false
	}
	return m, true
}

func (r *runner) recoverReturn(plain *big.Int) (*Result, error) {
	res := r.result(StatusRecovered, nil)
	res.Plaintext = new(big.Int).Set(plain)
	r.log.LogEvent(Event{Kind: EventTerminate, Queries: len(r.history), Message: "recovered and verified m^e mod N == c"})
	return res, nil
}

// enumerateVerified enumerates at most limit integer candidates and returns the
// unique one satisfying working m^e mod N == working c. It returns nil when
// there is not exactly one verified candidate within the limit.
func (r *runner) enumerateVerified(limit int) *big.Int {
	nMinusOne := new(big.Int).Sub(r.N, bigOne)
	examined := 0
	var verified []*big.Int
	for _, iv := range r.intervals {
		lo, hi, ok := iv.integerRange(nMinusOne)
		if !ok {
			continue
		}
		for m := new(big.Int).Set(lo); m.Cmp(hi) <= 0; m.Add(m, bigOne) {
			if examined >= limit {
				return nil
			}
			examined++
			if r.verify(m) {
				verified = append(verified, new(big.Int).Set(m))
				if len(verified) > 1 {
					return nil
				}
			}
		}
	}
	if len(verified) == 1 {
		return verified[0]
	}
	return nil
}

func (r *runner) initBaseBlind() error {
	fn := r.opt.BaseBlindR
	if fn == nil {
		fn = NewBaseBlindR(nil)
	}
	b, err := fn(r.N)
	if err != nil {
		return err
	}
	if b.R == nil || b.R.Sign() <= 0 || b.R.Cmp(r.N) >= 0 {
		return fmt.Errorf("%w: base blind r out of range", ErrInvalidInput)
	}
	if new(big.Int).GCD(nil, nil, b.R, r.N).Cmp(bigOne) != 0 {
		return fmt.Errorf("%w: base blind r must be coprime to N", ErrInvalidInput)
	}
	r.baseR = new(big.Int).Set(b.R)
	r.C = BlindQuery(r.outerC, r.baseR, r.E, r.N)
	r.q = new(big.Int).Set(r.C)
	return nil
}

func (r *runner) emitDerivation(count *big.Int) {
	lo, hi, ok := hull(r.intervals)
	ev := Event{Kind: EventDerivation, Step: r.step, Queries: len(r.history), Candidates: count.String(), Intervals: len(r.intervals)}
	if ok {
		ev.Lower = lo.RatString()
		ev.Upper = hi.RatString()
	}
	r.log.LogEvent(ev)
}

func (r *runner) budgetStop() (*Result, error) {
	cp := r.buildCheckpoint(StatusBudgetExhausted, "query budget reached")
	res := r.result(StatusBudgetExhausted, cp)
	r.log.LogEvent(Event{Kind: EventCheckpoint, Queries: len(r.history), Message: string(StatusBudgetExhausted)})
	return res, ErrBudgetExhausted
}

func (r *runner) stop(st Status, err error) (*Result, error) {
	cp := r.buildCheckpoint(st, err.Error())
	return r.result(st, cp), err
}

func (r *runner) result(st Status, cp *Checkpoint) *Result {
	count, _ := countCandidates(r.intervals, r.N)
	lo, hi, _ := hull(r.intervals)
	var loC, hiC *big.Rat
	if lo != nil {
		loC = new(big.Rat).Set(lo)
	}
	if hi != nil {
		hiC = new(big.Rat).Set(hi)
	}
	var plain *big.Int
	if st == StatusRecovered {
		_, plain = countCandidates(r.intervals, r.N)
	}
	return &Result{
		Status:        st,
		Plaintext:     plain,
		Binding:       r.bind,
		Queries:       len(r.history),
		Unavailable:   countOutcome(r.history, OutcomeUnavailable),
		Step:          r.step,
		Lower:         loC,
		Upper:         hiC,
		Intervals:     cloneIntervals(r.intervals),
		Candidates:    count,
		History:       append([]QueryRecord(nil), r.history...),
		HistoryDigest: DigestHistory(r.history),
		Checkpoint:    cp,
	}
}

func countOutcome(h []QueryRecord, o QueryOutcome) int {
	n := 0
	for _, rec := range h {
		if rec.Outcome == o {
			n++
		}
	}
	return n
}

// resumeFrom replays checkpoint history and rebuilds exact committed state.
func (r *runner) resumeFrom(cp *Checkpoint) error {
	if cp == nil {
		return nil
	}
	r.history = append([]QueryRecord(nil), cp.History...)
	if r.usedX == nil {
		r.usedX = map[string]struct{}{}
	}
	for _, rec := range cp.History {
		if rec.X != "" {
			r.usedX[normalizeHex(rec.X)] = struct{}{}
		}
	}
	if cp.BaseBlindR != "" {
		r.baseR = hexBig(cp.BaseBlindR)
		workingC := hexBig(cp.WorkingC)
		if r.baseR == nil || r.baseR.Sign() <= 0 || workingC == nil {
			return fmt.Errorf("%w: bad base-blinding state in checkpoint", ErrDigestMismatch)
		}
		r.C = workingC
		r.q = new(big.Int).Set(workingC)
	}
	for _, rec := range cp.History {
		switch rec.Outcome {
		case OutcomeEven, OutcomeOdd:
			T := hexBig(rec.Multiplier)
			p := ParityEven
			if rec.Outcome == OutcomeOdd {
				p = ParityOdd
			}
			if T == nil || T.Sign() <= 0 {
				return fmt.Errorf("%w: bad multiplier in history seq %d", ErrDigestMismatch, rec.Seq)
			}
			next, err := applyParity(r.intervals, r.N, T, p, r.opt.MaxIntervals)
			if err != nil {
				return err
			}
			r.intervals = next
			x := hexBig(rec.X)
			if x == nil {
				return fmt.Errorf("%w: bad query in history seq %d", ErrDigestMismatch, rec.Seq)
			}
			r.q = x
			r.T = T
			r.step = rec.Step
		default:
			// Non-parity attempts never committed state.
		}
	}
	return nil
}
