# cubediff

Compares two boolean switch-configuration expressions (up to 10 variables)
and prints **all minimal condition cubes** under which they differ.

- Every assignment covered by a reported cube makes the two expressions
  evaluate differently (soundness, checked against the full truth table).
- No literal can be removed from a reported cube without covering an
  assignment where the expressions agree (minimality / prime implicants).
- Together the cubes cover every differing assignment (completeness).

## Usage

```
go run . 'EXPR_A' 'EXPR_B'
```

Operators: `! ~ not`, `& && and`, `| || or`, `^ xor`, parentheses,
constants `0 1 true false`. Variables are identifiers (e.g. `feature_x`).

Example:

```
$ go run . 'a & (b | c)' 'a & b | c'
variables (3): [a b c]
differing assignments: 2 of 8
minimal cubes (1):
  a=0 c=1
```

## Method

1. Parse both expressions and evaluate them over the full truth table of
   the sorted variable union (2^n rows, n <= 10).
2. diff = A XOR B per row.
3. Enumerate all 3^n cubes; keep those entirely inside the diff onset.
4. Keep only minimal ones: dropping any single literal must exit the onset.
5. `VerifyCubes` re-checks soundness, minimality, and completeness against
   the truth table before printing.

## Tests

```
go test ./...
```

Covers parser behavior, fixed expression pairs (equivalence, XOR,
consensus terms, 10 variables), independent brute-force re-checks of
minimality/coverage, and 300 randomized expression pairs verified against
the truth table.
