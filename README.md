# Exact Rectilinear Geometry

Python 3.11 standard-library code for axis-aligned rectangles with integer or
rational coordinates. It computes exact union area, boundary perimeter, outer
perimeter, and oriented outer and hole rings.

## Run

```sh
python3.11 example.py
python3.11 -m unittest -v
```

## Semantics

- Rectangles are closed sets, so shared edges or corner points connect only
  according to actual cell topology.
- Coordinates are converted to `fractions.Fraction`; floating point is rejected.
- Zero-width or zero-height rectangles contribute no area, perimeter, or
  outer contour.
- `perimeter` includes every boundary component, including enclosed holes.
- `outer_perimeter` includes outer contours only.
- Outer rings are counterclockwise in Cartesian coordinates; hole rings are
  clockwise.

For two side-adjacent unit squares, the exact result is area `2` and perimeter
`6`. Two corner-touching unit squares remain two separate four-point rings,
rather than being joined into a self-intersecting polygon.
