# Indexed Job Scheduling & Failure-Policy Engine

An importable, dependency-free TypeScript state machine that reproduces the
indexed-Job semantics of the Kubernetes `batch/v1` Job controller:
`completionMode: Indexed`, `backoffLimitPerIndex`, `maxFailedIndexes`,
`podFailurePolicy`, per-index exponential backoff and canonical index-range
compression.

The engine is purely in-memory and timer-free. A control loop feeds observed
pod outcomes back into it and pulls newly scheduled pods out of it; every
time decision goes through an injectable clock, so whole Job lifecycles are
deterministic and testable.

## Layout

| Module | Responsibility |
| --- | --- |
| `src/types.ts` | Public types mirroring the relevant `batch/v1` API fields. |
| `src/failure-policy.ts` | Ordered, first-match evaluation of `podFailurePolicy.rules`. |
| `src/backoff.ts` | Deterministic per-index exponential backoff. |
| `src/ranges.ts` | Canonical index-range compression / expansion. |
| `src/engine.ts` | The `IndexedJobEngine` multi-layer state machine. |
| `src/index.ts` | Package barrel (the only entry point consumers need). |

## Build and test

```sh
npm run build      # tsc -> dist/ (JS + .d.ts + source maps)
npm run typecheck  # tsc --noEmit
npm test           # builds, then runs the node:test suite against dist/
```

> This environment ships a Node binary without built-in TypeScript type
> stripping, so tests run against the compiled output rather than raw `.ts`
> files. `node_modules/@types/node` and `node_modules/undici-types` are
> vendored locally solely for compilation; the runtime engine itself has zero
> dependencies.

## Quick start

```ts
import { IndexedJobEngine } from './dist/index.js';

const engine = new IndexedJobEngine({
  spec: {
    completions: 4,
    parallelism: 2,
    backoffLimitPerIndex: 2,
    maxFailedIndexes: 1,
    backoffLimit: 10,
    podFailurePolicy: {
      rules: [
        // Evictions are ignored entirely: no counter, no backoff, instant retry.
        { action: 'Ignore', onPodConditions: [{ type: 'DisruptionTarget' }] },
        // Exit code 42 permanently kills just this index.
        { action: 'FailIndex',
          onExitCodes: { containerName: 'app', operator: 'In', values: [42] } },
      ],
    },
  },
  now: () => clock, // inject a clock; defaults to Date.now
});

// 1. Ask for pods up to `parallelism`, smallest eligible index first.
const pods = engine.schedule(); // [{ index: 0, ... }, { index: 1, ... }]

// 2. Report an observed pod failure; the engine applies the first matching rule.
const outcome = engine.reportPodFailure({
  index: 0,
  podName: pods[0].podName,
  containers: [{ name: 'app', exitCode: 137 }],
  conditions: [{ type: 'DisruptionTarget', status: 'True' }],
});
// outcome.action === 'Ignore', outcome.counted === false

// 3. Report successes, then read canonical status.
engine.reportPodSuccess({ index: 0, podName: pods[0].podName });
engine.getCompletedIndexRanges(); // "0"
engine.getJobCondition();         // "Running" | "Complete" | "Failed"
```

## Semantics

### 1. `podFailurePolicy` — first match wins

`evaluateFailurePolicy` walks `rules` in declaration order and returns the
first rule whose **present** requirements all match (AND within a rule); a
rule with neither `onExitCodes` nor `onPodConditions` is a catch-all. With no
policy or no match, the default action is `Count`.

- `onExitCodes` inspects terminated, non-zero exit codes. `containerName`
  scopes the match to one business container (omitted = all containers).
  `In` matches when any inspected code is in `values`; `NotIn` matches only
  when every failed code is outside `values`.
- `onPodConditions` matches pod conditions such as the `DisruptionTarget`
  eviction marker. `In` (default) requires the condition with the expected
  status (status defaults to `True`); `NotIn` matches when it is absent or has
  another status.

Actions:

- **`Ignore`** — no per-index failure, no global failure, no backoff; the
  index is immediately re-schedulable.
- **`FailIndex`** — the index is permanently failed at once, regardless of
  how many retries it had left; other indexes keep running.
- **`FailJob`** — the whole Job becomes `Failed` immediately and every
  still-running pod is returned in `outcome.terminatedPods` for teardown.
- **`Count`** (or no match) — increment both the per-index and global
  failure counters and arm the index's exponential backoff.

### 2. Per-index failure budgets and global termination

Each index keeps its own `failures` counter and `backoffUntil` deadline.
When `failures` is strictly greater than `backoffLimitPerIndex`, only that
index becomes permanently failed. The Job terminates as `Failed` as soon as
either:

- the number of permanently failed indexes is strictly greater than
  `maxFailedIndexes` (`MaxFailedIndexesExceeded`), or
- the total number of counted failures is strictly greater than the global
  `backoffLimit` (`BackoffLimitExceeded`).

A `FailJob` rule produces `FailJobPolicy`. When every index is terminal
without breaching a threshold, the Job is `Complete` — tolerating up to
`maxFailedIndexes` permanently failed indexes.

### 3. Scheduling and backoff backfill

`schedule(now?)` launches pods up to `parallelism`, choosing indexes in
ascending order that are: not succeeded, not permanently failed, without an
active pod, and whose `backoffUntil <= now`. Freeing a slot (success,
permanent failure or ignored failure) lets the smallest pending index
backfill immediately.

Backoff is deterministic: delay `n` is
`min(initialIntervalMs * multiplier^(n-1), maxIntervalMs)` where `n` is the
one-based counted-failure number of that index (defaults: 10s base, ×2, 6m
cap). There is no jitter; override the knobs via the `backoff` option.

### 4. Canonical range strings

`compressIndexRanges` sorts and de-duplicates indexes, then renders runs of
one as `"5"` and runs of two or more as `"start-end"`, joined by commas:

```
[0,1,2,3,5,8,9,10] -> "0-3,5,8-10"
[3,1,2]            -> "1-3"
[]                 -> ""
```

`IndexedJobEngine` exposes this for both succeeded indexes
(`getCompletedIndexRanges`, equivalent to `status.completedIndexes`) and
permanently failed indexes (`getFailedIndexRanges`).

## Public surface

- `new IndexedJobEngine(options)` / `validateSpec(spec)`
- `schedule(now?)`, `reportPodFailure(info)`, `reportPodSuccess(info)`
- `getJobCondition()`, `getFailureReason()`, `getActivePodCount()`
- `getIndexState(i)`, `getIndexStates()`
- `getTotalCountedFailures()`, `getFailedIndexCount()`, `getSucceededIndexCount()`
- `getCompletedIndexes()`, `getFailedIndexes()`,
  `getCompletedIndexRanges()`, `getFailedIndexRanges()`
- Standalone helpers: `evaluateFailurePolicy`, `matchesExitCodes`,
  `matchesPodCondition`, `ruleMatches`, `compressIndexRanges`,
  `expandIndexRanges`, `backoffDelayMs`, `backoffDeadline`, `resolveBackoff`.
