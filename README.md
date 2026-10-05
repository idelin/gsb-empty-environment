# pairrank — exact top-K ranking timeline for moving points (Go stdlib only)

For 2D points moving with constant rational velocities and a static rational
query point, `pairrank` computes the exact top-K ranking (by squared distance,
ties broken by object ID) over a rational time interval `[0,T]`.

Because coordinates, velocities and `T` are rational, every pairwise squared
distance difference is a quadratic polynomial with rational coefficients.
Event times are therefore algebraic numbers of degree <= 2 and are **not**
assumed rational: each is represented exactly as `u + v*sqrt(m)` (`u,v`
rational, `m` squarefree) together with a witnessing polynomial and a rational
isolating interval.

## Output

The timeline interleaves:

- `segment`: an open interval `(a,b)` on which the full ranking is constant,
  certified by a rational `sample` point inside it;
- `point`: an exact instant — every clustered set of simultaneous pair roots
  in `[0,T]`, plus the boundary instants `0` and `T` — with the exact ranking
  at that instant (arithmetic in `Q(sqrt(m))`).

Handled correctly: coincident trajectories (identically zero difference
polynomials produce no events; the ID tiebreak is permanent), tangencies
(a double root does not swap the order on either side, but the exact ranking
at that instant still reflects the ID tiebreak), and multiple pairs whose
roots coincide at the same instant (roots from different polynomials are
clustered by exact algebraic equality).

## Certificates

`Verify()` independently recomputes everything from the raw input and checks:

1. event times strictly increasing, inside `[0,T]`;
2. rational isolating intervals contain their root and are pairwise disjoint;
3. root coverage: every root of every pair polynomial in `[0,T]` is carried
   by exactly one event — no unhandled intersection;
4. each event's tie set is exactly the set of pairs whose difference
   polynomial vanishes there;
5. each open segment contains no pair root (all pairs re-checked), and its
   recorded ranking is reproduced at three independent rational samples;
6. for every pair not tied at an event, its order at the event matches both
   adjacent open segments;
7. `topK` is a prefix of the full ranking at every timeline entry.

The CLI exits non-zero if any certificate check fails.

## Usage

```sh
go build -o pairrank .
./pairrank examples/demo.json     # or: cat input.json | ./pairrank
go test ./...
```

Input: JSON with `query` (`x`,`y`), `T`, `K` (default: number of objects) and
`objects` (`id`, `x`, `y`, `vx`, `vy`). All numbers are rationals, as strings
(`"3/2"`, `"1.5"`, `"-2"`) or JSON numbers; missing fields default to 0.

## Files

- `alg.go` — exact algebraic numbers `u + v*sqrt(m)`: normalization, exact
  equality/order via rational interval refinement, squarefree decomposition.
- `poly.go` — rational quadratics: exact roots, evaluation at rational and
  algebraic points.
- `timeline.go` — pair enumeration, root clustering, event/segment rankings.
- `verify.go` — the independent certificate checks above.
- `main.go` — CLI and JSON I/O.
- `main_test.go` — tests: K=0, K>n, negative velocities, identical distance
  functions, coincident trajectories, tangency, simultaneous events,
  boundary roots, irrational event times, end-to-end CLI.
