# assignment-solver

Minimum-cost perfect one-to-one bipartite assignment (TypeScript), with:

- **Local cost updates** — `setCost` / `enableEdge` / `disableEdge` mutate single edges.
- **Forbidden edges** — disabled edges can never be selected.
- **BigInt costs** — negative values and magnitudes beyond `Number.MAX_SAFE_INTEGER`.
- **Primal + dual certificates** — every optimum comes with potentials so the
  result can be re-verified independently (`src/verify.ts`).
- **Hall deficiency certificate** — when no perfect assignment exists, returns
  a row set `S` with neighbourhood `N(S)` and `|S| > |N(S)|`.
- **No stale potentials** — any mutation invalidates the cached result; duals
  are always recomputed from the current costs, never reused after an update.

## Usage

```ts
import { AssignmentSolver } from './src/assignment';
import { verifyOptimal, verifyInfeasible } from './src/verify';

const solver = new AssignmentSolver(3, [
  [4, 1, null],   // null = forbidden edge
  [2, 0, 5],
  [3n << 60n, 2, -7], // bigint costs allowed (number must be safe integer)
]);

const result = solver.solve();
if (result.status === 'optimal') {
  // result.assignment[i] = column of row i; result.cost: bigint
  verifyOptimal(solver, result); // true — independent proof of optimality
} else {
  verifyInfeasible(solver, result); // true — Hall certificate, |rows| > |cols|
}

solver.setCost(0, 1, 100);  // local update; cached result + duals dropped
solver.disableEdge(1, 0);   // forbid an edge
solver.enableEdge(0, 2, 9); // re-enable with a cost
solver.solve();             // recomputed from scratch
```

## Certificates

**Optimality** (`verifyOptimal`) checks, from the problem alone:
1. the assignment is a permutation using only enabled edges and its cost
   matches the reported cost;
2. dual feasibility `u[i] + v[j] <= c(i,j)` on every enabled edge;
3. strong duality `sum(u) + sum(v) === cost`.

**Infeasibility** (`verifyInfeasible`) checks `|S| > |N(S)|` and that every
enabled edge leaving `S` ends in `N(S)` — by Hall's theorem no perfect
matching exists.

## Algorithm

Feasibility via augmenting-path max matching (Hall set from alternating BFS),
then the O(n^3) Hungarian algorithm over BigInt costs; forbidden edges carry a
big-M cost that provably exceeds any all-enabled total, so they are never
chosen once feasibility is established.

## Tests

```
bun test        # 22 tests: brute-force permutation cross-check (n<=8),
                # forbidden unique edge, tied optima, update stress,
                # BigInt exactness, certificate tampering rejection
tsc --noEmit    # type-check (when TypeScript is installed)
```
