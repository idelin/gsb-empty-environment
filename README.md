# Texture Atlas Solver (exact, C++17)

Packs up to 12 rectangles into a square atlas of **provably minimal integer
side length**. Textures may be rotated 90 degrees. Every texture must be
surrounded by `pad` isolation (padding) pixels, including along the atlas
border.

## Isolation modes

- `shared` — the isolation strip between two neighbouring textures may be
  reused by both: any pair of textures must be separated by at least `pad`.
- `exclusive` — each texture owns its isolation zone: any pair must be
  separated by at least `2 * pad`.

## Build & run

```sh
make
printf '2 1 shared\n2 2\n2 2\n' | ./atlas_solver
```

Input (stdin):

```
n pad mode        # 1 <= n <= 12, pad >= 0, mode: shared|0 or exclusive|1
w h               # n lines, texture sizes (>= 1)
```

Output (stdout): minimal side length `S`, then one `x y rot` line per texture
(in input order; `rot` = 1 if rotated 90 degrees).

```
7
1 1 0
1 4 0
```

## Independent collision checker

`src/checker.cpp` shares no logic with the solver. It re-reads a plain-text
spec and validates the raw geometric constraints directly (border padding +
pairwise separation with the mode-dependent gap). The solver refuses to print
a placement that the checker rejects, and the checker is also usable
standalone:

```sh
printf '7\n2 1 shared\n2 2\n2 2\n1 1 0\n1 4 0\n' | ./atlas_solver --check
# prints OK (exit 0) or FAIL: <violations> (exit 1)
```

Check-spec format: `S` on line 1, then `n pad mode`, then n lines `w h`,
then n lines `x y rot`.

## Why the result is exact (not a heuristic)

1. Requiring pairwise separation `gap` is equivalent to packing the expanded
   boxes `(w_i+gap) x (h_i+gap)` without overlap. A packing of those boxes in
   a square of side `L` is an atlas of side `S = L + 2*pad - gap`, so
   minimising `S` is minimising `L`.
2. For a fixed `L`, feasibility is decided by a **complete** backtracking
   search: any packing of integer-sided boxes can be compacted (push boxes
   left/down until each rests on the border or another box), after which
   every coordinate is a subset sum of box sides. The search enumerates
   exactly those candidate coordinates, so it finds a packing whenever one
   exists.
3. `L` is swept upward from a lower bound (area bound and largest box side);
   the first feasible `L` is therefore minimal, and so is `S`.

## Tests

```sh
./tests/run_tests.sh [seed] [trials]
```

Runs known-answer cases plus a randomised cross-check: the C++ solver's
minimal `S` is compared against `tests/brute.py`, an independent brute-force
exact solver written directly from the problem semantics, and every emitted
placement is re-validated through `--check`.

## Files

- `src/main.cpp` — CLI + exact solver
- `src/checker.h` / `src/checker.cpp` — independent collision checker
- `tests/brute.py` — independent brute-force exact solver (cross-check)
- `tests/random_crosscheck.py` — randomised differential test driver
- `tests/run_tests.sh` — test entry point
