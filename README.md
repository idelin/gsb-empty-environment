# Minimum-Area Oriented Bounding Rectangle (Exact, Python 3.11)

Computes the minimum-area oriented bounding rectangle of a 2D point set with
**exact rational arithmetic only** (`fractions.Fraction`). No floating point,
no epsilon-based topological decisions anywhere. Standard library only.

## Files

- `min_area_rect.py` — library: convex hull, rotating calipers, certificate,
  brute-force reference, certificate verifier, mutable `PointSet`.
- `test_min_area_rect.py` — `unittest` suite (28 tests).
- `demo.py` — runnable sample (`python3.11 demo.py`).

## Design

**Pipeline.** Monotone-chain convex hull (exact cross products) → rotating
calipers over hull edges. For each edge direction `u` (with `v = u` rotated
90°), the four extremal vertices are tracked by pointers that advance
monotonically CCW as the edge direction rotates — total O(h) pointer steps
after an exact O(h) initialisation for the first edge. The certificate area
for an edge is the exact rational `(umax-umin)*(vmax-vmin)/|u|²`.

**No epsilon, ever.** All comparisons (hull orientation, pointer advancement,
area ordering, angle ordering) are exact `Fraction` comparisons. Near-collinear
integer points at 1e9 scale cannot yield a negative or mis-rounded area.

**Canonical tie-breaking** among equal-area optima, applied in order:

1. smaller exact area;
2. smaller orientation angle in `[0, π)` — directions are normalised to the
   closed upper half-plane and compared by an exact cross-product sign test;
3. lexicographically smallest corner tuple (exact `Fraction`s).

Additionally, when several input points attain an extreme projection (a
supporting plateau), the witness extreme point is the lexicographically
smallest one. The resulting certificate is fully deterministic and invariant
under input reordering and duplication.

**Degenerate cases are defined, not patched:**

- empty input → `ValueError`;
- one distinct point → zero-area rectangle collapsed on the point;
- all points collinear → zero-area rectangle on the extreme segment
  (corners are the two endpoints, each repeated).

**Certificate.** `min_area_rectangle` returns a frozen `Certificate` with:

- `area` — exact `Fraction`;
- `corners` — the four rectangle corners, CCW, recovered exactly from the
  projection ranges via `p = (U·u + V·v)/|u|²` (u ⊥ v);
- `direction` and `supporting_edge` — the hull edge collinear with a side;
- `extreme_points` — the four arg-extremum input points (`u_min`, `u_max`,
  `v_min`, `v_max`);
- `u_range`, `v_range` — verifiable projection ranges `[min, max]` of `p·u`
  and `p·v` over the whole point set.

**Verification.** `verify_certificate(cert, points)` checks, all exactly:
optimality against `brute_force_min_area` (which enumerates every hull edge
and recomputes extremes per edge, pointer-free), projection ranges equal the
true extremes, extreme points are actual input points attaining the ranges,
the supporting edge is a real hull edge parallel to `direction`, and the four
corners recover a rectangle of exactly equal (non-negative) area via the
shoelace formula.

**Mutation.** `PointSet` assigns every insertion a distinct id (duplicate
coordinates keep separate identities), supports `add` / `add_many` /
`remove_many` (batch withdrawal; unknown ids raise `KeyError`), and
`minimum_area_rectangle()` on the current set.

## API sketch

```python
from min_area_rect import PointSet, min_area_rectangle, verify_certificate

cert = min_area_rectangle([(0, 0), (6, 2), (4, 8), (-2, 6)])
assert cert.area == 40
assert verify_certificate(cert, [(0, 0), (6, 2), (4, 8), (-2, 6)])

ps = PointSet([(0, 0), (6, 0), (6, 4), (0, 4)])
dup = ps.add((6, 4))          # duplicate coordinate, new identity
ps.remove_many([dup])         # batch withdrawal by id
assert ps.minimum_area_rectangle().area == 24
```

## Sample run (`python3.11 demo.py`, abridged)

```
Case 1: tilted rectangle (exact optimum beats axis-aligned frame)
  exact area        : 40
  supporting edge   : ((-2, 6), (0, 0))
  proj range p.u    : [-40, 0]     proj range p.v : [0, 40]
  corners (CCW)     : (-2, 6) (0, 0) (6, 2) (4, 8)
  verify_certificate: True
  matches brute force edge enumeration: True

Case 2: near-collinear points at 1e9 scale
  exact area        : 1000000000          # exact, non-negative
  verify_certificate: True

Case 3: rational coordinates
  exact area        : 5715/5576
  corners (CCW)     : (-2/3, 7/8) (892/2091, -1561/2788) ...

Case 4/5: single point / collinear  ->  exact area 0, degenerate corners defined
Case 6  : batch withdrawal + duplicate identity + removing unique extreme point
Case 7  : empty input -> ValueError: minimum-area rectangle is undefined for an empty point set
```

## Test results (`python3.11 -m unittest test_min_area_rect -v`)

```
test_interior_points_removed ... ok
test_single_point ... ok
test_certificate_rejects_wrong_area ... ok
test_random_rational_sets_match_brute_force ... ok
test_random_sets_match_brute_force ... ok
test_all_identical_points ... ok
test_collinear_zero_area_no_negative ... ok
test_empty_raises ... ok
test_single_point_zero_area ... ok
test_two_points_zero_area ... ok
test_near_collinear_large_integers ... ok
test_near_collinear_tilted_large_integers ... ok
test_no_epsilon_area_comparison ... ok
test_axis_aligned_square ... ok
test_corners_recover_equal_area_rectangle ... ok
test_rational_coordinates ... ok
test_rectangle_beats_axis_frame ... ok
test_rotated_square_is_its_own_minimum ... ok
test_batch_withdrawal ... ok
test_batch_withdrawal_multiple ... ok
test_duplicate_identity ... ok
test_remove_unique_extreme_point ... ok
test_unknown_id_raises ... ok
test_duplicate_coordinates_same_certificate ... ok
test_input_order_invariance ... ok
test_regular_hexagon_deterministic ... ok
test_square_picks_zero_angle ... ok
test_collinear_collapses_to_endpoints ... ok

----------------------------------------------------------------------
Ran 28 tests in ~17s

OK
```

The randomized suites cross-validate the rotating-calipers result against the
per-hull-edge brute-force reference (integer and rational coordinates),
including near-collinear 1e9-scale inputs, collinear families of every small
slope, input-order invariance, and duplicate-coordinate invariance.
