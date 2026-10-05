# task-priority-queue

A min-priority queue for tasks with stable, revocable handles, FIFO
tie-breaking, and atomic batch priority updates. TypeScript, zero dependencies.

## Semantics

- **Handles**: `enqueue` returns a `Handle { slot, generation }`. Slots are
  recycled after `cancel`/`extractMin`; every recycle bumps the slot's
  generation, so handles issued before the recycle are stale and rejected
  (`cancel` returns `false`, `changePriority`/`updateMany` throw
  `StaleHandleError`).
- **Ordering**: `(priority, seq)` lexicographic. Equal priorities dequeue in
  insertion order. The sequence counter is a `bigint`, so ordering stays
  correct beyond `Number.MAX_SAFE_INTEGER` (pass `initialSeq` to resume from
  a persisted counter).
- **Atomic batches**: `updateMany` applies updates in order; on any failure
  (stale handle or non-finite priority) it restores the full prior state —
  heap array, entry priorities, *and* the slot → heap-index mapping — then
  rethrows.

## API

```ts
const queue = new TaskPriorityQueue<Task>({ initialSeq: 0n });
const handle = queue.enqueue(task, 5);        // -> Handle
queue.changePriority(handle, 1);              // throws StaleHandleError if stale
queue.updateMany([{ handle, priority: 2 }]);  // atomic, rolls back on failure
queue.cancel(handle);                         // -> boolean (false if stale)
queue.peekMin();                              // -> { value, priority } | undefined
queue.extractMin();                           // -> { value, priority } | undefined
queue.size; queue.isEmpty;
```

Priorities must be finite numbers (`NaN`/`Infinity` throw `TypeError`).

## Development

```sh
npm test        # node --test (requires Node >= 22.6 for type stripping)
npm run typecheck   # requires: npm i -D typescript @types/node
```

The randomized test in `test/priority-queue.test.ts` cross-checks thousands of
mixed operations against an independent sorted-list oracle, including sequence
numbers beyond `Number.MAX_SAFE_INTEGER`, duplicate/stale cancels, and failing
batches that must roll back completely.
