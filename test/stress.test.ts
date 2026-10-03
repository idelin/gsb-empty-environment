import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WalManager } from '../src/wal.ts';
import { BufferPool, Disk, LatchManager } from '../src/storage.ts';
import { TransactionManager } from '../src/transaction-manager.ts';
import { RecoveryEngine } from '../src/recovery.ts';
import { LockConflictError } from '../src/locks.ts';

/**
 * Deterministic pseudo-random interleaving stress test: many concurrent
 * transactions on a small page set, random commits/aborts, random page
 * flushes (steal/no-force), a checkpoint, then a crash. The recovered
 * state must equal the independently computed committed-only state.
 */
test('stress: interleaved txns, crash, recovery matches committed-only state', () => {
  // Deterministic PRNG (mulberry32) so the test is reproducible.
  let seed = 0xC0FFEE;
  const rand = () => {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };

  const wal = new WalManager();
  const disk = new Disk();
  const pool = new BufferPool(disk, wal);
  const latches = new LatchManager();
  const tm = new TransactionManager(wal, pool, latches);

  const PAGES = ['P1', 'P2', 'P3', 'P4'];
  const N_TXN = 24;
  const committed = new Set<number>();
  const aborted = new Set<number>();
  // Model of what each committed txn wrote, applied in commit order.
  const committedWrites: Array<{ page: string; key: string; value: number; txn: number }> = [];
  const txns: number[] = [];
  for (let i = 0; i < N_TXN; i++) txns.push(tm.begin());

  const open = new Set(txns);
  let ckptDone = false;
  while (open.size > 0) {
    const ids = [...open];
    const txnId = ids[Math.floor(rand() * ids.length)]!;
    const action = rand();
    if (action < 0.6) {
      const page = PAGES[Math.floor(rand() * PAGES.length)]!;
      const key = `k${Math.floor(rand() * 3)}`;
      const value = Math.floor(rand() * 1000);
      try {
        tm.update(txnId, page, key, value);
      } catch (e) {
        // Strict 2PL conflict: the deterministic scheduler just skips.
        if (e instanceof LockConflictError) continue;
        throw e;
      }
      committedWrites.push({ page, key, value, txn: txnId });
    } else if (action < 0.8) {
      tm.commit(txnId);
      committed.add(txnId);
      open.delete(txnId);
    } else {
      tm.abort(txnId);
      aborted.add(txnId);
      open.delete(txnId);
    }
    if (!ckptDone && committed.size >= N_TXN / 4) {
      tm.checkpoint();
      ckptDone = true;
    }
    // Random steal flushes (only when the WAL protocol permits).
    if (rand() < 0.3) {
      wal.flush(wal.getLastLSN());
      const victim = PAGES[Math.floor(rand() * PAGES.length)]!;
      pool.flushPage(victim);
    }
  }

  // A few extra txns that stay in flight across the crash (losers).
  const loserWrites: Array<{ page: string; key: string; value: number }> = [];
  let loserLocks = 0;
  for (let i = 0; i < 4; i++) {
    const t = tm.begin();
    const page = PAGES[Math.floor(rand() * PAGES.length)]!;
    const key = `k${Math.floor(rand() * 3)}`;
    const value = 10_000 + i;
    try {
      tm.update(t, page, key, value);
      loserLocks++;
    } catch (e) {
      if (e instanceof LockConflictError) continue;
      throw e;
    }
    loserWrites.push({ page, key, value });
  }

  // Locks of ended transactions are all released; only in-flight losers'
  // locks remain, and they are destroyed with the (volatile) lock manager.
  assert.equal(tm.lockLeakCount(), loserLocks, 'only loser locks survive');

  wal.flush(wal.getLastLSN());
  const walFile = wal.persistToFile();
  pool.crash();

  // Independently compute the expected committed-only final state by
  // replaying only committed transactions' writes in log order.
  const expected: Record<string, Record<string, number>> = {};
  for (const w of committedWrites) {
    if (!committed.has(w.txn)) continue;
    (expected[w.page] ??= {})[w.key] = w.value;
  }

  const engine = new RecoveryEngine(WalManager.loadFromFile(walFile), disk);
  engine.recover();

  const actual: Record<string, Record<string, number>> = {};
  for (const [pid, p] of disk.allPages()) actual[pid] = p.data as Record<string, number>;
  // Pages with no surviving committed writes must be empty or absent.
  for (const [pid, data] of Object.entries(actual)) {
    assert.deepEqual(data, expected[pid] ?? {}, `page ${pid}`);
  }
  for (const [pid, data] of Object.entries(expected)) {
    assert.deepEqual(actual[pid] ?? {}, data, `page ${pid}`);
  }

  // Strict no-leak assertions.
  assert.equal(engine.getTxnTable().size, 0, 'no transaction leaks');
  assert.equal(engine.dirtyPagesAfterRecovery().length, 0, 'no dirty page leaks');
  assert.equal(engine.latchLeakCount(), 0, 'no recovery latch leaks');
  assert.equal(latches.heldCount, 0, 'no forward-processing latch leaks');
});
