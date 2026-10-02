import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { Inspector } from '../src/inspector.ts';
import type { LogRecord } from '../src/log-record.ts';
import { RecoveryEngine } from '../src/recovery.ts';
import { buildCrashScenario } from '../src/scenario.ts';
import { Storage } from '../src/storage.ts';
import { Wal } from '../src/wal.ts';
import { cleanupDir, makeTempDir } from './helpers.ts';

test('concurrent appends: unique contiguous LSNs, intact per-txn chains, zero leaks', async () => {
  const dir = await makeTempDir();
  try {
    const walPath = join(dir, 'wal.log');
    const wal = await Wal.create(walPath);
    const txnIds = ['T1', 'T2', 'T3', 'T4', 'T5'];
    const opsPerTxn = 9; // BEGIN + 6 UPDATE + COMMIT + END

    // 5 concurrent writers, each streaming its own transaction.
    await Promise.all(
      txnIds.map((txnId, t) =>
        (async () => {
          await wal.append({ kind: 'BEGIN', txnId });
          for (let i = 0; i < 6; i++) {
            await wal.append({
              kind: 'UPDATE',
              txnId,
              pageId: `P${(t % 3) + 1}`,
              key: `k${i}`,
              before: i === 0 ? null : i - 1,
              after: i,
            });
          }
          await wal.append({ kind: 'COMMIT', txnId });
          await wal.append({ kind: 'END', txnId });
        })(),
      ),
    );

    const total = txnIds.length * opsPerTxn;
    assert.equal(wal.size, total);
    assert.equal(wal.pendingWrites, 0, 'no write left behind (no leak)');

    const records = wal.scan();
    const lsns = records.map((r) => r.lsn).sort((a, b) => a - b);
    assert.deepEqual(lsns, Array.from({ length: total }, (_, i) => i + 1), 'LSNs are exactly 1..N');

    // Per-transaction prevLSN chains are intact despite interleaving.
    const byTxn = new Map<string, LogRecord[]>();
    for (const r of records) {
      if (r.txnId === null) continue;
      const list = byTxn.get(r.txnId) ?? [];
      list.push(r);
      byTxn.set(r.txnId, list);
    }
    for (const [txnId, chain] of byTxn) {
      assert.equal(chain.length, opsPerTxn);
      chain.forEach((r, i) => {
        assert.equal(r.prevLSN, i === 0 ? null : chain[i - 1].lsn, `${txnId} chain broken at LSN ${r.lsn}`);
      });
    }

    // Durability: file holds exactly one JSONL line per record.
    const lines = (await readFile(walPath, 'utf8')).split('\n').filter((l) => l.trim() !== '');
    assert.equal(lines.length, total);

    // The interleaved-but-well-formed log passes every WAL invariant.
    const report = new Inspector().inspectWal(records);
    assert.deepEqual(report.violations, []);
    assert.equal(report.ok, true);
  } finally {
    await cleanupDir(dir);
  }
});

test('concurrent inspectors are deterministic and side-effect free', async () => {
  const dir = await makeTempDir();
  try {
    const { wal, storage } = await buildCrashScenario(dir);
    const records = wal.scan();
    const inspector = new Inspector();
    const reports = await Promise.all(
      Array.from({ length: 50 }, () => Promise.resolve().then(() => inspector.inspectWal(records))),
    );
    const first = JSON.stringify(reports[0]);
    for (const report of reports) {
      assert.equal(JSON.stringify(report), first);
      assert.equal(report.ok, true);
    }
  } finally {
    await cleanupDir(dir);
  }
});

test('concurrent recovery on isolated replicas converges to identical state', async () => {
  const dirs = await Promise.all([makeTempDir(), makeTempDir(), makeTempDir()]);
  try {
    const results = await Promise.all(
      dirs.map(async (dir) => {
        const { wal, storage } = await buildCrashScenario(dir);
        await new RecoveryEngine(wal, storage).recover();
        return {
          wal: wal.scan(),
          pages: storage.snapshot(),
          skew: new Inspector().detectDataSkew(wal.scan(), storage),
        };
      }),
    );
    const reference = JSON.stringify({ wal: results[0].wal, pages: results[0].pages });
    for (const r of results) {
      assert.equal(JSON.stringify({ wal: r.wal, pages: r.pages }), reference);
      assert.deepEqual(r.skew, []);
    }
  } finally {
    await Promise.all(dirs.map(cleanupDir));
  }
});

test('appends stay consistent while a snapshot scan is in flight', async () => {
  const dir = await makeTempDir();
  try {
    const wal = await Wal.create(join(dir, 'wal.log'));
    await wal.append({ kind: 'BEGIN', txnId: 'T1' });
    const scans: LogRecord[][] = [];
    await Promise.all([
      (async () => {
        for (let i = 0; i < 50; i++) {
          await wal.append({ kind: 'UPDATE', txnId: 'T1', pageId: 'P1', key: `k${i}`, before: null, after: i });
        }
        await wal.append({ kind: 'COMMIT', txnId: 'T1' });
        await wal.append({ kind: 'END', txnId: 'T1' });
      })(),
      (async () => {
        for (let i = 0; i < 20; i++) {
          scans.push(wal.scan());
          await new Promise((r) => setImmediate(r));
        }
      })(),
    ]);
    // Snapshots are monotonically growing prefixes of the final log.
    const final = wal.scan();
    for (const snap of scans) {
      assert.ok(snap.length <= final.length);
      snap.forEach((r, i) => assert.deepEqual(r, final[i]));
    }
    assert.equal(wal.pendingWrites, 0);
    assert.equal(new Inspector().inspectWal(final).ok, true);
  } finally {
    await cleanupDir(dir);
  }
});

test('checkpoint appended concurrently with transactions keeps the log well-formed', async () => {
  const dir = await makeTempDir();
  try {
    const wal = await Wal.create(join(dir, 'wal.log'));
    const storage = await Storage.create(join(dir, 'db.json'), { P1: { a: 0 } });
    const txns = ['T1', 'T2', 'T3'].map((txnId) =>
      (async () => {
        await wal.append({ kind: 'BEGIN', txnId });
        const update = await wal.append({ kind: 'UPDATE', txnId, pageId: 'P1', key: txnId, before: null, after: 1 });
        // Flush the page so the empty-DPT checkpoints below are truthful.
        storage.setValue('P1', txnId, 1, update.lsn);
        await wal.append({ kind: 'COMMIT', txnId });
        await wal.append({ kind: 'END', txnId });
      })(),
    );
    const checkpoints = (async () => {
      for (let i = 0; i < 3; i++) {
        await wal.append({ kind: 'CHECKPOINT', txnId: null, txnTable: [], dirtyPageTable: [] });
      }
    })();
    await Promise.all([...txns, checkpoints]);

    const records = wal.scan();
    assert.equal(wal.size, 15);
    assert.equal(wal.pendingWrites, 0);
    assert.equal(new Inspector().inspectWal(records).ok, true);

    // And the log is still recoverable end to end.
    const engine = new RecoveryEngine(wal, storage);
    const report = await engine.recover();
    assert.equal(report.analysis.txnTable.size, 0, 'no leaked transactions');
    assert.equal(storage.getValue('P1', 'T1'), 1);
    assert.equal(storage.getValue('P1', 'T2'), 1);
    assert.equal(storage.getValue('P1', 'T3'), 1);
  } finally {
    await cleanupDir(dir);
  }
});
