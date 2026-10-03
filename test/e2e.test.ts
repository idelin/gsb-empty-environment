import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { buildAcceptanceScenario, recoverAcceptanceScenario } from '../src/scenario.ts';
import { verifyRecoveredState } from '../src/verify.ts';
import { FileWALStorage } from '../src/wal.ts';
import type { LogRecord } from '../src/types.ts';

// Acceptance boundary: WAL file with 3 uncommitted + 2 committed txns and a
// checkpoint, then a crash; the recovery engine must roll back the uncommitted
// changes and persist the committed ones.
test('end-to-end: checkpointed WAL with 2 committed + 3 uncommitted txns recovers correctly', () => {
  const dir = mkdtempSync(join(tmpdir(), 'aries-e2e-'));
  const walPath = join(dir, 'wal.jsonl');

  const scenario = buildAcceptanceScenario(walPath);
  const { t1, t2 } = scenario.committed;
  const { t3, t4, t5 } = scenario.uncommitted;

  // The WAL file exists and captures the crash state.
  assert.equal(existsSync(walPath), true);
  const preWal: LogRecord[] = new FileWALStorage(walPath).readAll();
  assert.equal(preWal.length, 13);
  assert.equal(preWal.filter((r) => r.type === 'COMMIT').length, 2);
  assert.equal(preWal.some((r) => r.type === 'END_CHECKPOINT'), true);
  // Stolen page: P1 on disk carries T3's uncommitted write before recovery.
  assert.equal(scenario.disk.readPage(1).data['k1'], 11);

  const { report, log, bp } = recoverAcceptanceScenario(walPath, scenario.disk);

  // Analysis found exactly the 3 uncommitted txns as losers.
  assert.deepEqual(report.losers, [t3, t4, t5]);
  assert.equal(report.analysis.checkpointBeginLSN !== null, true);
  assert.deepEqual(
    [...report.analysis.tt.keys()].sort((a, b) => a - b),
    [t3, t4, t5],
  );

  // Undo compensated every uncommitted update (T3:1, T4:2, T5:1).
  assert.equal(report.clrsWritten, 4);
  assert.equal(report.endsWritten, 3);

  // Committed data persisted, uncommitted data rolled back.
  assert.equal(scenario.disk.readPage(1).data['k1'], 10); // T1 wins over T3
  assert.equal(scenario.disk.readPage(2).data['k1'], 20); // T1 wins over T4
  assert.equal(scenario.disk.readPage(3).data['k1'], 30); // T2 wins over T5
  assert.equal(scenario.disk.readPage(4).data['k1'], 0); // T4's insert undone

  // Recovery records are durable in the WAL file itself.
  const postWal: LogRecord[] = new FileWALStorage(walPath).readAll();
  assert.equal(postWal.length, 20);
  assert.equal(postWal.filter((r) => r.type === 'CLR').length, 4);
  for (const txn of [t3, t4, t5]) {
    const last = [...postWal].reverse().find((r) => 'txnId' in r && r.txnId === txn);
    assert.equal(last?.type, 'END');
  }

  // No leaks: buffer pool fully flushed, log tail empty, verification clean.
  assert.equal(bp.allClean(), true);
  assert.equal(log.pendingTail, 0);
  const verification = verifyRecoveredState(scenario.disk, postWal);
  assert.deepEqual(verification.errors, []);
  assert.deepEqual(verification.winners, [t1, t2]);
  assert.deepEqual(verification.losers, [t3, t4, t5]);
});

test('end-to-end: WAL file is valid JSON lines, human-inspectable', () => {
  const dir = mkdtempSync(join(tmpdir(), 'aries-e2e-'));
  const walPath = join(dir, 'wal.jsonl');
  const scenario = buildAcceptanceScenario(walPath);
  recoverAcceptanceScenario(walPath, scenario.disk);
  const lines = readFileSync(walPath, 'utf8').trim().split('\n');
  assert.equal(lines.length, 20);
  for (const line of lines) {
    const record = JSON.parse(line) as LogRecord;
    assert.equal(typeof record.lsn, 'number');
    assert.equal(typeof record.type, 'string');
  }
});
