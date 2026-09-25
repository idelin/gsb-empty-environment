# chronodur

Exact duration expressions in pure Python 3.11 (standard library only).
Parses day/hour/minute/second and rational-second expressions, keeps
calendar and fixed quantities strictly separate, compiles expressions to
composable operation sequences, and evaluates them against synthetic
offset-transition tables — all with exact rational arithmetic.

## Three kinds of time

| Type | Meaning | Representation |
|---|---|---|
| `Duration` | a length of time | `(months, cal_days, fixed)` — calendar months, calendar days, exact fixed seconds (`Fraction`) |
| `Instant` | an absolute point in time | exact `Fraction` seconds since the Unix epoch |
| `WallTime` | a local wall-clock reading in a zone | naive `datetime` + `SyntheticZone` + exact sub-second `Fraction` |

`Instant - Instant` yields a `Duration`; a `WallTime` is never an
`Instant` until resolved through its zone's offset table.

## Units and explicit disambiguation

| Unit | Kind | Meaning |
|---|---|---|
| `mo` | calendar | calendar month (month-end clamping) |
| `cd` | calendar | calendar day (not always 24 h) |
| `d` | fixed | fixed 24-hour day, exactly 86400 s |
| `h`, `min`, `s` | fixed | 3600 s, 60 s, 1 s |

Seconds accept exact rationals: `1/3s`, `0.5s` (parsed as `Fraction`,
never floats). Ambiguous bare units are rejected with a position and a
suggestion: `1D` → use `cd` or `d`; `1m` → use `mo` or `min`.

## Grammar

```
expr     := ("-" | "+")? sum
sum      := term (("+" | "-") term)*
term     := unary ("*" unary)*
unary    := "(" expr ")" | quantity | number
quantity := (number unit)+
number   := INT | INT "." INT | INT "/" INT
```

A leading `-` negates the **whole** expression — `-1h30min` is
`-(1h 30min)`, never `(-1h) + 30min`. Mixed signs inside require the
explicit binary operators (`1mo - 3h`). Bare numbers cannot mix with
durations (`1 + 1h` is an error), and two durations cannot be
multiplied (`2h * 3h` is an error).

## Semantics

- **Values** (`parse_duration`) are exact triples; addition of values
  commutes. Normalization only performs transforms that provably
  preserve semantics: carrying *inside* the fixed domain
  (`60s → 1min → 1h → 1d`, all exact by definition), reducing
  fractions, merging like terms. It never turns `30cd` into `1mo`,
  `744h` into `1mo`, or `1cd` into `24h`. Normalization is idempotent
  and `parse(format(v)) == v` always.
- **Programs** (`compile_program`) preserve source order as an op
  sequence `(mo, n) | (cd, n) | (fx, seconds)`; `Program.then`
  composes sequences. Calendar ops apply in wall-clock space (month-end
  clamping: `2024-01-31 + 1mo → 2024-02-29`, and clamping is not
  reversible: `+1mo` again → `2024-03-29`). Fixed ops apply in absolute
  time through the zone's offset table.
- **Non-commutation**: calendar months and fixed seconds cannot be
  swapped. Counterexample (see demo): from `2024-03-09 12:00` in a
  US-style zone, `1mo 1d` → `2024-04-10 12:00` but `1d 1mo` →
  `2024-04-10 13:00`. Normalization therefore never reorders a program.
- **1 calendar day ≠ 24 hours** across an offset change: `1cd` over the
  spring-forward elapses 82800 s (23 h); `1d` always elapses 86400 s.

## Errors, limits, budgets

Every parse-time failure reports an exact column with a caret. Overflow
limits: calendar fields ≤ 10⁹, fixed seconds ≤ 10¹⁸, scalars ≤ 10¹⁸,
rational denominators ≤ 10¹². `Budget(max_steps, max_depth)` bounds
parser work; exponentially nested input such as `9*(9*(…1s…))` is
stopped by the overflow limit with a position, and deep parenthesization
by the depth budget. Wall-clock times in an offset gap raise
`WallTimeError`; folds resolve to the earlier instant.

## Layout

```
chronodur/
  errors.py    error types with exact source positions
  model.py     Duration value, canonical formatting
  cal.py       proleptic Gregorian month arithmetic (clamping)
  zone.py      SyntheticZone transition tables, Instant/WallTime
  program.py   ordered, composable operation sequences
  parser.py    tokenizer, recursive-descent parser, evaluator, compiler
tests/         unittest suite (65 tests)
examples/demo.py
```

## Run

```
python3.11 -m unittest discover -v   # run the test suite
python3.11 examples/demo.py          # run the guided tour
```

## Sample output (examples/demo.py)

```
== 2. Fixed 24h day vs calendar day across an offset change ==
  2024-03-09 12:00 + 1cd -> wall 2024-03-10 12:00:00  (elapsed 82800s = 23h)
  2024-03-09 12:00 +  1d -> wall 2024-03-10 13:00:00  (elapsed 86400s = 24h)

== 3. Non-commutation counterexample: calendar month vs fixed day ==
  base 2024-03-09 12:00 US-like
  +1mo then +1d -> 2024-04-10 12:00:00
  +1d then +1mo -> 2024-04-10 13:00:00
  equal? False  (values agree, ordered programs do not)

== 7. Diagnostics with exact positions ==
  parse error: ambiguous unit 'D': use 'cd' (calendar day) or 'd' (fixed 24-hour day) (column 2)
    1D
     ^
```

## Test results

```
$ python3.11 -m unittest discover
Ran 65 tests in 0.7s
OK
```

Coverage highlights: fixed-vs-calendar day across both DST directions;
non-commuting month/fixed-day counterexample; month-end clamp tables
(incl. leap years and irreversibility); parse→print→parse round trips
and idempotent normalization over generated values; gap/fold behavior;
exact rational sub-second arithmetic through application; positioned
diagnostics for every illegal mixing; budget exhaustion and exponential
nesting overflow with positions.
