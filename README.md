# request-coalescer

Deduplicates concurrent loads by key while giving every caller its own
deadline and cancellation scope.

## Semantics

- **Load once per key**: the first `request(key)` starts a batch and invokes
  the loader once; concurrent requests for the same key join the in-flight
  batch and share its result.
- **Per-caller deadlines**: each caller passes its own `deadlineMs` (and/or
  `AbortSignal`). A caller that times out or cancels is rejected individually
  (`TimeoutError` / `CoalescerAbortError`) — the shared load keeps running
  for the remaining callers.
- **Abort when everyone leaves**: when the last participant of a batch
  leaves, the batch is torn down and the loader's `AbortSignal` is aborted.
- **No stale overwrites**: a torn-down or settled batch is detached from the
  registry before any user code runs, so a late loader result can never
  resolve a newer batch for the same key.
- **Re-entrancy safe**: abort listeners (and loader code) may call `request`
  again for the same key; this starts a fresh batch.
- **Sync-throw safe**: a loader that throws synchronously is converted into
  an asynchronous rejection for all participants and frees the key for retry.

## Usage

```ts
import { RequestCoalescer } from "request-coalescer";

const coalescer = new RequestCoalescer<string, User>(async (id, signal) => {
  const res = await fetch(`/users/${id}`, { signal });
  return res.json();
});

const user = await coalescer.request("42", { deadlineMs: 250 });
```

## Testing

Tests run on Node >= 23.6 (native TypeScript type stripping) with a manual
scheduler, so deadlines advance deterministically:

```sh
node --test
```

`ManualScheduler` (in `src/scheduler.ts`) can also be injected in production
code paths that need a custom clock.
