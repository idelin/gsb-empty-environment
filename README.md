# cutsolve

Optimal recursive integer-line cutting of binary images (up to 8x8), in Go.

An image is recursively cut along integer horizontal/vertical lines into
rectangular leaves. Each leaf is filled with its majority binary color
(ties resolve to 0); miscolored pixels are the error.

## Single frame

Among all cut trees with at most `k` leaves, `SolveSingle` minimizes:

1. miscolored pixels,
2. then the actual leaf count,
3. then the public pre-order cut encoding (below).

Exact DP over `(rectangle, exact leaf count)`; children of a cut are strictly
smaller rectangles, so area-ordered evaluation is exact. Combining per-child
optima is valid because errors add and the encoding order decomposes
lexicographically over `(cut, left, right)`.

## Two frames, one shared template

`SolveDual` requires both same-size frames to use **one common cut tree**
(independent per-frame optimization is explicitly not allowed); each leaf
still picks its own majority color per frame. Subject to at most `k` leaves
and per-frame error caps (`err0 <= cap0`, `err1 <= cap1`), it minimizes total
error `err0+err1`, then leaf count, then encoding.

The DP keeps, per `(rectangle, exact leaf count)`, the Pareto frontier of
achievable `(err0, err1)` pairs with the minimal-encoding tree per pair.
Dominated points are pruned (safe: a dominated child strictly worsens one
frame without helping the other). When no shared tree meets the caps, the
solver returns infeasibility evidence: the full Pareto frontier per leaf
count plus each frame's best achievable error.

## Public pre-order cut encoding

- leaf: `L`
- internal node: `H<pos>` or `V<pos>` (absolute cut coordinate), then the
  first child (top for `H`, left for `V`), then the second child.

Ordering: token kind `H < L < V`, then ascending position, then children
recursively (matches lexicographic string order for single-digit positions,
i.e. all images up to 8x8). Example: `V1H1LLL`.

## Verification

`BruteSingle` / `BruteDual` enumerate every cut tree with at most `k` leaves
(each tree exactly once, via its unique root cut) and recompute the optimum
independently. Tests cross-check DP vs enumeration on all sizes up to 4x4
(4x4 has ~2.8M trees), including infeasible dual instances; 8x8 runs check
DP invariants. `-verify` does the same from the CLI.

## Usage

```sh
go run ./cmd/cutsolve -mode single -k 4 -img0 "0110/1001/0000" -verify
go run ./cmd/cutsolve -mode dual -k 4 -cap 2 -img0 "0110/1001" -img1 "0011/1100" -verify
go test ./...
```

Flags: `-k` leaf budget; `-cap` both frames, `-cap0`/`-cap1` per frame
(default: unconstrained); images are rows of `0`/`1` joined by `/`.
