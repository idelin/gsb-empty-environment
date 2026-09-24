# bezier_flatten

Subdivide quadratic/cubic Bezier curves into polylines with a **certified**
geometric error bound, using exact rational arithmetic (Python 3.11,
standard library only).

## Guarantees

- **Certified error**: flatness uses a closed-form geometric bound on the
  distance between the curve and its chord segment (see the module
  docstring for the derivation). No epsilon tuning anywhere; all
  comparisons are exact `Fraction` inequalities.
- **Parameter intervals**: `FlattenResult.parameters` / `.intervals` give
  dyadic-rational knots covering `[0, 1]` contiguously.
- **Endpoint continuity**: consecutive segments share endpoints exactly;
  every polyline vertex lies exactly on the curve.
- **Degenerate cases are defined**: collinear ordered control points finish
  in one segment (bound is exactly zero); collinear *overshooting* curves
  keep subdividing; coincident points and closed curves (`P0 == Pn`) work.
- **Cusps**: the distance bound stays positive at a cusp, so subdivision
  continues there (verified by tests with a cusp at `t = 1/3`).
- **Depth cap**: if `max_depth` is reached with the tolerance unmet,
  `FlattenResult.converged` is `False` and the offending parameter
  intervals are listed in `unmet_intervals`.

## Usage

```python
from fractions import Fraction
from bezier_flatten import flatten_quadratic, flatten_cubic

r = flatten_cubic((0, 0), (1, 4), (5, -3), (6, 2), Fraction(1, 50))
r.converged        # True
r.intervals        # parameter intervals, one per polyline segment
r.points           # exact Fraction vertices on the curve
```

Coordinates may be `int`, `Fraction`, `float` (exact binary value) or
numeric strings; everything is converted to `Fraction`.

## Run

```
python3.11 example.py                  # demo
python3.11 -m unittest test_bezier_flatten -v   # tests
```
