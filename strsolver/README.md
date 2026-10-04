# strsolver

A solver for finite-domain string constraint problems, written in Go.

## Problem model

- A finite **alphabet** (distinct bytes) and `n` string variables.
- Per variable: **length bounds** `[min, max]`, optional **prefix** and
  **suffix**.
- Global **forbidden substrings**: no variable's value may contain any of
  them. Because a concatenation result is itself a variable, this also
  forbids words formed *across the boundary* of a concatenation.
- **Concatenation equalities** `x[result] = x[left] + x[right]`.

## Solution ordering

Solutions are ordered by:

1. **total length** — the sum of all variable lengths, then
2. **tuple-lexicographic** order of `(x[0], ..., x[n-1])` with byte-wise
   string comparison.

`Solve` returns the smallest solution under this order.

## Budget semantics

The budget is counted in **search states** (nodes of the length-enumeration
tree plus nodes of the character-assignment DFS). Outcomes:

- `sat` — smallest satisfying assignment found (independently checkable
  with `Verify`),
- `unsat` — the search space was *fully* explored, no solution exists,
- `unknown` — the budget was exhausted first; an incomplete search is
  **never** reported as `unsat`.

`budget <= 0` means unlimited.

## Library use

```go
p := &strsolver.Problem{
    Alphabet:  "ab",
    Vars:      []strsolver.VarBounds{{Min: 1, Max: 1, Prefix: "a"}, {Min: 1, Max: 1}, {Min: 2, Max: 2}},
    Forbidden: []string{"ab"},
    Concat:    []strsolver.ConcatEq{{Left: 0, Right: 1, Result: 2}},
}
res := strsolver.Solve(p, 1000000)        // sat: ["a" "a" "aa"]
err := strsolver.Verify(p, res.Assignment) // independent check: nil
```

## CLI

```
strsolver solve  < problem.json        # {"status":"sat","assignment":[...],"states":N}
strsolver verify problem.json asg.json # "ok" or "invalid: ..." (exit 1)
```

Problem JSON: `{"alphabet":"ab","vars":[{"min":0,"max":2,"prefix":"a"}],
"forbidden":["aa"],"concat":[{"left":0,"right":1,"result":2}],"budget":1000000}`.
`solve` exits 3 when the status is `unknown`.

## Testing

`go test ./...` runs unit tests plus a randomized cross-check of `Solve`
against `BruteForce` (exhaustive enumeration) on small instances, including
cases where forbidden substrings span concatenation boundaries.
