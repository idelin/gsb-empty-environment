# primeinterp

Polynomial interpolation over a prime field GF(p), plus a revision-aware
sample stream that tracks the current interpolant.

## Interpolation (`interp.go`, `field.go`)

- `Interpolate(p, pts)` returns the coefficients (constant term first) of
  the unique lowest-degree polynomial through the points. Duplicate
  x-coordinates are allowed when they agree on y; a repeated x with a
  different y is rejected with `*ConflictError`. Trailing zero
  coefficients are trimmed (the zero polynomial is the empty slice).
- `Evaluate(p, coeffs, x)` evaluates with Horner's rule.
- All modular arithmetic reduces negative inputs into `[0, p)`; `p` is
  validated as prime (deterministic Miller–Rabin for int64).

## Sample stream (`stream.go`)

- `Stream.Apply(Sample{ID, Revision, X, Y})` folds revisioned samples
  into the stream. A newer revision for an existing ID revokes the old
  contribution; stale or equal revisions are ignored. The tracked
  lowest-degree polynomial can therefore shrink after a correction.
- `Stream.Snapshot()` captures an immutable view; `Snapshot.Query()`
  reports one of three states:
  - `StatusInsufficient` — no live samples;
  - `StatusConsistent` — unique interpolant, canonical `Coeffs` returned;
  - `StatusContradictory` — duplicate x with different y, offending
    x-coordinates listed in `Conflicts`.
- Snapshots are bound at capture time: later `Apply` calls never change
  the result of querying an older snapshot.

## Tests

`go test ./...` covers random-polynomial round trips, redundant and
contradictory samples, negative coordinates, trailing-zero trimming,
revision revocation with degree decrease, duplicate-x conflicts with
recovery, snapshot binding, and a 500-step randomized replay checked
against from-scratch interpolation after every apply.
