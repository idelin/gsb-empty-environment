# lazy-attr-scheduler

Cost-aware lazy attribute fetch scheduler for policy decisions (TypeScript, no dependencies).

## Design

- **Policy** (`src/policy.ts`): only explicit attributes, integer comparisons,
  finite-set membership, and `and`/`or`/`not`. No external identity service.
- **Planner** (`src/planner.ts`): evaluates `and`/`or` children cheapest-first
  (a heuristic, **not** claimed globally minimal) and marks the first child of
  each node as *required* (must-read) and the rest as *optional*
  (skipped when short-circuiting already decides the outcome).
- **Scheduler** (`src/scheduler.ts`):
  - batches attribute requests made in the same tick per (subject, scope);
  - cache merged per subject+scope, isolated across permission scopes;
  - concurrent evaluations share in-flight fetches; cancelling one consumer
    does not affect others (the fetch aborts only when its last consumer leaves);
  - every fetch carries a version; mixed-version reads retry the stale
    attributes, then fail deterministically (`VersionConflictError`);
  - deterministic fetcher behaviour: reentrant in-flight request
    (`ReentrantFetchError`), timeout (`FetchTimeoutError`), partial batch
    failure (retry failed attributes only, then `AttributeUnavailableError`);
  - after completion or cancellation, no further attributes are fetched.
- **Evidence** (`src/evidence.ts`): access evidence and the decision are
  committed atomically as a single record (temp file + rename for the file store).

## Requirements & run

Node.js >= 22.6 (uses `node:test` and `--experimental-strip-types`).

```sh
npm test
# or explicitly:
node --experimental-strip-types --test test/*.test.ts
```

See `RESULTS.md` for the recorded output of an actual run.
