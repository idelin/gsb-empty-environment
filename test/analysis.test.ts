import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LogManager } from '../src/log.ts';
import { BufferPool, Disk } from '../src/storage.ts';
import { RecoveryManager } from '../src/recovery.ts';
import { buildAcceptanceScenario } from './helpers.ts';

test('analysis reconstructs DPT and txn table from checkpoint forward', () => {
  const s = buildAcceptanceScenario();
  const rm = new RecoveryManager(s.log, s.disk, s.buffer);
  const a = rm.analyze();

  assert.ok(a.checkpointBeginLSN > 0, 'must locate the begin checkpoint');

  const statuses = new Map([...a.txnTable].map(([id, e]) => [id, e.status]));
  assert.equal(statuses.size, 3, 'only the 3 in-flight loser txns remain');
  for (const t of s.losers) assert.equal(statuses.get(t), 'RUNNING');
  assert.equal(statuses.has(s.winners[0]), false, 't1 END flushed by checkpoint');
  assert.equal(statuses.has(s.winners[1]), false, 't2 END flushed by group commit');

  assert.deepEqual([...a.dpt.keys()].sort((x, y) => x - y), [1, 2, 3, 4, 5]);
  assert.equal(a.dpt.get(1), 1, 'P1 dirty since LSN 1 (checkpoint snapshot)');
  assert.equal(a.dpt.get(2), 2, 'P2 dirty since LSN 2 (checkpoint snapshot)');
  for (const pg of [3, 4, 5]) {
    assert.ok(a.dpt.get(pg)! > a.checkpointBeginLSN, `P${pg} dirtied after checkpoint`);
  }
  assert.equal(a.redoLSN, 1, 'redo starts at oldest recLSN in DPT');
});

test('analysis on empty log yields empty tables', () => {
  const log = new LogManager();
  const disk = new Disk();
  const buffer = new BufferPool(disk);
  const rm = new RecoveryManager(log, disk, buffer);
  const a = rm.analyze();
  assert.equal(a.txnTable.size, 0);
  assert.equal(a.dpt.size, 0);
  assert.equal(a.redoLSN, 0);
});
