# assignment-solver

Minimum-cost perfect one-to-one assignment solver in TypeScript.

- **Local cost updates**: `setCost` / `enableEdge` / `disableEdge` mutate single
  edges; the next `solve()` recomputes from scratch — stale potentials are
  never reused after an update.
- **Disabled edges** are forbidden: they can never appear in a matching and
  impose no dual constraint.
- **BigInt costs**: negative values and magnitudes beyond
  `Number.MAX_SAFE_INTEGER` are exact.
- **Infeasible instances** return a Hall deficiency certificate: a row set `S`
  with `|N(S)| < |S|` (maximum deficiency, via alternating BFS from all
  unmatched rows).
- **Primal + dual certificates**: optimal results carry the matching, its
  total cost, and potentials `u`, `v`; `verifyCertificate` independently
  recomputes everything from the problem data.

## Usage

```ts
import { AssignmentSolver, verifyCertificate } from './src/assignment';

const s = new AssignmentSolver(3);
s.setCost(0, 0, 4n);          // bigint, number also accepted
s.setCost(0, 1, -2n);         // negative costs are fine
s.setCost(1, 0, (1n << 62n)); // beyond safe integer
s.disableEdge(2, 0);          // forbidden edge

const result = s.solve();
if (result.status === 'optimal') {
  // result.matching[i] = column of row i; result.cost; result.dual.{u,v}
} else {
  // result.hallDefect.{rows, neighborhood, deficiency}
}
verifyCertificate(s, result); // independent recomputation -> true

s.setCost(0, 0, 100n);        // local update invalidates cached state
s.solve();                    // fresh potentials, fresh certificate
```

## Certificates

For an `optimal` result, `verifyCertificate` checks independently:

- primal: `matching` is a permutation, uses only allowed edges, and its
  recomputed cost equals `result.cost`;
- dual feasibility: `u[i] + v[j] <= cost[i][j]` for every allowed edge;
- complementary slackness: equality on every matched edge;
- strong duality: `sum(u) + sum(v) === cost`.

For an `infeasible` result it checks that `neighborhood` contains every column
adjacent to `rows` through an allowed edge and that
`rows.length - neighborhood.length === deficiency > 0` (Hall violation).

## Algorithm

- Feasibility: augmenting-path matching on the allowed-edge graph; on failure,
  alternating BFS from all unmatched rows yields the maximum-deficiency
  Hall set.
- Optimization: Hungarian (Kuhn–Munkres), `O(n^3)`, restricted to allowed
  edges, all arithmetic in `bigint`. Termination is guaranteed on feasible
  instances: the alternating tree's row set `R` always has an allowed edge to
  a column outside the tree, otherwise `N(R)` would violate Hall's condition.

## Tests

Requires [Bun](https://bun.sh) (runs TypeScript directly):

```
bun test
```

The suite cross-checks random instances (negative / huge bigint costs, random
disabled edges, random update sequences) against brute-force enumeration of
all permutations, and covers: unique feasible edge disabled, multiple optimal
ties, certificate tampering rejection, and cache invalidation on updates.
