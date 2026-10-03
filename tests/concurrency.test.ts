import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { LogManager } from '../src/log-manager.ts';
import { StorageEngine } from '../src/storage.ts';
import { LockManager, LockAbortedError } from '../src/lock-manager.ts';
import {
  TransactionManager,
  TransactionAbortedError,
} from '../src/transaction.ts';
import { AriesRecovery } from '../src/recovery.ts';
import { mulberry32, randomInt } from '../src/random.ts';

const ACCOUNT_PAGE = 10;
const NUM_ACCOUNTS = 10;
const INITIAL_BALANCE = 1000;

interface World {
  dir: string;
  walPath: string;
  log: LogManager;
  storage: StorageEngine;
  locks: LockManager;
  tm: TransactionManager;
}

async function makeWorld(seedBalances = true): Promise<World> {
  const dir = mkdtempSync(join(tmpdir(), 'aries-conc-'));
  const walPath = join(dir, 'wal.log');
  const log = new LogManager(walPath, { truncate: true });
  const storage = new StorageEngine(log, { slotsPerPage: NUM_ACCOUNTS });
  const locks = new LockManager();
  const tm = new TransactionManager(log, storage, locks);
  if (seedBalances) {
    const t = tm.begin();
    for (let i = 0; i < NUM_ACCOUNTS; i++) {
      await tm.write(t, ACCOUNT_PAGE, i, String(INITIAL_BALANCE));
    }
    tm.commit(t);
    storage.flushAll();
  }
  return { dir, walPath, log, storage, locks, tm };
}

async function totalBalance(tm: TransactionManager): Promise<number> {
  const t = tm.begin();
  let sum = 0;
  for (let i = 0; i < NUM_ACCOUNTS; i++) {
    sum += Number(await tm.read(t, ACCOUNT_PAGE, i));
  }
  tm.commit(t);
  return sum;
}

function diskTotal(storage: StorageEngine): number {
  let sum = 0;
  for (let i = 0; i < NUM_ACCOUNTS; i++) {
    sum += Number(storage.readDiskSlot(ACCOUNT_PAGE, i) || '0');
  }
  return sum;
}

/**
 * One executor "node": runs `txnsPerNode` transfer transactions against
 * the shared arbiter. When `shouldCrash()` flips, the node dies on the
 * spot, abandoning any in-flight transaction (no abort, no cleanup) —
 * exactly what a real crash looks like to the recovery engine.
 */
async function runNode(
  world: World,
  rand: () => number,
  txnsPerNode: number,
  shouldCrash: () => boolean,
): Promise<void> {
  const { tm } = world;
  for (let i = 0; i < txnsPerNode; i++) {
    if (shouldCrash()) return;
    const txnId = tm.begin();
    try {
      const from = randomInt(rand, 0, NUM_ACCOUNTS - 1);
      let to = randomInt(rand, 0, NUM_ACCOUNTS - 1);
      if (to === from) to = (to + 1) % NUM_ACCOUNTS;
      const amount = randomInt(rand, 1, 50);
      const fromBal = Number(await tm.read(txnId, ACCOUNT_PAGE, from));
      if (shouldCrash()) return;
      await tm.write(txnId, ACCOUNT_PAGE, from, String(fromBal - amount));
      if (shouldCrash()) return;
      const toBal = Number(await tm.read(txnId, ACCOUNT_PAGE, to));
      await tm.write(txnId, ACCOUNT_PAGE, to, String(toBal + amount));
      if (shouldCrash()) return;
      if (rand() < 0.1) {
        tm.abort(txnId); // voluntary abort: must leave no trace
      } else {
        tm.commit(txnId);
      }
    } catch (err) {
      if (
        err instanceof LockAbortedError ||
        err instanceof TransactionAbortedError
      ) {
        tm.abort(txnId); // no-op if the arbiter already aborted it
      } else {
        throw err;
      }
    }
  }
}

test('multi-node transfers preserve the balance invariant under contention', async () => {
  const world = await makeWorld();
  try {
    const rand = mulberry32(42);
    const nodes = Array.from({ length: 8 }, (_, i) =>
      runNode(world, mulberry32(1000 + i), 30, () => false),
    );
    await Promise.all(nodes);
    void rand;

    // Every transaction is accounted for exactly once (checked before the
    // balance probe below commits its own transaction).
    const { committed, aborted } = world.tm;
    assert.equal(committed.size + aborted.size, 8 * 30 + 1 /* seed txn */);
    for (const id of committed) assert.equal(aborted.has(id), false);

    // Atomicity + isolation: the invariant holds after every committed txn.
    assert.equal(await totalBalance(world.tm), NUM_ACCOUNTS * INITIAL_BALANCE);

    // Strictly no leaks: no locks, no waiters, no active transactions,
    // and the wait-for graph stayed acyclic throughout.
    assert.equal(world.locks.hasCycle(), false);
    assert.equal(world.locks.isQuiescent(), true, world.locks.diagnostics());
    assert.equal(world.tm.activeTxnCount(), 0);
  } finally {
    rmSync(world.dir, { recursive: true, force: true });
  }
});

test('crash mid-workload: recovery preserves committed work, rolls back the rest', async () => {
  const world = await makeWorld();
  try {
    let crashed = false;
    const nodes = Array.from({ length: 6 }, (_, i) =>
      runNode(world, mulberry32(5000 + i), 40, () => crashed),
    );
    // Let the workload run, then "pull the plug" while transactions are
    // still in flight (guaranteed losers for the recovery phase).
    for (let i = 0; i < 1_000_000 && !crashed; i++) {
      await Promise.resolve();
      if (world.tm.committed.size >= 25 && world.tm.activeTxnCount() > 0) {
        crashed = true;
      }
    }
    await Promise.all(nodes);
    const committedBefore = new Set(world.tm.committed);
    assert.ok(committedBefore.size >= 25, 'workload made progress');

    world.log.flush(); // crash point: WAL durable, buffer pool lost
    world.storage.crash();

    const log2 = new LogManager(world.walPath);
    const storage2 = new StorageEngine(log2, { slotsPerPage: NUM_ACCOUNTS });
    // Recover onto the same durable pages the crashed engine flushed.
    const report = new AriesRecovery(log2, storage2).recover(world.walPath);

    // Independently recompute the expected state: replay, in LSN order,
    // only the UPDATEs of transactions that committed before the crash.
    const wal = LogManager.readFile(world.walPath);
    const committedIds = new Set(
      wal.filter((r) => r.type === 'COMMIT').map((r) => r.txnId),
    );
    const expected = new Array<string>(NUM_ACCOUNTS).fill('');
    for (const rec of wal) {
      if (rec.type === 'UPDATE' && rec.pageId === ACCOUNT_PAGE && committedIds.has(rec.txnId)) {
        expected[rec.offset!] = rec.afterImage!;
      }
    }
    for (let i = 0; i < NUM_ACCOUNTS; i++) {
      assert.equal(
        storage2.readDiskSlot(ACCOUNT_PAGE, i),
        expected[i],
        `account ${i}`,
      );
    }

    // The money invariant survives the crash.
    assert.equal(diskTotal(storage2), NUM_ACCOUNTS * INITIAL_BALANCE);

    // Every loser was compensated and ended; nothing committed was undone.
    for (const id of committedBefore) {
      assert.equal(report.undo.losers.includes(id), false);
    }
    assert.ok(report.undo.losers.length > 0, 'crash left losers to undo');
    assert.equal(log2.flushedUpTo, log2.lastLSN, 'no unflushed log records');
  } finally {
    rmSync(world.dir, { recursive: true, force: true });
  }
});

test('no deadlock: random multi-lock workloads always terminate', async () => {
  const world = await makeWorld();
  try {
    const rand = mulberry32(7);
    // Transactions grab up to 4 accounts in random order: a classic
    // deadlock trap that wound-wait must dissolve.
    const nodes = Array.from({ length: 10 }, (_, i) =>
      (async () => {
        const r = mulberry32(9000 + i);
        for (let k = 0; k < 25; k++) {
          const txnId = world.tm.begin();
          try {
            const picks = new Set<number>();
            const n = randomInt(r, 2, 4);
            while (picks.size < n) picks.add(randomInt(r, 0, NUM_ACCOUNTS - 1));
            for (const acct of picks) {
              const bal = Number(await world.tm.read(txnId, ACCOUNT_PAGE, acct));
              await world.tm.write(txnId, ACCOUNT_PAGE, acct, String(bal));
            }
            world.tm.commit(txnId);
          } catch (err) {
            if (
              err instanceof LockAbortedError ||
              err instanceof TransactionAbortedError
            ) {
              world.tm.abort(txnId);
            } else {
              throw err;
            }
          }
        }
      })(),
    );
    await Promise.all(nodes);
    void rand;

    assert.equal(world.locks.hasCycle(), false);
    assert.equal(world.locks.isQuiescent(), true, world.locks.diagnostics());
    assert.equal(world.tm.activeTxnCount(), 0);
    assert.equal(await totalBalance(world.tm), NUM_ACCOUNTS * INITIAL_BALANCE);
  } finally {
    rmSync(world.dir, { recursive: true, force: true });
  }
});
