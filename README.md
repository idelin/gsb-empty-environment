# rectunion

Computes the **area** and **perimeter** of the union of axis-aligned
rectangles with integer coordinates.

- Closed rectangles `[X1, X2] x [Y1, Y2]`; zero-area rectangles
  (degenerate edges/points) contribute nothing.
- Sweep-line algorithm (no rasterization), exact `math/big` results.
- Overlapping edges count only their exposed parts; edges shared between
  abutting rectangles are interior and excluded from the perimeter.
  Rectangles touching only at a corner keep their full perimeters.

## Usage

```go
rects := []rectunion.Rect{
    rectunion.NewRect(0, 0, 4, 4),
    rectunion.NewRect(2, 2, 6, 6),
}
area, perimeter := rectunion.Union(rects) // 28, 24
```

Coordinates may be arbitrary-precision `*big.Int` (see `Rect`) and may be
given in any order.

## Algorithm

Sweep a vertical line along x. Between consecutive distinct rectangle
x-boundaries the cross-section is constant; its merged y-intervals give:

- area: `width * totalIntervalLength` per slab,
- horizontal perimeter: `2 * numIntervals * width` per slab,
- vertical perimeter: at each event x, the symmetric-difference length of
  the cross-sections on either side.

## Tests

`go test ./...` verifies nested, duplicate, corner-touching, and
partial-edge-contact configurations (plus a randomized fuzz) against a
unit-cell grid brute force on small integer grids.
