# gsb — guillotine-cut binary image solver

Images (max 8x8, pixels `0`/`1`) are recursively cut along integer
horizontal/vertical lines into rectangular leaves. Each leaf is painted its
majority color; ties resolve to `0`.

## Public preorder cut encoding

```
leaf            -> "L"
horizontal cut  -> "H" + 2-digit row + enc(top child)  + enc(bottom child)
vertical cut    -> "V" + 2-digit col + enc(left child) + enc(right child)
```

Tree ordering: lexicographic byte order of the encoding string.

## Commands

```
go run . solve1 -k K -img 0110,1100,0011,0110 [-verify]
go run . solve2 -k K -capA A -capB B -imgA ... -imgB ... [-verify]
go run . selftest [-n N] [-maxdim D] [-maxk K] [-seed S]
go test ./...
```

- `solve1`: minimizes mis-painted pixels with at most `k` leaves.
  Ties: fewer actual leaves, then smaller encoding. `-verify` re-enumerates
  every cut tree and confirms the optimum and the tie-broken tree.
- `solve2`: both frames share **one** cut tree (independent per-frame
  optimization is not allowed); leaf colors are chosen per frame. Minimizes
  total error subject to `errA <= capA` and `errB <= capB` and `leaves <= k`.
  Prints the exact tree, or `INFEASIBLE` (exit 3) with evidence: the minimum
  achievable per-frame errors and the full Pareto frontier of achievable
  `(errA, errB, leaves)` points. `-verify` enumerates all trees to confirm
  both feasibility and the optimal value.
- `selftest`: randomized DP-vs-enumeration cross-check on small images.

## Method

- Single frame: DP over `(rectangle, leaf budget)`; the
  `(error, leaves, encoding)` lexicographic objective has optimal
  substructure because preorder encodings concatenate.
- Two frames: DP returning a Pareto frontier of `(errA, errB, leaves, enc)`
  per `(rectangle, budget)`; frontiers are combined over all cuts and budget
  splits and pruned by dominance (equal costs keep the smallest encoding).
  The root frontier is filtered by the caps and leaf limit, then selected by
  `(total error, leaves, encoding)`.
