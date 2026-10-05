# dtri — exact incremental Delaunay triangulation for integer survey points

Pure Go standard library. Two-dimensional integer coordinates of arbitrary
size (`math/big`), incremental point insertion, counter-clockwise triangles,
edge adjacency, convex hull, and an independent validator. Duplicate,
collinear and cocircular data can never produce flipped faces or
order-dependent topology.

## Layout

- `predicate.go` — exact `orient` / `incircle` predicates (big.Int, no overflow possible)
- `triangulate.go` — canonicalization + Bowyer-Watson incremental construction
- `validate.go` — independent validator (`Validate`)
- `io.go` — JSON input/output
- `cmd/dtri` — CLI front end
- `triangulate_test.go` — acceptance tests
- `examples/` — sample JSON inputs

## Public determinism contract (canonical order + cocircular tie-break)

The output is a function of the input **multiset**, not of the input order:

1. Points with identical coordinates are merged; all source IDs are kept and
   sorted.
2. Distinct points are sorted lexicographically by `(x, y)`. This canonical
   order — never the input order — drives the incremental construction.
3. Points are inserted one at a time (Bowyer-Watson cavity + retriangulation).
   When the inserted point lies **exactly on** a circumcircle (cocircular
   case), it is treated as **strictly inside** that circle. This is the
   symbolic-perturbation rule "the later point in canonical order wins the
   tie", equivalent to the regular triangulation with infinitesimal weights
   decreasing in canonical order. Because the inserted point is always the
   newest, the rule is consistent, and the final triangulation is canonical.

Degenerate inputs: zero/one/two distinct points and all-collinear sets are
reported with `status` = `empty` / `point` / `segment` / `collinear`; the
`chain` field lists all points ordered along the line and no triangles are
emitted.

The super-triangle used by the construction is placed at a distance
`>= 6*M^3` around the bounding box (M = bbox side). For integer coordinates
every non-collinear triple has doubled area >= 1, so every circumradius is
`< 2*sqrt(2)*M^3`; the bound proves super-triangle vertices can never fall
inside a real circumcircle and corrupt the hull region.

## Independent validator

`Validate(out)` recomputes everything from the output with exact predicates:

- every triangle strictly CCW (positive area), all points used;
- no two triangles overlap (no vertex strictly inside another triangle, no
  proper edge crossings);
- edge adjacency symmetric and consistent with the neighbor table;
- hull = boundary edge loop, convex, and covering every point;
- Euler relations `V-E+T = 1` and `3T = 2E-B`;
- Delaunay: no fourth point strictly inside any triangle's circumcircle
  (cocircular-on is allowed);
- degenerate cases: chain is a monotone permutation of all points, all
  collinear.

## CLI

```sh
go build -o dtri ./cmd/dtri
./dtri -in examples/cocircular.json -validate          # JSON in, JSON out
cat examples/grid.json | ./dtri -validate | jq .status
```

Input: `{"points": [{"id": "A-1", "x": 12, "y": "-7"}, ...]}` — `id`
optional (string or number), coordinates are integers of arbitrary size as
JSON numbers or strings. Output: `status`, `points` (with merged `ids`),
`triangles` (`vertices` CCW + `neighbors`, -1 = hull edge), `hull` (CCW,
including collinear boundary points), `chain` (degenerate cases only).

## Reproducible experiments

```sh
go test ./...        # or: go test -v .
```

- `TestGridSubsets` — **all 512 subsets** of a 3x3 integer grid triangulated
  and validated; each subset re-run with reversed input must give
  byte-identical canonical output.
- `TestCocircularFour` / `TestCocircularTwelve` — exactly cocircular integer
  points under all 24 permutations / 25 random shuffles: identical output.
- `TestLongCollinearChain` — 500 collinear points shuffled: single ordered
  chain, no triangles.
- `TestInsertOnEdge` — points exactly on hull edges and diagonals.
- `TestLargeCoordinates` — coordinates around 2^100 and 2^127 where float64
  and int64 predicates would silently fail.
- `TestReproducible` — fixed-seed random sets (seeds 42/1234/99), shuffled
  re-runs must reproduce byte-identical output. Same seed, same result,
  every run.

Note: acceptance is based on the exact predicates and the validator
properties above — never on "the grid looks uniform".
