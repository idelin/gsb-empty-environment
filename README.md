# Shared-vertex mesh edits with transactions and reversible patches

Pure Python 3.11+, standard library only (`fractions`, `unittest`).

## Files
- `geom.py` — exact 2D predicates on integer/rational coordinates.
- `mesh.py` — `Mesh`, operations, `Transaction`, patches, (de)serialization.
- `test_mesh.py` — unittest suite (22 tests).
- `demo.py` — runnable end-to-end sample.

## Model
- `Mesh.vertices: {vid: Point}` is the single source of truth for
  coordinates, so **moving a shared vertex updates every referencing face**.
- `Mesh.faces: {fid: [vid, ...]}` are ordered rings; operations never
  reorder a ring, so **orientation (sign of signed area) is preserved**.

## Operations
- `move_vertex(vid, x, y)` — validated against all faces containing `vid`.
- `split_edge(fid, index, new_vid, x, y)` — inserts `new_vid` on edge
  `ring[index] -> ring[index+1]`; the point must lie **strictly on** the edge
  (exact collinearity + betweenness, endpoints excluded).
- `merge_edges(fid, index)` — removes `ring[index]`, merging its two incident
  edges; **rejected if the shortcut edge makes the face self-intersect**.
  The removed vertex stays in the store (other faces may reference it).

## Exactness / degenerate cases (no epsilons)
All topology is decided with `fractions.Fraction` arithmetic. A face is
invalid iff it has: < 3 vertices, a zero-length edge, a repeated vertex
position, two non-adjacent edges sharing any point, or zero signed area.
Consecutive collinear edges are legal. A point 10^-12 off an edge is exactly
*not* on the edge (see `test_split_point_off_edge_rejected_exactly`).

## Transactions & patches
`Transaction.commit()` applies ops in order; on any failure every completed
op is undone via its inverse patch — the mesh is left byte-for-byte equal
(vertex IDs and coordinates). Each op returns a structured forward patch;
`invert_patch` + `apply_patch` restore the exact prior state.
`encode_patch`/`decode_patch` make patches JSON-safe.

## Run
```
python3 demo.py            # sample scenario
python3 -m unittest -v     # test suite
```
