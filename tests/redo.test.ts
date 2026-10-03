import test from 'node:test';
import assert from 'node:assert/strict';
import { DatabaseSystem } from '../src/system.ts';
import { analysisPhase, redoPhase } from '../src/recovery.ts';
import { LogManager } from '../src/logManager.ts';
import { BufferPool } from '../src/bufferPool.ts';
import { LogType } from '../src/types.ts';
import { buildAcceptanceScenario } from './helpers.ts';

function restartFrom(system: DatabaseSystem): { lm: LogManager; bp: BufferPool } {
  const lm = new LogManager(system.disk);
  const bp = new BufferPool(system.disk, lm);
  return { lm, bp };
}

test('redo: repeats history including uncommitted updates and CLRs', () => {
  const system = new DatabaseSystem();
  const t1 = system.begin();
  const t2 = system.begin();
  system.update(t1, 1, 10);
  system.update(t2, 2, 20);
  system.update(t1, 1, 11);
  system.abort(t2); // CLRs for t2 are part of history
  system.logManager.flushAll();
  // Nothing flushed to disk: every update must be redone.
  system.crash();

  const { lm, bp } = restartFrom(system);
  const log = lm.readAll();
  const analysis = analysisPhase(log);
  const stats = redoPhase(log, analysis, system.disk, bp);

  const updateCount = log.filter((r) => r.type === LogType.Update).length;
  const clrCount = log.filter((r) => r.type === LogType.Clr).length;
  assert.equal(stats.applied, updateCount + clrCount);
  assert.equal(bp.read(1), 11, 'committed-bound updates re-applied');
  assert.equal(bp.read(2), 0, 'aborted txn stays undone (CLR redone)');
});

test('redo: skips pages already on disk with a newer pageLSN', () => {
  const { system } = buildAcceptanceScenario();
  // P1 was stolen to disk at pageLSN 8; its updates (LSN 1 and 8) must be skipped.
  system.crash();
  const { lm, bp } = restartFrom(system);
  const log = lm.readAll();
  const analysis = analysisPhase(log);
  const stats = redoPhase(log, analysis, system.disk, bp);

  assert.equal(stats.applied, 5); // LSN 2,3,11,12,13
  assert.equal(stats.skipped, 2); // LSN 1,8 already durable on P1
  assert.equal(system.disk.pageLSN(1), 8, 'P1 untouched by redo');
});

test('redo: starts at the minimum recLSN from the checkpoint DPT', () => {
  const system = new DatabaseSystem();
  const t1 = system.begin();
  system.update(t1, 1, 1); // LSN 1
  system.update(t1, 2, 2); // LSN 2
  system.bufferPool.flushPage(1); // page 1 clean again
  system.checkpoint();            // LSN 3,4: DPT snapshot only contains page 2
  system.update(t1, 2, 3); // LSN 5
  system.logManager.flushAll();
  system.crash();

  const { lm, bp } = restartFrom(system);
  const log = lm.readAll();
  const analysis = analysisPhase(log);
  assert.deepEqual([...analysis.dirtyPageTable.entries()], [[2, 2]]);
  assert.equal(analysis.redoLSN, 2, 'redo starts at the first dirtying LSN of page 2');
  const stats = redoPhase(log, analysis, system.disk, bp);
  // LSN 1 is before redoLSN (never visited); LSN 2 and 5 are applied to page 2.
  assert.equal(stats.applied, 2);
  assert.equal(stats.skipped, 0);
  assert.equal(bp.read(2), 3);
});

test('redo: is idempotent — a second pass applies nothing', () => {
  const { system } = buildAcceptanceScenario();
  system.crash();
  const { lm, bp } = restartFrom(system);
  const log = lm.readAll();
  const analysis = analysisPhase(log);
  const first = redoPhase(log, analysis, system.disk, bp);
  bp.flushAll();
  const second = redoPhase(log, analysis, system.disk, bp);
  assert.ok(first.applied > 0);
  assert.equal(second.applied, 0, 'nothing left to redo');
});
