import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LockManager, LockAbortedError } from '../src/lock-manager.ts';

/** Flush the microtask queue so resolved/rejected promises settle. */
async function tick(times = 5): Promise<void> {
  for (let i = 0; i < times; i++) await Promise.resolve();
}

function makeManager(): {
  lm: LockManager;
  victims: number[];
} {
  const victims: number[] = [];
  const lm = new LockManager({ recordEvents: true });
  lm.setVictimHandler((txnId) => {
    victims.push(txnId);
    lm.releaseAll(txnId);
  });
  return { lm, victims };
}

test('shared locks are compatible; exclusive lock blocks until release', async () => {
  const { lm } = makeManager();
  lm.registerTxn(1);
  lm.registerTxn(2);
  lm.registerTxn(3);

  await lm.acquire(1, 'k', 'S');
  await lm.acquire(2, 'k', 'S'); // compatible: granted immediately

  let xGranted = false;
  const pendingX = lm.acquire(3, 'k', 'X').then(() => {
    xGranted = true;
  });
  await tick();
  assert.equal(xGranted, false, 'X must wait while S locks are held');

  lm.release(1, 'k');
  await tick();
  assert.equal(xGranted, false, 'X still waits for the second S');
  lm.release(2, 'k');
  await pendingX;
  assert.equal(xGranted, true);

  lm.releaseAll(3);
  lm.unregisterTxn(1);
  lm.unregisterTxn(2);
  lm.unregisterTxn(3);
  assert.equal(lm.isQuiescent(), true, lm.diagnostics());
});

test('FIFO fairness: a queued X blocks later S requests (no barging)', async () => {
  const { lm } = makeManager();
  for (const t of [1, 2, 3]) lm.registerTxn(t);

  await lm.acquire(1, 'k', 'S');
  const order: string[] = [];
  const pendingX = lm.acquire(2, 'k', 'X').then(() => order.push('X2'));
  const pendingS = lm.acquire(3, 'k', 'S').then(() => order.push('S3'));
  await tick();
  assert.deepEqual(order, []);

  lm.release(1, 'k');
  await tick();
  assert.deepEqual(order, ['X2'], 'queued X is granted before the later S');

  lm.release(2, 'k');
  await Promise.all([pendingX, pendingS]);
  assert.deepEqual(order, ['X2', 'S3']);

  lm.releaseAll(3);
  for (const t of [1, 2, 3]) lm.unregisterTxn(t);
  assert.equal(lm.isQuiescent(), true, lm.diagnostics());
});

test('reentrant acquire of an already-held lock resolves immediately', async () => {
  const { lm } = makeManager();
  lm.registerTxn(1);
  await lm.acquire(1, 'k', 'X');
  await lm.acquire(1, 'k', 'X'); // same mode
  await lm.acquire(1, 'k', 'S'); // weaker mode while holding X
  lm.releaseAll(1);
  lm.unregisterTxn(1);
  assert.equal(lm.isQuiescent(), true, lm.diagnostics());
});

test('lock upgrade S->X succeeds for a sole holder and is recorded', async () => {
  const { lm } = makeManager();
  lm.registerTxn(1);
  await lm.acquire(1, 'k', 'S');
  await lm.acquire(1, 'k', 'X'); // upgrade
  assert.ok(lm.getEvents().includes('grant t1 X k'));
  lm.releaseAll(1);
  lm.unregisterTxn(1);
  assert.equal(lm.isQuiescent(), true, lm.diagnostics());
});

test('wound-wait: older requester wounds a younger conflicting holder', async () => {
  const { lm, victims } = makeManager();
  lm.registerTxn(1); // older (smaller timestamp)
  lm.registerTxn(2); // younger

  await lm.acquire(2, 'k', 'X'); // younger holds the lock
  await lm.acquire(1, 'k', 'X'); // older wounds younger, then takes it

  assert.deepEqual(victims, [2], 'younger holder must be wounded');
  assert.ok(lm.getEvents().includes('wound t2'));
  assert.ok(lm.getEvents().includes('grant t1 X k'));

  lm.releaseAll(1);
  lm.unregisterTxn(1);
  lm.unregisterTxn(2);
  assert.equal(lm.isQuiescent(), true, lm.diagnostics());
});

test('wound-wait: younger requester waits for an older holder (no wound)', async () => {
  const { lm, victims } = makeManager();
  lm.registerTxn(1); // older
  lm.registerTxn(2); // younger

  await lm.acquire(1, 'k', 'X');
  let granted = false;
  const pending = lm.acquire(2, 'k', 'X').then(() => {
    granted = true;
  });
  await tick();

  assert.deepEqual(victims, [], 'younger requester must not wound anyone');
  assert.equal(granted, false);
  // Wait-for edge points younger -> older, keeping the graph acyclic.
  assert.deepEqual(lm.waitForEdges(), [[2, 1]]);
  assert.equal(lm.hasCycle(), false);

  lm.release(1, 'k');
  await pending;
  assert.equal(granted, true);

  lm.releaseAll(2);
  lm.unregisterTxn(1);
  lm.unregisterTxn(2);
  assert.equal(lm.isQuiescent(), true, lm.diagnostics());
});

test('wounded victim has its pending requests rejected with LockAbortedError', async () => {
  const { lm, victims } = makeManager();
  lm.registerTxn(1);
  lm.registerTxn(2);
  lm.registerTxn(3);

  await lm.acquire(1, 'b', 'X'); // oldest holds B
  await lm.acquire(3, 'a', 'X'); // youngest holds A
  const waiting = lm.acquire(3, 'b', 'X'); // ...and waits for B (held by older)
  const rejection = assert.rejects(waiting, LockAbortedError);
  await lm.acquire(2, 'a', 'X'); // middle wounds the younger holder of A
  await rejection;
  assert.deepEqual(victims, [3]);

  lm.releaseAll(1);
  lm.releaseAll(2);
  for (const t of [1, 2, 3]) lm.unregisterTxn(t);
  assert.equal(lm.isQuiescent(), true, lm.diagnostics());
});

test('upgrade conflict applies wound-wait against the other shared holder', async () => {
  const { lm, victims } = makeManager();
  lm.registerTxn(1); // older
  lm.registerTxn(2); // younger

  await lm.acquire(1, 'k', 'S');
  await lm.acquire(2, 'k', 'S');
  await lm.acquire(1, 'k', 'X'); // older upgrades: wounds younger holder

  assert.deepEqual(victims, [2]);
  assert.ok(lm.getEvents().includes('grant t1 X k'));

  lm.releaseAll(1);
  lm.unregisterTxn(1);
  lm.unregisterTxn(2);
  assert.equal(lm.isQuiescent(), true, lm.diagnostics());
});

test('releasing a lock that is not held throws', async () => {
  const { lm } = makeManager();
  lm.registerTxn(1);
  assert.throws(() => lm.release(1, 'never-held'), /does not hold/);
  lm.unregisterTxn(1);
  assert.equal(lm.isQuiescent(), true);
});

test('acquiring with an unregistered transaction rejects', async () => {
  const { lm } = makeManager();
  await assert.rejects(lm.acquire(99, 'k', 'X'), /unregistered txn/);
  assert.equal(lm.isQuiescent(), true);
});

test('releaseAll frees held locks and wakes the next waiter', async () => {
  const { lm } = makeManager();
  lm.registerTxn(1);
  lm.registerTxn(2);

  await lm.acquire(1, 'a', 'X');
  await lm.acquire(1, 'b', 'X');
  let granted = false;
  const pending = lm.acquire(2, 'a', 'X').then(() => {
    granted = true;
  });
  await tick();
  assert.equal(granted, false);

  lm.releaseAll(1); // abort/commit path
  await pending;
  assert.equal(granted, true);

  lm.releaseAll(2);
  lm.unregisterTxn(1);
  lm.unregisterTxn(2);
  assert.equal(lm.isQuiescent(), true, lm.diagnostics());
});

test('grant history is linearizable: no overlapping conflicting holders', async () => {
  const { lm } = makeManager();
  const txns = [1, 2, 3, 4];
  for (const t of txns) lm.registerTxn(t);

  // Every txn hammers the same key with X locks in interleaved order.
  await Promise.all(
    txns.map(async (t) => {
      for (let i = 0; i < 20; i++) {
        try {
          await lm.acquire(t, 'hot', 'X');
          await tick(1);
          lm.release(t, 'hot');
        } catch (err) {
          // Wounded by an older txn while waiting or holding: the victim
          // handler already released its locks. Both surfaces are valid.
          assert.ok(
            err instanceof LockAbortedError ||
              (err instanceof Error && err.message.includes('does not hold')),
            `unexpected error: ${err}`,
          );
        }
      }
    }),
  );

  // Rebuild holder intervals from the event log and verify mutual exclusion.
  const held = new Map<number, string>();
  for (const event of lm.getEvents()) {
    const parts = event.split(' ');
    const kind = parts[0];
    const txn = Number(parts[1].slice(1));
    const key = parts[parts.length - 1];
    if (key !== 'hot') continue;
    if (kind === 'grant') {
      assert.equal(
        held.size,
        0,
        `grant to t${txn} while held by ${[...held.keys()]}: ${event}`,
      );
      held.set(txn, key);
    } else if (kind === 'release') {
      assert.equal(held.has(txn), true, `release without grant: ${event}`);
      held.delete(txn);
    }
  }
  assert.equal(held.size, 0, 'all locks released at the end');

  for (const t of txns) lm.unregisterTxn(t);
  assert.equal(lm.isQuiescent(), true, lm.diagnostics());
});
