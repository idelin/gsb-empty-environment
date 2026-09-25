# lazy-attr-policy

Cost-aware lazy attribute-fetch scheduler for policy decisions, in TypeScript,
verified with the Node.js built-in test runner (no external dependencies).

## Design

- **Policy language** (`src/policy.ts`): explicit attributes only; integer
  comparisons (`cmp`), membership in finite sets (`in`), boolean `and`/`or`/`not`.
  No external identity service — the caller passes a `Subject` explicitly.
- **Planner** (`src/planner.ts`): orders `and`/`or` children cheapest-subtree-first
  and splits attributes into `mustRead` (needed on every decision path, prefetched
  in one batch per source) and `optional` (fetched lazily, only if reached).
  The heuristic is greedy and **does not claim a globally minimal cost**
  (`plan.claimsOptimal === false`); `test/cost-baseline.test.ts` enumerates all
  fetch orders of a small policy to provide a baseline.
- **Scheduler** (`src/scheduler.ts`):
  - Batches pending attribute requests into one `fetchBatch` per
    (permission, subject, source).
  - Cache key = (permission, subject, attribute): same subject+permission merges
    reads; different permissions are isolated.
  - Concurrent evaluations coalesce in-flight batches; cancelling one evaluation
    only removes its own waiters. After completion/cancellation no further
    attributes are fetched.
  - Reentrancy: a fetch triggered from inside `fetchBatch` for the same
    (permission, subject, source) is serialized into a later batch — deterministic
    order, no deadlock (do not await the reentrant evaluation inside `fetchBatch`).
  - Timeouts reject the whole batch with `FetchTimeoutError`; partial batch
    failures reject only the failed attributes with `AttrFetchError` (failures
    are not cached).
- **Versions** (`src/evaluator.ts`): every fetch outcome carries a version. A
  single evaluation must observe one consistent version per source; a mixed
  version raises `VersionConflictError`, invalidates the source's cache entries
  for that (permission, subject), and retries (bounded by `maxVersionRetries`).
- **Journal** (`src/journal.ts`): decision + access evidence are committed
  atomically as one immutable record; cancelled/failed evaluations commit nothing.

## Run

Requires Node.js >= 23.6 (native type stripping) or Node 22.6+ with
`--experimental-strip-types`.

```sh
node --test test/*.test.ts
```

See `RESULTS.md` for the recorded run.
