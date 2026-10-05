# Region-Aware Offline Request Allocator

Offline batch assignment of indivisible requests to capacitated nodes.
The whole batch is recomputed on every run, so capacity shrinks and node
exits are just new inputs; the previous plan only feeds the migration
objective. Requires Node.js >= 22.18 (runs TypeScript directly, no build
step and no dependencies).

## Objectives (lexicographic)

1. Maximize total admitted demand (rejections minimized by demand).
2. Minimize total cross-region cost (`costMatrix[requestRegion][nodeRegion]`).
3. Minimize migrated requests vs the previous plan (a migration = admitted
   before and now, on a different node; previous nodes may be offline).
4. Tie-break: smallest node-id vector with requests ordered by request id.
   Rejected requests use a sentinel that sorts after every node id, so ties
   prefer keeping smaller-id requests admitted.

Requests are indivisible and no node is ever over capacity.

## Search budget and optimality

`solve(problem, { budget })` caps the exact branch-and-bound search at
`budget` search nodes (default 1,000,000). If the budget is exhausted, the
best feasible plan found so far is returned with `optimal: false` and
`stats.budgetExhausted: true` — optimality is never claimed without proof.
The all-rejected plan seeds the incumbent, so an interrupted run still
returns a feasible plan. Instances with <= 10 requests can be cross-checked
against `solveExhaustive`, which enumerates every feasible assignment.

## Usage

```sh
npm test                          # node --test (unit + exhaustive cross-check + CLI)
node src/cli.ts demo              # print a synthetic input instance
node src/cli.ts demo | node src/cli.ts solve -
node src/cli.ts solve input.json --budget 200000 --exhaustive
```

Input JSON: `requests` (`id`, `region`, integer `demand`), `nodes` (`id`,
`region`, integer `capacity`), `costMatrix[requestRegion][nodeRegion]`,
optional `previous` (`requestId -> nodeId`), optional `budget`.

Output JSON: `assignment` (requestId -> nodeId or null), `rejected`,
`ledger` (per-node capacity/used/remaining/requests), `migrations`
(`{requestId, from, to}`), `stats`, `optimal`. With `--exhaustive`, a
`verification` section compares against brute force (exit code 2 on
disagreement).

## Layout

- `src/types.ts` — problem/result types
- `src/allocator.ts` — validation, lexicographic comparator, bounded branch and bound
- `src/exhaustive.ts` — brute-force verifier for small instances
- `src/synth.ts` — deterministic local synthetic data generator
- `src/cli.ts` — command line (`solve`, `demo`, `help`)
- `test/allocator.test.ts` — node:test suite
