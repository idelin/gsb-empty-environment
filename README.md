# task-scope

Structured concurrency for asynchronous tasks (TypeScript, no dependencies).

A `TaskScope` lets a parent task spawn child tasks. When the parent — or any
descendant — fails, cancellation is requested for all descendants, and the
scope waits until every task has fully finished, including cleanup hooks and
tasks spawned while the scope is already closing.

## Semantics

- **Cancellation propagation**: the first non-cancellation failure aborts the
  scope's `AbortSignal`; every descendant observes it via `ctx.signal`.
- **Cleanup is awaited**: `ctx.onCleanup(fn)` hooks run (LIFO) after the task
  body settles, even on failure or cancellation. `close()` only resolves once
  all bodies and cleanups have finished.
- **Spawning while closing**: tasks may still be spawned while the scope is
  closing (e.g. from a cleanup hook). They start with an already-aborted
  signal and are awaited like any other task. Spawning after the scope has
  fully closed throws.
- **Stable error aggregation**: failures reject with `TaskScopeError`.
  - `errors`: every non-cancellation error (body and cleanup), sorted by task
    creation sequence number (`seq`; the scope body is `0`).
  - `rootCause`: the chronologically first non-cancellation error.
  - Cancellation errors (`CancellationError` / `AbortError`) are never
    recorded and never override the root cause.

## Usage

```ts
import { runTaskScope, TaskScopeError } from './src/task-scope.ts';

await runTaskScope(async (scope) => {
  scope.spawn(async (ctx) => {
    ctx.onCleanup(async () => {
      await releaseResources(); // always awaited, even during shutdown
    });
    while (!ctx.signal.aborted) {
      await doWork();
    }
  });
  // Returning or throwing here closes the scope: descendants are cancelled
  // and all cleanup is awaited before runTaskScope settles.
});
```

## Tests

Requires Node.js >= 22.6 (type stripping):

```sh
npm test
```
