# decimal

Immutable arbitrary-precision decimal arithmetic in Go (stdlib `math/big` only).

A `Decimal` is an exact value `mant * 10^exp`. Every operation copies its
operands — a `Decimal` is never mutated after construction, so values and
expression nodes can be shared freely without big.Int reuse polluting
results.

## Features

- **Exact add/sub/mul across different scales** — `Decimal.Add/Sub/Mul`
  align exponents exactly; `Context.Add/Sub/Mul` additionally return the
  value rounded to a chosen precision.
- **Exact + rounded in one result** — every `Context` operation returns an
  `Outcome{Exact, Rounded}`: the exact value when representable, plus the
  value rounded to `Context.Precision` fractional digits.
- **Four rounding modes** — `ToZero`, `ToPosInf`, `ToNegInf`, `HalfEven`
  (ties-to-even), correct for negative halves (`-2.5 → -2`, `-1.5 → -2`).
- **Budget-aware division** — exactness is decided by factoring the reduced
  denominator (only 2s and 5s terminate), never by truncating digits. If
  the exact expansion needs more than `Context.MaxScale` fractional digits,
  `Outcome.Exact` is `nil` — the library never pads zeros and fakes
  exactness. `Outcome.Rounded` is always the correctly rounded quotient.
- **Division by zero is separate** — `ErrDivisionByZero`, never a `Decimal`.
- **Expression DAGs** — `Const/Add/Sub/Mul/Neg/Div` build shareable nodes;
  `Eval` applies per-node (intermediate) rounding under a `Context` and
  propagates inexactness upward.

## Example

```go
ctx := decimal.Context{Precision: 4, Mode: decimal.HalfEven, MaxScale: 10}

o, _ := ctx.Div(mustParse("1"), mustParse("3"))
// o.Exact == nil (non-terminating), o.Rounded == 0.3333

o, _ = ctx.Div(mustParse("1"), mustParse("8192"))
// o.Exact == nil: 1/8192 needs 13 digits > MaxScale 10 — not faked.
// o.Rounded == 0.0001 (correctly rounded, not truncated).

ctx.MaxScale = 13
o, _ = ctx.Div(mustParse("1"), mustParse("8192"))
// o.Exact == 0.0001220703125, o.Rounded == 0.0001

shared := decimal.Mul(decimal.Const(a), decimal.Const(b))
e := decimal.Add(shared, decimal.Div(shared, decimal.Const(c)))
out, err := e.Eval(ctx) // shared subtree is safe to reuse and re-evaluate
```

## Testing

`go test ./...` cross-checks the library against an independent
`math/big.Rat` reference: negative half values under every mode, huge
exponents (±20000), per-node intermediate precision in expression trees,
and randomized fuzzing of all operations, precisions and modes.
