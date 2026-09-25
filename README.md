# scanfill — exact scanline polygon fill (Python 3.11, stdlib only)

Scanline fill with multiple rings, holes and exact affine transforms,
supporting both **even-odd** and **nonzero winding** rules.  All
coordinates are integers or `fractions.Fraction` rationals; every
predicate is an exact rational comparison — there is **no epsilon
anywhere**.

## Layout

- `scanfill/geometry.py` — exact affine `Transform`, bbox, and the
  per-pixel oracle `point_in_rings`.
- `scanfill/raster.py` — `fill_rows`: scanline fill of a pixel rect.
- `scanfill/canvas.py` — `Layer` (rings + rule + transform + clip +
  boolean mask) and `TiledCanvas` (tile cache, dirty-region edits,
  union compositing).
- `tests/test_scanfill.py` — unittest suite (see `RESULTS.txt` for a
  recorded run).

## Coverage model (unified rule)

- Pixels are sampled at their centers `(x + 1/2, y + 1/2)`.
- Scanline intersections are computed and sorted as exact rationals;
  crossings at identical x are grouped and applied together, horizontal
  edges never generate crossings, and vertices use a half-open
  y-interval so each is counted exactly once.
- A pixel center lying exactly on an edge or vertex is **covered**
  (closed-set semantics).  Two polygons sharing an edge therefore
  compose with no cracks, and `fill(A) OR fill(B) == fill(A ∪ B)`.
- Layers are composited by boolean OR after applying per-layer clip
  rects and boolean masks as AND filters, so coverage is never double
  counted.

## Defined degenerate behaviour

- Zero-length edges are ignored; rings with fewer than two distinct
  points cover nothing.
- A singular transform (determinant exactly zero) is defined to produce
  empty coverage; edits under it dirty no tiles.
- Negative and fractional coordinates are first-class (exact
  `Fraction` arithmetic).

## Tiled editing

`TiledCanvas.move_vertex` returns the exact dirty tile set (tiles whose
pixel centers intersect the union of the old and new geometry bboxes,
computed with exact floor/ceil) and re-fills only those tiles.
`canvas.stats["rows"]` counts scanline rows actually recomputed, which
the tests use to prove there is no full-canvas fallback.

## Running the tests

```sh
python3.11 -m unittest tests.test_scanfill -v
```
