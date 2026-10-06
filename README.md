# orthofill

Fill axis-aligned (horizontal/vertical) closed paths on the integer grid and
classify every cell center under the **non-zero winding rule**.

- Cell centers are integer lattice points `(x, y)`; cell `(x, y)` is the unit
  square centered there.
- Paths may self-intersect and retrace edges. An edge retraced in the opposite
  direction cancels its winding contribution; its geometry is still drawn, so
  centers on it remain boundary.
- Centers lying exactly on a drawn edge are reported separately as `Boundary`,
  never as inside/outside.
- Each subpath is closed implicitly (last vertex back to first). Every segment,
  including the closing one, must be horizontal or vertical, otherwise
  `ErrNotOrthogonal` is returned. Zero-length segments are ignored.

## API

```go
fill, err := orthofill.FillScanline(paths, area) // batched sweep
fill, err := orthofill.FillRay(paths, area)      // direct per-point ray reference
w, onEdge, err := orthofill.Winding(paths, p)    // single-point query
```

`FillScanline` gathers each row's edge crossings once and evaluates the whole
row via suffix sums; `FillRay` casts an independent `+X` ray per center. Both
use the half-open endpoint rule `minY <= y < maxY`, so a ray through a shared
vertex counts exactly one crossing. The tests cross-check the two
implementations on randomized self-intersecting multi-loop inputs and cover
shared endpoints, shared edges, nested reverse loops (holes), and
reverse-retraced edge cancellation.

## Test

```sh
go test ./...
```
