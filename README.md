# path-patch-subscriptions

Transactional, path-based subscriptions to a patch stream. Zero dependencies,
plain TypeScript, tested with the Node.js built-in test runner
(`node --test`, Node >= 22.6 runs `.ts` directly via type stripping).

```sh
npm test    # node --test test/store.test.ts
npm run demo
```

## Model

- **Paths** are segment lists (`/a/b/0` -> `['a','b','0']`); array indices are
  numeric segments. Ancestry is **segment-wise**: `/ab` is not an ancestor of
  `/abc`.
- **Patterns** are paths plus `*` (exactly one segment) and `**` (zero or more
  segments). Every subscription has **subtree semantics**: a pattern matches a
  change when it matches a *prefix* of the change path, so subscribing to a
  parent path yields notifications for child changes.
- **Ops**: `set` / `delete` / `move`, grouped into transactions (`store.tx`).
  Each op expands into `Change` records (old/new path diffs):
  - `move` emits one `move` change per node in the moved subtree, each with
    `oldPath` and `path`; matching runs against both.
  - deleting array element `i` of an `n`-element array emits an **explicit
    notification scope**: the deleted element's subtree, `shift` changes
    `arr[j+1] -> arr[j]` for `j = i..n-2`, and a `delete` of tail `arr[n-1]`.
    Indices outside `i..n-1` are not notified.
  - deleting/moving a subtree enumerates its descendant paths, so subscribers
    at exact descendant paths are notified too.

## Semantics guaranteed by the implementation

- **Shared automaton** (`src/automaton.ts`): all patterns compile into one NFA
  (literal edges, `*` edges, `**` = epsilon edge into a self-looping state).
  Common prefixes share states; `match()` reports the number of state
  activations actually traversed (`Store.stats`, `Automaton.stateCount`).
- **Reference verification** (`src/reference.ts`): an independent recursive
  per-subscription matcher. `Store.verifyLastCommit()` recomputes the expected
  notifications and diffs them against the automaton-driven pipeline; the test
  suite also runs a randomized differential test.
- **De-duplication with op identity and order**: a subscription matched by
  several changes of the same op (e.g. both ends of a move) gets **one**
  notification; `notification.ops` lists each op once, in commit order.
- **Transaction boundaries**:
  - Writes apply to a copy-on-write working copy; commit swaps the root
    atomically, so readers (`store.get`, `store.snapshot`) never observe
    intermediate states, and old snapshots stay valid.
  - `subscribe`/`unsubscribe` called inside a tx callback or a notification
    callback are staged and take effect at the transaction boundary (after the
    current commit's notifications are delivered).
  - Reentrant writes (a `tx` opened inside a notification callback) are queued
    and committed after the current delivery round, in FIFO order.
- **Replay**: every committed op is appended to `store.opLog`;
  `Store.replay(log, initial)` rebuilds an identical state from a base state.

## Layout

- `src/path.ts` — path parsing, segment-wise prefix test
- `src/pattern.ts` — pattern parsing
- `src/automaton.ts` — shared matching automaton + traversal metrics
- `src/reference.ts` — independent per-subscription matcher + verifier
- `src/store.ts` — transactional store, change generation, notifications
- `test/store.test.ts` — `node:test` suite (semantics, differential, metrics, replay)
- `demo.ts` — runnable end-to-end sample
