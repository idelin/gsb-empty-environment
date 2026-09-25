# scanfill — exact scanline polygon filling (Python 3.11, stdlib only)

Multi-ring polygons with holes and affine transforms, filled by an exact
scanline core.  Two fill rules (even-odd, non-zero winding) are supported.
Editing returns tile dirty regions and re-fills locally; layers compose
with clips and boolean masks under one unified 0/1 coverage rule.

## Exactness model (no epsilon, ever)

* Coordinates are `int` or `fractions.Fraction` (floats are rejected).
  Edge/scanline intersections are computed as exact `Fraction`s and
  sorted by exact value, so coincident edges, horizontal edges and
  vertices need no tolerance handling.
* Pixel `(px, py)` is sampled at its centre `(ox+px+1/2, oy+py+1/2)`.
* A sample point `P` is classified by a ray towards `+x`.  An edge
  crosses the ray iff `P.y ∈ [min(y0,y1), max(y0,y1))` (half-open) and
  the crossing's `x` is strictly greater than `P.x`.  This is equivalent
  to nudging `P` by an infinitesimal `(+ex, +ey)` — a fixed topological
  convention, not an epsilon patch.
* Consequences, all defined behaviour:
  * Two polygons sharing an edge partition the boundary samples:
    no cracks, no double-counted coverage.
  * Horizontal and zero-length edges never cross a ray.
  * Spikes traversed out-and-back cancel exactly in both rules.
  * A singular transform (determinant 0) collapses rings onto a line;
    signed crossings of a closed ring always balance, so the result
    covers nothing.
  * Empty rings, lone points and line "polygons" cover nothing.

## Layout

* `scanfill/geometry.py` — exact `Point` / affine `Transform`.
* `scanfill/polygon.py` — multi-ring `Polygon`, fill rules.
* `scanfill/scanline.py` — the core: `fill_row_spans` (span generator)
  and `winding_at` (independent per-point reference).
* `scanfill/canvas.py` — tiled `Canvas`; `add_polygon` / `move_vertex` /
  `remove_polygon` return dirty tile sets and re-fill only those;
  `canvas.stats` records recomputed rows/tiles.
* `scanfill/layers.py` — `Mask` boolean algebra, `Layer` (mask + clip +
  select), `composite` (binary "over" = union).

## Tests

    python3.11 -m unittest discover -s tests -v

Covers: shared-edge union equivalence (integer, diagonal-through-pixel-
centres, and rational shared edges), hole orientation under both rules,
partial re-fill after a vertex move (with recorded recomputed-row count),
negative coordinates, rational/rotation/singular transforms, degenerate
edges, scanline-vs-pointwise pixel parity on small canvases, and layer
clip/mask compositing.  See `TEST_RESULTS.txt` for an actual run.
