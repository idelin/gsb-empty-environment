# state-history

Append-only TypeScript state-history library built on a **persistent (immutable) version tree**.

- **View the past without moving the head** — `checkout(versionId)` opens a read-only
  `ViewSession`; the real head (tip of the active branch) is untouched.
- **New events keep flowing** — while a view is open, `store.commit(event)` keeps
  appending to the real head of the active branch.
- **Editing the past requires an explicit branch** — a view cannot commit
  (`DetachedHeadError`); `view.fork(name)` creates a new branch at the viewed
  version and switches to it. Existing branches (including `main`) can never be
  overwritten: branch creation rejects duplicate names and commits are append-only.
- **Persistent version tree** — every mutation returns a new `VersionTree`;
  snapshots taken earlier stay valid forever, so tests and tooling can verify
  branch topology, switching, and cleanup against any point in time.
- **Scoped subscriptions with guaranteed cleanup** — `view.onChild(...)`
  subscriptions are released when the view closes (explicitly, via `fork()`, or
  when another `checkout` replaces it); `store.subscribeBranch(...)` returns an
  unsubscribe function. `store.subscriptionCount` exposes live listener count.

## Usage

```ts
import { HistoryStore } from './src/index.ts';

const store = new HistoryStore<number, number>((state, event) => state + event, 0);

store.commit(1);               // v2 on main
store.commit(2);               // v3 on main  <- real head

const view = store.checkout('v2');   // read-only view of the past
view.state;                          // 1
store.head.id;                       // 'v3' — head did not move

store.commit(3);               // v4 — new events keep landing on the head

view.commit(99);               // throws DetachedHeadError
view.fork('fix');              // explicit branch at v2, now active
store.commit(100);             // v5 on 'fix' (state 101); main tip still v4
```

## Verifying history with snapshots

```ts
const snapshot = store.versionTree;  // immutable forever
// ...commits, forks, switches later...
snapshot.size;                       // unchanged
snapshot.tipOf('main');              // unchanged
store.versionTree.isAncestor(a, b);  // topology queries on the live tree
```

## Requirements & tests

No runtime dependencies. Sources are plain `.ts` run via Node's type stripping
(Node ≥ 23.6 works out of the box; on 22.6+ use `node --experimental-strip-types`).

```sh
npm test        # node --test tests/*.test.ts
```

`tsconfig.json` is provided for editors/`tsc --noEmit` (`erasableSyntaxOnly`).
