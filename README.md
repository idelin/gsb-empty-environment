# rectcover

Exact solver for the lattice rectangle cover problem.

Given target and forbidden points on a finite integer grid, find
axis-aligned lattice rectangles that cover every target and contain no
forbidden point. Rectangles may overlap; overlapping lattice points are
charged once per covering rectangle. The objective is lexicographic:
first minimize total area, then the number of rectangles.

## Method

1. **Candidate enumeration** (`EnumerateCandidates`): every rectangle whose
   edges are tight against target coordinates, that covers at least one
   target and no forbidden point. Shrinking any feasible rectangle onto its
   covered targets never hurts, so an optimal solution exists among these
   candidates.
2. **Exact search** (`Solve`): depth-first branch and bound. The next
   uncovered target with the fewest covering candidates is branched on;
   incumbents prune by (area, count) with a per-target minimum-area lower
   bound. Budgets (`MaxArea`, `MaxRects`, `MaxNodes`) can interrupt the
   search, reported as `infeasible` (proven within budget) or
   `budget_exhausted` (node budget hit; best-so-far returned if any).
3. **Exhaustive cross-check** (`SolveExhaustive`): enumerates all candidate
   subsets (up to 22 candidates) to verify `Solve` on small grids — used in
   tests to audit gaps (uncovered targets), isolated points (targets whose
   only candidate is their own 1x1 cell), and budget interruption.

Every solution carries per-target evidence: the index of a rectangle
covering that target. `Problem.Verify` re-checks coverage, forbidden-point
avoidance, and area accounting.

## Usage

```sh
echo '{"targets": [[0,0],[2,0]], "forbidden": [[1,0]]}' | go run ./cmd/rectcover
```

```json
{
  "status": "optimal",
  "area": 2,
  "rects": [
    {"minX": 0, "minY": 0, "maxX": 0, "maxY": 0},
    {"minX": 2, "minY": 0, "maxX": 2, "maxY": 0}
  ],
  "evidence": [
    {"target": [0, 0], "rect": 0},
    {"target": [2, 0], "rect": 1}
  ],
  "nodes": 4
}
```

## Test

```sh
go test ./...
```
