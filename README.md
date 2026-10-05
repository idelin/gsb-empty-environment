# keyed-executor

Keyed job executor with deterministic scheduling.

## Semantics

- **Per-key serialization** — jobs submitted with the same key run strictly one
  at a time, in FIFO order.
- **Shared concurrency budget** — jobs with different keys run concurrently,
  bounded by `concurrency` (default 4). Ready keys are scheduled FIFO.
- **Failure isolation** — a failed job rejects only its own handle; the key's
  queued successors continue.
- **Cancellation** — `handle.cancel()` removes a queued job and rejects its
  promise with `JobCancelledError`. Running jobs cannot be cancelled.
- **Idle-key release** — a key's scheduling state is dropped synchronously as
  soon as the key has no running job and an empty queue.
- **Graceful close** — `close()` cancels all queued jobs, rejects further
  submissions (including recursive submissions from still-running jobs) with
  `ExecutorClosedError`, and resolves when all running jobs settle. Idempotent.

## Usage

```ts
import { KeyedExecutor } from './src/keyed-executor';

const executor = new KeyedExecutor({ concurrency: 8 });

const handle = executor.submit('user:42', async () => fetchProfile(42));
handle.cancel();            // only while queued
const profile = await handle.promise;

await executor.close();     // drain running jobs, cancel queued ones
```

Re-entrant submissions are safe: a job may synchronously submit follow-up work
for its own key, and settlement callbacks may submit new jobs for a key whose
state was just released — both are queued and scheduled normally.

## Development

```sh
npm install
npm test        # builds with tsc, runs node:test suites (Node >= 18)
```

Tests are deterministic: they drive the executor with manually resolved
deferreds and a microtask flush, never with timers.
