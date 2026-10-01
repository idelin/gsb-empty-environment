# errbound-judge

Exact threshold judgement for measurements that carry error bounds.
TypeScript, zero dependencies, Node.js built-in test runner (`node:test`).
All arithmetic is exact rational arithmetic on **BigInt** — values never
transit through `Number`, and rounding only ever happens explicitly.

## Requirements & running

- Node.js >= 22.6 (uses native TypeScript type stripping; no build step,
  no dependencies, no network needed).

```sh
npm test          # node --experimental-strip-types --test test/*.test.ts
npm run example   # node --experimental-strip-types examples/demo.ts
```

## Core semantics

- A **measurement** is `value ± error`. `error = null` means the error is
  **unknown**: the interval is unbounded and is *never* treated as 0 — any
  verdict that would need the bound becomes `needs-review`.
- A **constraint** is a rational linear form vs a threshold with an explicit
  open/closed sense (`<`, `<=`, `>`, `>=`). Exactly touching a threshold is
  decided by the sense: `<=` passes, `<` does not.
- **Single-constraint classification** (`classifyConstraint`):
  `determinate-pass` / `determinate-fail` / `needs-review`. An interval
  straddling the threshold is `needs-review` — never passed by centre value.

## Joint analysis over shared error variables

`compileSystem` maps every measurement to one shared deviation variable
`d_i ∈ [-err_i, +err_i]`, so `analyzeJoint` reasons about the **joint
feasible region** — per-constraint "possibly passes" are never merged into
an overall pass:

- `determinate-pass` — every error assignment in the box satisfies all
  constraints. **Certificate**: per-constraint worst-case box vertex and
  margin (`verifyPassCertificate`).
- `determinate-fail` — no assignment satisfies all constraints.
  **Certificate**: Farkas multipliers, a non-negative combination of the
  source inequalities deriving `0 <= c < 0` (or strict `0 < c <= 0`),
  produced by exact Fourier–Motzkin elimination (`verifyFarkas`).
- `mixed` — both kinds of points exist. **Witnesses**: one satisfying and
  one violating assignment, both exactly re-checkable
  (`verifyJointWitnesses`).
- `undecided` — the elimination budget (`maxInequalities`) was exhausted;
  reported honestly instead of guessing.

## Small-domain enumeration

`verifyByEnumeration` independently re-checks small (<= 4 variables), fully
bounded systems: all `2^n` box vertices for the universal check (exact —
a linear form is maximised at a vertex), and all `n`-face intersections
(exact rational Gaussian elimination) plus a midpoint grid for the
existential check. Strict-only interior feasibility is found via the grid;
if nothing is found while strict constraints are present the report says
`inconclusive` rather than guessing. `crossCheck` compares the enumeration
report with the joint verdict and lists any inconsistency. Unbounded or
large domains are skipped with an explicit reason.

## Evidence and incremental revocation

`EvidenceStore` caches each judgment with a dependency snapshot
(measurement versions + constraint-set version). Changing a measurement or
constraint **revokes** stale judgments — entries are never mutated — and
every `issued`/`revoked` event is appended to an audit log, so historical
determinations remain available (`historyOf`, `history`).

## Exact numbers and explicit rounding

- `Rational` keeps `num/den` as coprime BigInts; parsing (`Rational.parse`)
  reads decimal/scientific strings digit-by-digit into BigInt.
- `toDecimal(scale, mode)` requires an explicit `RoundingMode`:
  `floor | ceil | towardZero | awayFromZero | halfUp | halfEven`.
  It is the *only* place rounding exists, and it never feeds back into
  arithmetic.

## Sample output (`npm run example`)

```text
1) Per-constraint classification (interval semantics, no centre-value pass)
  p-max: determinate-pass  -- worst case 99/10 satisfies <= 10
  t-open: needs-review  -- interval [77, 83] straddles threshold 83; ...
  f-max: needs-review  -- unknown error makes the interval unbounded; ...
2) Joint analysis over SHARED deviation variables
  [joint] status = mixed
    satisfying witness (deviations): [0, 0, 0]
    violating  witness (deviations): [0, 3, 0]
3) Shared error variable: per-item 'possibly pass' does NOT merge
  per-item: lo=needs-review, hi=needs-review
  [joint(m>=1, m<=-1)] status = determinate-fail
    Farkas certificate: derived 0 <= -2 from
      1 x constraint:hi
      1 x constraint:lo
6) Evidence: measurement updates revoke stale judgements, history kept
  t0: temp=9+/-0.5   -> determinate-pass
  t1: temp=10.5+/-0.5 -> mixed (old certificate revoked)
7) Exact decimals: no Number transit, explicit rounding only
  0.1 + 0.2 = 3/10 (exact), not 0.30000000000000004
  9007199254740993^2 = 81129638414606699710187514626049 (exact BigInt)
```

## Test results (`npm test`)

41 tests, all passing (Node v23.9.0):

```text
ℹ tests 41
ℹ pass 41
ℹ fail 0
```

Coverage: exact rational arithmetic & rounding modes (`test/rational.test.ts`),
interval classification incl. boundary/unknown-error rules
(`test/classify.test.ts`), joint verdicts with certificates, witnesses and
budget (`test/solver.test.ts`), enumeration cross-checks
(`test/enumerate.test.ts`), incremental evidence revocation & history
(`test/evidence.test.ts`).

## Layout

- `src/rational.ts` — BigInt exact rationals, explicit rounding
- `src/interval.ts`, `src/measurement.ts` — intervals, versioned measurements
- `src/constraint.ts`, `src/system.ts` — constraints, deviation-space compile
- `src/classify.ts` — single-constraint interval classification
- `src/fm.ts` — exact Fourier–Motzkin: witness, Farkas certificate, budget
- `src/solver.ts` — joint verdicts + certificates/witnesses
- `src/enumerate.ts` — small-domain enumeration verifier & cross-check
- `src/evidence.ts` — evidence store: incremental revocation, audit history
