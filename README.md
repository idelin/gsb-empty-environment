# Exact 2D Affine Scene Graph (Python 3.11, stdlib only)

## Files
- `affine2d.py` — exact 2D affine matrices over `fractions.Fraction`.
  Composition (`@`), exact inverse (`None` when `det == 0`), builders:
  `translation`, `scaling`, `rotation` (exact rational cos/sin, validated
  by `cos²+sin² == 1`), `quarter_turn`, `shear`. Floats are rejected so
  no rounding error can leak in; degenerate tests are exact (`det == 0`),
  never epsilon-based.
- `scene.py` — `Scene` / `Node` / `Snapshot`:
  - World-matrix cache per node, keyed on `(node, local_version)` pairs
    of the **whole ancestor path**, so mutating any ancestor invalidates
    exactly its descendant subtree (never stale, never over-flushed).
  - `reparent(node, parent, keep_world=True)` solves
    `local = inv(parent_world) @ node_world`; under a singular parent it
    raises `UnsolvableReparentError` **before** touching any state
    (atomic). Cycles raise `CycleError`.
  - Every mutation bumps the scene version and appends to per-node
    history, enabling hit queries at any past version (`Snapshot`).
  - `hit()` returns `HitStatus.NOT_INVERTIBLE` for singular world
    matrices — no pseudo-inverse is ever substituted.
- `test_affine_scene.py` — unittest suite, including a randomized fuzz
  test that mutates ancestors and reparents nodes, then checks cached
  worlds, inverse-hit roundtrips, multi-view snapshots, and local
  invalidation scope against an independent from-the-root composition.
- `demo.py` — runnable end-to-end sample.

## Run
```
python3.11 demo.py
python3.11 -m unittest test_affine_scene -v
```
