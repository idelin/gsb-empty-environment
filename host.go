package parity

import (
	"context"
	"math/big"
)

// Common aliases for hosts using different terminology. They reference the
// same sentinel values, so errors.Is continues to work.
var (
	// ErrTemporarilyUnavailable aliases ErrUnavailable.
	ErrTemporarilyUnavailable = ErrUnavailable
	// ErrRateLimited aliases ErrUnavailable (rate limiting is retried).
	ErrRateLimited = ErrUnavailable
	// ErrKeyRotationMismatch aliases ErrVersionMismatch.
	ErrKeyRotationMismatch = ErrVersionMismatch
)

// Even and Odd are short aliases for the parity values.
const (
	Even = ParityEven
	Odd  = ParityOdd
)

// Run is a direct alias for Recover.
func Run(p Params, cb Callback, opts Options) (*Result, error) { return Recover(p, cb, opts) }

// Attack is a direct alias for Recover.
func Attack(p Params, cb Callback, opts Options) (*Result, error) { return Recover(p, cb, opts) }

// Solve is a direct alias for Recover.
func Solve(p Params, cb Callback, opts Options) (*Result, error) { return Recover(p, cb, opts) }

// VerifyCandidate reports whether m is in [0,N) and m^e mod N == c. This is the
// mandatory final acceptance check before claiming success.
func VerifyCandidate(N, E, C, m *big.Int) bool {
	if N == nil || E == nil || C == nil || m == nil {
		return false
	}
	if m.Sign() < 0 || m.Cmp(N) >= 0 {
		return false
	}
	return new(big.Int).Exp(m, E, N).Cmp(C) == 0
}

// CheckQuery validates the callback wire input contract 0 <= x < N.
func CheckQuery(x, N *big.Int) bool {
	return x != nil && N != nil && x.Sign() >= 0 && x.Cmp(N) < 0
}

// AdaptBool adapts a callback returning (true=odd LSB 1, false=even LSB 0).
func AdaptBool(f func(x *big.Int) (odd bool, err error)) Callback {
	return func(x *big.Int) (Parity, error) {
		odd, err := f(x)
		if err != nil {
			return ParityRejected, err
		}
		if odd {
			return ParityOdd, nil
		}
		return ParityEven, nil
	}
}

// RecoverIntE is the positional convenience entry for an int public exponent
// and a generic int-parity callback.
func RecoverIntE(N *big.Int, e int, C *big.Int, query func(*big.Int) (int, error), opts ...Options) (*Result, error) {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	return Recover(Params{N: N, EInt: e, C: C}, AdaptFunc(query), o)
}

// AttackBuilder is a fluent host-facing constructor.
type AttackBuilder struct {
	ctx  context.Context
	p    Params
	cb   Callback
	opts Options
}

// NewAttack starts a fluent recovery builder.
func NewAttack() *AttackBuilder {
	return &AttackBuilder{ctx: context.Background()}
}

// Context sets the run context.
func (b *AttackBuilder) Context(ctx context.Context) *AttackBuilder {
	if ctx != nil {
		b.ctx = ctx
	}
	return b
}

// Params sets the full parameter set.
func (b *AttackBuilder) Params(p Params) *AttackBuilder { b.p = p; return b }

// N sets the public odd modulus.
func (b *AttackBuilder) N(N *big.Int) *AttackBuilder { b.p.N = N; return b }

// E sets the public exponent as *big.Int.
func (b *AttackBuilder) E(E *big.Int) *AttackBuilder { b.p.E = E; return b }

// EInt sets the public exponent from an int (rsa.PublicKey.E).
func (b *AttackBuilder) EInt(e int) *AttackBuilder { b.p.EInt = e; b.p.E = nil; return b }

// C sets the target ciphertext.
func (b *AttackBuilder) C(C *big.Int) *AttackBuilder { b.p.C = C; return b }

// Ciphertext is an alias for C.
func (b *AttackBuilder) Ciphertext(C *big.Int) *AttackBuilder { return b.C(C) }

// KeyVersion binds a key-rotation version.
func (b *AttackBuilder) KeyVersion(v string) *AttackBuilder { b.p.KeyVersion = v; return b }

// Callback sets the parity callback.
func (b *AttackBuilder) Callback(cb Callback) *AttackBuilder { b.cb = cb; return b }

// QueryFunc sets a generic int-parity callback.
func (b *AttackBuilder) QueryFunc(f func(*big.Int) (int, error)) *AttackBuilder {
	b.cb = AdaptFunc(f)
	return b
}

// Oracle sets an Oracle implementation.
func (b *AttackBuilder) Oracle(o Oracle) *AttackBuilder { b.cb = o.QueryParity; return b }

// Logger sets the safe event logger.
func (b *AttackBuilder) Logger(l Logger) *AttackBuilder { b.opts.Logger = l; return b }

// QueryBudget sets the new-call budget for this invocation.
func (b *AttackBuilder) QueryBudget(n int) *AttackBuilder { b.opts.QueryBudget = n; return b }

// MaxQueries sets the total cumulative query cap.
func (b *AttackBuilder) MaxQueries(n int) *AttackBuilder { b.opts.MaxQueries = n; return b }

// TotalQueryBudget sets the cumulative budget including checkpoint history.
func (b *AttackBuilder) TotalQueryBudget(n int) *AttackBuilder { b.opts.TotalQueryBudget = n; return b }

// MaxConsecutiveUnavailable caps consecutive unavailable retries.
func (b *AttackBuilder) MaxConsecutiveUnavailable(n int) *AttackBuilder {
	b.opts.MaxConsecutiveUnavailable = n
	return b
}

// BlindNone disables wire rerandomization.
func (b *AttackBuilder) BlindNone() *AttackBuilder { b.opts.Blinding = BlindNone; return b }

// BlindRetries rerandomizes unavailable retries (default).
func (b *AttackBuilder) BlindRetries() *AttackBuilder { b.opts.Blinding = BlindRetries; return b }

// BlindEveryQuery rerandomizes every wire attempt with a small-factor r.
func (b *AttackBuilder) BlindEveryQuery() *AttackBuilder { b.opts.Blinding = BlindEveryQuery; return b }

// BaseBlind enables scalable classic persistent base blinding.
func (b *AttackBuilder) BaseBlind() *AttackBuilder { b.opts.BaseBlind = true; return b }

// BlindFunc supplies a custom per-query blind factor generator.
func (b *AttackBuilder) BlindFunc(fn BlindFunc) *AttackBuilder { b.opts.Blind = fn; return b }

// MaxIntervals caps exact interval-set expansion.
func (b *AttackBuilder) MaxIntervals(n int) *AttackBuilder { b.opts.MaxIntervals = n; return b }

// ExpectedHistoryDigest pins resume to an externally stored digest.
func (b *AttackBuilder) ExpectedHistoryDigest(d string) *AttackBuilder {
	b.opts.ExpectedHistoryDigest = d
	return b
}

// Run executes the configured attack.
func (b *AttackBuilder) Run() (*Result, error) {
	return RecoverContext(b.ctx, b.p, b.cb, b.opts)
}

// Resume validates cp and continues the configured attack.
func (b *AttackBuilder) Resume(cp *Checkpoint) (*Result, error) {
	return Resume(b.ctx, b.p, cp, b.cb, b.opts)
}
