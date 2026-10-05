# dt — squared Euclidean distance transform

`dt` computes, for every pixel of a binary image, the squared Euclidean
distance to the nearest foreground pixel and the coordinates of that
source pixel. Ties are broken deterministically in favor of the
lexicographically smallest `(row, column)` source.

## Usage

```go
fg := [][]bool{
    {false, false, false},
    {false, true, false},
    {false, false, false},
}
res := dt.Transform(fg)
// res.Dist[r][c] — squared distance (dt.NoDist if no foreground exists)
// res.SrcR[r][c], res.SrcC[r][c] — source pixel (dt.NoSource if none)
```

## Algorithm

The separable parabola-envelope transform of Felzenszwalb and
Huttenlocher: one 1D transform per row, then one per column, for
`O(height × width)` total time. Parabola intersection points are stored
as exact rationals and all distance and intersection comparisons are done
with integer cross-multiplication — no floating point is used anywhere.

Lexicographic tie-breaking falls out of the pass order: the row pass
resolves ties toward the smaller column, and the column pass resolves
ties toward the smaller row, which is exactly the first source coordinate.

## Testing

`go test ./...` compares the transform against an `O(n²)` brute-force
reference on every binary image of several small sizes (covering the
no-foreground, single-row/column and equidistant cases) plus randomized
larger images.
