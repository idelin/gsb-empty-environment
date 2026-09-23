import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Store, Transaction } from '../src/store.ts';

// ---------- 1. merging: many updates in one cycle commit once ----------

test('multiple updates in one cycle merge into a single commit and notification', () => {
  const store = new Store({ a: 0, b: 0, c: 0 });
  const seen: unknown[] = [];
  store.subscribe((s) => seen.push({ ...s }));

  store.batch((tx) => {
    tx.set('a', 1);
    tx.update('b', (b) => b + 10);
    tx.set('c', 3);
  });

  assert.deepEqual(store.getState(), { a: 1, b: 10, c: 3 });
  assert.equal(store.commits, 1);
  assert.equal(seen.length, 1, 'exactly one notification per scheduling cycle');
  assert.deepEqual(seen[0], { a: 1, b: 10, c: 3 });
});

// ---------- 2. ordering: functional reads previous, mixed order kept ----------

test('functional updates read the previous queued value; direct/functional order preserved', () => {
  const store = new Store({ x: 0 });
  store.batch((tx) => {
    tx.update('x', (x) => x + 1); //   0 -> 1
    tx.set('x', 10); //                1 -> 10 (direct assignment wins at its position)
    tx.update('x', (x) => x + 1); //  10 -> 11
    tx.set('x', 5); //                11 -> 5
    tx.update('x', (x) => x * 100); //  5 -> 500
  });
  assert.equal(store.getState().x, 500);
});

test('two consecutive x => x + 1 add 2, not 1', () => {
  const store = new Store({ x: 0 });
  store.batch((tx) => {
    tx.update('x', (x) => x + 1);
    tx.update('x', (x) => x + 1);
  });
  assert.equal(store.getState().x, 2);
});

// ---------- 3. conditional updates, cross-field invariants, read-then-write ----------

test('conditional updates and read-then-write evaluate in order on the draft, committed once', () => {
  const store = new Store({ balance: 100, escrow: 0, total: 100, moves: 0 });
  const notifications: unknown[] = [];
  store.subscribe((s) => notifications.push({ ...s }));

  store.batch((tx) => {
    // read-then-write: move 40 from balance to escrow, reading sibling fields
    tx.update('escrow', (e, d) => e + Math.min(40, d.balance));
    tx.update('balance', (b) => b - 40);
    // conditional update evaluated against the draft AFTER the two ops above
    tx.updateIf('moves', (d) => d.escrow > 0, (m) => m + 1);
    tx.setIf('balance', (d) => d.balance < 0, 0); // guard, must not fire here
    // cross-field invariants on the draft
    tx.assert((d) => d.balance + d.escrow === d.total, 'funds must be conserved');
    tx.assert((d) => d.balance >= 0, 'no overdraft');
  });

  assert.deepEqual(store.getState(), { balance: 60, escrow: 40, total: 100, moves: 1 });
  assert.equal(notifications.length, 1, 'evaluated on draft, then committed once');
});

test('conditionals see earlier ops of the same batch, not the committed state', () => {
  const store = new Store({ n: 1, flag: false });
  store.batch((tx) => {
    tx.set('n', 10);
    tx.setIf('flag', (d) => d.n >= 10, true); // sees draft n=10, not committed n=1
  });
  assert.deepEqual(store.getState(), { n: 10, flag: true });
});

// ---------- 4. cancellation & idempotent retry ----------

test('cancelling one operation does not undo later independent updates', () => {
  const store = new Store({ a: 0, b: 0, c: 0 });
  store.batch((tx) => {
    tx.set('a', 1);
    const doomed = tx.set('b', 2);
    tx.set('c', 3);
    doomed.cancel();
  });
  assert.deepEqual(store.getState(), { a: 1, b: 0, c: 3 }, 'b skipped, c still applied');
});

test('operation IDs make retries idempotent', () => {
  const store = new Store({ count: 0, other: 0 });

  const op = (tx: Transaction<{ count: number; other: number }>) => {
    tx.update('count', (c) => c + 1, { opId: 'inc-1' });
  };
  store.batch(op);
  store.batch(op); // retry: same opId
  store.batch(op);
  assert.equal(store.getState().count, 1, 'retried opId applied exactly once');

  // a retry batch may still carry NEW operations; only the duplicate is skipped
  store.batch((tx) => {
    tx.update('count', (c) => c + 1, { opId: 'inc-1' }); // duplicate, skipped
    tx.update('other', (o) => o + 5, { opId: 'other-1' }); // new, applied
  });
  assert.deepEqual(store.getState(), { count: 1, other: 5 });
  assert.ok(store.hasApplied('inc-1'));
  assert.ok(store.hasApplied('other-1'));
});

// ---------- 5. finite-state reference: merged vs unmerged execution ----------

// Enum-like finite state (const object + union type; no reactive framework involved).
const Phase = { Idle: 'idle', Loading: 'loading', Ready: 'ready', Failed: 'failed' } as const;
type Phase = (typeof Phase)[keyof typeof Phase];
type Machine = { phase: Phase; attempts: number; score: number };

// The SAME script, expressed as data, interleaving enum assignment and
// functional updates. `effects` counts updater invocations (side effects).
type ScriptOp =
  | { kind: 'setPhase'; value: Phase }
  | { kind: 'bumpAttempts' }
  | { kind: 'addScore'; amount: number }
  | { kind: 'scaleScore'; factor: number }
  | { kind: 'bumpAttemptsIfReady' }
  | { kind: 'failIfNegativeScore' }
  | { kind: 'assertAttempts' };

function buildScript(effects: { updaterCalls: number }): ScriptOp[] {
  void effects;
  return [
    { kind: 'setPhase', value: Phase.Loading }, // enum assignment
    { kind: 'bumpAttempts' }, //                 functional update
    { kind: 'addScore', amount: 10 }, //         functional update
    { kind: 'setPhase', value: Phase.Ready }, // enum assignment interleaved
    { kind: 'scaleScore', factor: 2 }, //        functional update
    { kind: 'bumpAttemptsIfReady' }, //          conditional functional update
    { kind: 'failIfNegativeScore' }, //          conditional enum assignment (must not fire)
    { kind: 'assertAttempts' }, //               cross-field invariant
  ];
}

function applyScriptOp(
  tx: Transaction<Machine>,
  op: ScriptOp,
  effects: { updaterCalls: number },
): void {
  const counted =
    <T,>(fn: (prev: T) => T) =>
    (prev: T): T => {
      effects.updaterCalls += 1;
      return fn(prev);
    };
  switch (op.kind) {
    case 'setPhase':
      tx.set('phase', op.value);
      break;
    case 'bumpAttempts':
      tx.update('attempts', counted((a) => a + 1));
      break;
    case 'addScore':
      tx.update('score', counted((s) => s + op.amount));
      break;
    case 'scaleScore':
      tx.update('score', counted((s) => s * op.factor));
      break;
    case 'bumpAttemptsIfReady':
      tx.updateIf('attempts', (d) => d.phase === Phase.Ready, counted((a) => a + 1));
      break;
    case 'failIfNegativeScore':
      tx.setIf('phase', (d) => d.score < 0, Phase.Failed);
      break;
    case 'assertAttempts':
      tx.assert((d) => d.attempts >= 1 && d.phase !== Phase.Loading, 'invalid machine state');
      break;
  }
}

function runMerged(initial: Machine, ops: ScriptOp[], effects: { updaterCalls: number }) {
  const store = new Store(initial);
  let notifications = 0;
  store.subscribe(() => (notifications += 1));
  store.batch((tx) => {
    for (const op of ops) applyScriptOp(tx, op, effects);
  });
  return { state: store.getState(), notifications };
}

function runUnmerged(initial: Machine, ops: ScriptOp[], effects: { updaterCalls: number }) {
  const store = new Store(initial);
  let notifications = 0;
  store.subscribe(() => (notifications += 1));
  for (const op of ops) store.batch((tx) => applyScriptOp(tx, op, effects)); // one op per cycle
  return { state: store.getState(), notifications };
}

test('merged optimization and unmerged execution agree on result and side-effect count', () => {
  const initial: Machine = { phase: Phase.Idle, attempts: 0, score: 1 };
  const ops = buildScript({ updaterCalls: 0 });

  const mergedEffects = { updaterCalls: 0 };
  const unmergedEffects = { updaterCalls: 0 };
  const merged = runMerged(initial, ops, mergedEffects);
  const unmerged = runUnmerged(initial, ops, unmergedEffects);

  assert.deepEqual(merged.state, unmerged.state, 'identical resulting state');
  assert.deepEqual(merged.state, { phase: Phase.Ready, attempts: 2, score: 22 });
  assert.equal(
    mergedEffects.updaterCalls,
    unmergedEffects.updaterCalls,
    'identical side-effect (updater invocation) count',
  );
  assert.equal(mergedEffects.updaterCalls, 4);
  assert.equal(merged.notifications, 1, 'merged: a single notification');
  assert.ok(unmerged.notifications > 1, 'unmerged: one notification per cycle');
});

test('merged and unmerged raise the same error with the same side-effect count', () => {
  const initial: Machine = { phase: Phase.Idle, attempts: 0, score: 1 };
  const ops: ScriptOp[] = [...buildScript({ updaterCalls: 0 }), { kind: 'assertAttempts' }];
  // make the final invariant fail by forcing a Loading phase at the end
  const failing: ScriptOp[] = [...ops, { kind: 'setPhase', value: Phase.Loading }, { kind: 'assertAttempts' }];

  const mergedEffects = { updaterCalls: 0 };
  const unmergedEffects = { updaterCalls: 0 };

  const mergedStore = new Store(initial);
  let mergedErr: Error | null = null;
  try {
    mergedStore.batch((tx) => {
      for (const op of failing) applyScriptOp(tx, op, mergedEffects);
    });
  } catch (err) {
    mergedErr = err as Error;
  }
  assert.ok(mergedErr, 'merged execution raises');

  const unmergedStore = new Store(initial);
  let unmergedErr: Error | null = null;
  for (const op of failing) {
    try {
      unmergedStore.batch((tx) => applyScriptOp(tx, op, unmergedEffects));
    } catch (err) {
      unmergedErr = err as Error;
    }
  }

  assert.ok(unmergedErr, 'unmerged execution raises too');
  assert.equal((mergedErr as Error).message, (unmergedErr as Error).message, 'same error');
  assert.match((mergedErr as Error).message, /invalid machine state/);
  assert.equal(mergedEffects.updaterCalls, unmergedEffects.updaterCalls, 'same side-effect count');
  // atomicity: the failed merged batch leaves no partial values at all
  assert.deepEqual(mergedStore.getState(), initial);
});

// ---------- 6. replay & reader isolation ----------

test('readers never observe transaction intermediate state', () => {
  const store = new Store({ x: 0 });
  const observedDuringBatch: number[] = [];
  const observedBySubscriber: number[] = [];
  store.subscribe((s) => observedBySubscriber.push(s.x));

  store.batch((tx) => {
    tx.set('x', 1);
    observedDuringBatch.push(store.getState().x); // external reader mid-batch
    observedDuringBatch.push(tx.read().x); //       reader inside the batch
    tx.set('x', 2);
    observedDuringBatch.push(store.getState().x);
  });

  assert.deepEqual(observedDuringBatch, [0, 0, 0], 'draft is invisible until commit');
  assert.deepEqual(observedBySubscriber, [2], 'subscriber sees only the final committed value');
});

test('operation log replays to the exact same state', () => {
  const initial = { x: 0, tag: 'a' };
  const store = new Store(initial);
  store.batch((tx) => {
    tx.update('x', (x) => x + 1, { opId: 'op-1' });
    tx.set('tag', 'b');
  });
  store.batch((tx) => {
    tx.update('x', (x) => x * 10, { opId: 'op-2' });
    tx.update('x', (x) => x + 5);
  });

  const replayed = Store.replay(initial, store.log);
  assert.deepEqual(replayed, store.getState());
  assert.deepEqual(replayed, { x: 15, tag: 'b' });
});

// ---------- 7. nested batches & failure atomicity ----------

test('nested batches: only the outermost batch commits and notifies', () => {
  const store = new Store({ n: 0 });
  let notifications = 0;
  store.subscribe(() => (notifications += 1));

  store.batch((tx) => {
    tx.update('n', (n) => n + 1);
    store.batch((inner) => {
      inner.update('n', (n) => n + 1);
      store.batch((innermost) => innermost.update('n', (n) => n + 1));
    });
    assert.equal(store.getState().n, 0, 'still uncommitted inside the outer batch');
  });

  assert.equal(store.getState().n, 3);
  assert.equal(store.commits, 1);
  assert.equal(notifications, 1, 'only the outermost batch notifies');
});

test('failed batch leaves no partial values and does not notify', () => {
  const store = new Store({ a: 1, b: 1 });
  let notifications = 0;
  store.subscribe(() => (notifications += 1));

  assert.throws(
    () =>
      store.batch((tx) => {
        tx.set('a', 100);
        tx.set('b', 200);
        tx.assert((d) => d.a + d.b < 10, 'sum too large');
      }),
    /sum too large/,
  );

  assert.deepEqual(store.getState(), { a: 1, b: 1 }, 'rolled back completely');
  assert.equal(notifications, 0);
  assert.equal(store.commits, 0);
  assert.equal(store.log.length, 0, 'nothing logged from a failed batch');
});

test('a throwing updater aborts the batch; the store stays usable afterwards', () => {
  const store = new Store({ x: 1 });
  assert.throws(() =>
    store.batch((tx) => {
      tx.set('x', 42);
      tx.update('x', () => {
        throw new Error('boom');
      });
    }),
  );
  assert.equal(store.getState().x, 1);

  store.batch((tx) => tx.set('x', 7)); // store still functional
  assert.equal(store.getState().x, 7);
});
