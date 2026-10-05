# fmproj — rational inequality projection tool

`fmproj` eliminates chosen variables from a system of linear inequalities
with exact rational arithmetic (Fourier–Motzkin elimination) and prints the
equivalent constraint set over the remaining variables. Strict (`<`, `>`)
and non-strict (`<=`, `>=`, `==`) boundaries are distinguished and tracked
through elimination.

## Guarantees

- **Exactness**: all arithmetic uses `math/big.Rat`; no floating point.
- **Provenance**: every output constraint carries the list of source
  constraints and the positive multipliers used to derive it.
- **Redundancy**: tautologies, exact duplicates and dominated constraints
  are removed, but each removal is recorded in `removed` together with its
  provenance and the reason. Optional `--lp-reduce` also removes
  LP-implied constraints and records the achieved bound as evidence.
- **Budget**: if the number of generated combinations exceeds the budget,
  the tool reports `budget_exceeded` and returns **no** constraints —
  a partial projection would not be equivalent to the input.
- **Verification**: an independent exact rational simplex solver
  cross-checks the projection:
  - feasibility of the original and projected systems must agree
    (empty-set detection, strict constraints handled via an exact
    delta-maximization formulation);
  - boundedness of both systems is reported via recession-cone LPs;
  - small rational sample points in the remaining-variable space are
    evaluated against the projected constraints and compared with an
    independent feasibility query on the original system;
  - opposite inequality pairs (`a.x <= r` and `-a.x <= -r`) are reported
    as implicit equalities.
  If any check fails, the status becomes `verification_failed` and the
  result must not be trusted.

## Input format (JSON, file argument or stdin)

```json
{
  "vars": ["x", "y", "z"],
  "eliminate": ["z"],
  "budget": 1000,
  "samples": 500,
  "lp_reduce": false,
  "constraints": [
    {"id": "c1", "coeffs": {"x": "1", "y": "1", "z": "-1"}, "op": ">=", "rhs": "0"}
  ]
}
```

- `op` is one of `<=`, `<`, `>=`, `>`, `==` (equalities are split into two
  opposite inequalities).
- Coefficients and right-hand sides are exact rationals (`"3"`, `"-2/5"`,
  `"1.25"` are all accepted).

## Output

- `status`: `ok`, `infeasible`, `budget_exceeded`, or `verification_failed`.
- `constraints`: projected constraints with `op` (`<` or `<=`) and
  `provenance` (alternative derivations, each a list of `{id, mult}`).
- `removed`: redundant constraints with provenance and removal reason.
- `contradiction`: for `infeasible`, the derived false constraint
  (e.g. `0 < 0`) with its provenance.
- `verification`: feasibility agreement, boundedness flags, number of
  sample points checked, mismatches (must be empty), implicit equalities.

Exit codes: `0` ok/infeasible, `1` usage error, `2` budget exceeded,
`3` verification failed.

## Usage

```sh
go build -o fmproj .
./fmproj examples/projection.json
./fmproj --budget 5000 --samples 1000 --lp-reduce < input.json
go test ./...
```
