# binsolve

Exact solver for balanced bin packing on discrete positions.

Each item has a weight; every bin offers the same discrete slot positions and
holds each item in at most one slot. A bin is feasible when its total weight
is within capacity **and** its center of gravity lies inside the safe
interval. The solver minimizes, lexicographically:

1. the number of bins, then
2. the maximum eccentricity `|cog - target|` across bins
   (`target` defaults to the midpoint of the safe interval).

Supports at most 18 items. All arithmetic uses exact fractions
(`math/big.Rat`); no floating-point tolerances are involved anywhere,
including the independent verification of the emitted placement.

## Usage

```
go build -o binsolve .
./binsolve example.json        # or: cat example.json | ./binsolve
```

Input (JSON; every rational accepts `"3/2"`, `1.5`, or `"7"`):

- `items`: list of `{id, weight}` (1..18 items, positive weights)
- `positions`: slot coordinates available in every bin
- `capacity`: max total weight per bin
- `safe_interval`: `[lo, hi]` allowed center-of-gravity range per bin
- `target` (optional): balance reference point for eccentricity

Output (JSON):

- `bins`, `max_eccentricity`: exact optima (fractions as strings)
- `placement`: per bin, item → slot assignments plus the exact total
  weight, center of gravity, and eccentricity — the placement evidence
- `verification`: independent exact re-check of the evidence (item
  coverage, slot distinctness, capacity, recomputed center of gravity
  inside the safe interval, recomputed eccentricities)

## Method

Branch and bound over set partitions (one bin at a time, symmetry-broken
by always packing the lightest-indexed remaining item first and by
collapsing equal-weight items). For each candidate bin, an exact
branch-and-bound slot assignment minimizes eccentricity, pruned by
rearrangement-inequality bounds on the achievable center of gravity.
Results are cached per item subset.

## Tests

```
go test ./...
```

Includes cross-checks of the solver against exhaustive enumeration on
random small instances with non-dyadic rational weights.
