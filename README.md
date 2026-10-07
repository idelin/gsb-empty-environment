# testselect

Go library that picks a minimum-cost set of tests covering all required
faults, by exhaustive subset enumeration (at most 20 tests).

## Model

Each `Test` has an ID, a non-negative cost, the faults it covers, and two
relations:

- `Requires`: prerequisite tests, pulled in transitively and billed.
- `Excludes`: mutually exclusive tests (symmetric); a selection whose
  prerequisite closure contains a conflicting pair is rejected.

## Selection rule

Among all exclusion-free prerequisite closures, `Select` prefers:

1. the most covered faults,
2. the lowest total cost (prerequisites included),
3. the lexicographically smallest sorted ID list (tie-break).

Faults no feasible selection can cover are returned in `Result.Missing`.

## Usage

```go
res, err := testselect.Select([]testselect.Test{
    {ID: "setup", Cost: 4},
    {ID: "main", Cost: 1, Covers: []string{"f1"}, Requires: []string{"setup"}},
    {ID: "solo", Cost: 6, Covers: []string{"f1"}},
}, []string{"f1"})
// res.Selected = ["main", "setup"], res.TotalCost = 5, res.Missing = nil
```

## Test

```sh
go test ./...
```
