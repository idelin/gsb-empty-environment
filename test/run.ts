import { CancellationToken } from '../src/cancellation';
import { Gate } from '../src/deferred';
import { runImport } from '../src/importer';
import { VirtualStorage, gatedCommit } from '../src/virtual-storage';
import { assert, assertDeepEqual, assertEqual, run, test } from './harness';
import { makeSource } from './fixtures';

// 10 rows, batchSize 4 -> batches of 4, 4, 2
const SOURCE = makeSource(10);
const BATCH_SIZE = 4;

test('cancel before any commit: zero visible writes', async () => {
  const storage = new VirtualStorage();
  const token = new CancellationToken();
  token.cancel();

  const report = await runImport(SOURCE, { storage, batchSize: BATCH_SIZE, token });

  assert(report.cancelled, 'report should be marked cancelled');
  assertEqual(report.committedRows, 0, 'no rows committed');
  assertEqual((await storage.allRows()).length, 0, 'storage must stay empty');
  assertEqual((await storage.committedBatchIds()).length, 0, 'no committed batches');
  assertEqual(storage.commitLog.length, 0, 'commit log must stay empty');
  assert(
    report.batches.every((b) => b.status === 'not-started'),
    'every batch should be not-started',
  );
});

test('cancel between batches (via hasBatch hook): zero visible writes', async () => {
  const token = new CancellationToken();
  // Cancel fires after parse/validate, right before the first commit decision.
  const storage = new VirtualStorage({
    beforeHasBatch: () => token.cancel(),
  });

  const report = await runImport(SOURCE, { storage, batchSize: BATCH_SIZE, token });

  assert(report.cancelled, 'report should be marked cancelled');
  assertEqual((await storage.allRows()).length, 0, 'storage must stay empty');
  assertEqual(storage.commitLog.length, 0, 'no batch may reach commit');
  assert(
    report.batches.every((b) => b.status === 'not-started'),
    'every batch should be not-started',
  );
});

test('cancel during in-flight commit: current batch returns real result, later batches stop', async () => {
  const gate = new Gate();
  const token = new CancellationToken();
  const entered: string[] = [];
  const storage = new VirtualStorage(
    gatedCommit(gate, (batchId) => {
      entered.push(batchId);
      // Cancel while the first commit is parked inside storage.
      token.cancel();
      gate.open();
    }),
  );

  const report = await runImport(SOURCE, { storage, batchSize: BATCH_SIZE, token });

  assert(report.cancelled, 'report should be marked cancelled');
  assertEqual(entered.length, 1, 'exactly one commit may start');
  const first = report.batches[0];
  assertEqual(first.status, 'committed', 'in-flight batch completes');
  assert(first.result !== undefined, 'in-flight batch reports its real result');
  assertEqual(first.result!.inserted, 4, 'real inserted count from storage');
  assertEqual(first.result!.batchId, first.id, 'result carries the batch id');
  assertDeepEqual(
    report.batches.slice(1).map((b) => b.status),
    ['not-started', 'not-started'],
    'later batches never start',
  );
  assertEqual((await storage.allRows()).length, 4, 'only the in-flight batch is visible');
  assertEqual(report.committedRows, 4, 'committedRows reflects the real result');
});

test('batch ids are stable across runs and machines', async () => {
  const first = await runImport(SOURCE, { storage: new VirtualStorage(), batchSize: BATCH_SIZE });
  const second = await runImport(SOURCE, { storage: new VirtualStorage(), batchSize: BATCH_SIZE });

  assertDeepEqual(
    first.batches.map((b) => b.id),
    second.batches.map((b) => b.id),
    'same input yields the same batch ids',
  );
  assert(
    first.batches.every((b) => /^batch-\d+-[0-9a-f]{16}$/.test(b.id)),
    'ids have the stable content-hash format',
  );
  const ids = first.batches.map((b) => b.id);
  assertEqual(new Set(ids).size, ids.length, 'ids are unique within a run');
});

test('restart skips committed batches without duplicating rows', async () => {
  const gate = new Gate();
  const tokenA = new CancellationToken();
  const storage = new VirtualStorage(
    gatedCommit(gate, () => {
      // "Crash" the first run while the first batch commit is in flight.
      tokenA.cancel();
      gate.open();
    }),
  );

  const crashed = await runImport(SOURCE, { storage, batchSize: BATCH_SIZE, token: tokenA });
  assertEqual(
    crashed.batches.filter((b) => b.status === 'committed').length,
    1,
    'first run commits exactly one batch before stopping',
  );
  const committedIds = await storage.committedBatchIds();
  assertEqual(committedIds.length, 1, 'one batch durable in storage');

  // Restart with a fresh token against the same storage.
  const resumed = await runImport(SOURCE, { storage, batchSize: BATCH_SIZE, token: new CancellationToken() });

  assertEqual(resumed.cancelled, false, 'resumed run completes');
  assertEqual(resumed.batches[0].status, 'skipped', 'already-committed batch is skipped');
  assertEqual(resumed.batches[0].id, committedIds[0], 'skipped batch matches the durable id');
  assertDeepEqual(
    resumed.batches.slice(1).map((b) => b.status),
    ['committed', 'committed'],
    'remaining batches commit on resume',
  );
  assertEqual(storage.commitLog.length, 3, 'each batch committed exactly once across both runs');

  const rows = await storage.allRows();
  assertEqual(rows.length, 10, 'all rows present after resume');
  assertEqual(new Set(rows.map((r) => r.id)).size, 10, 'no duplicate rows after resume');
});

test('re-running a finished import is a no-op', async () => {
  const storage = new VirtualStorage();
  await runImport(SOURCE, { storage, batchSize: BATCH_SIZE });
  const again = await runImport(SOURCE, { storage, batchSize: BATCH_SIZE });

  assert(again.batches.every((b) => b.status === 'skipped'), 'every batch skipped');
  assertEqual(again.committedRows, 0, 'nothing re-committed');
  assertEqual(again.skippedRows, 10, 'all rows accounted as skipped');
  assertEqual((await storage.allRows()).length, 10, 'row count unchanged');
});

test('partial bad rows are isolated; good rows still commit', async () => {
  const source = [
    'id,name,quantity',
    'a-1,Alpha,3',
    'a-2,,5', // empty name
    'a-3,Gamma,not-a-number', // bad quantity
    'a-4,Delta,0', // non-positive quantity
    'a-5,Epsilon,7',
    'a-1,Duplicate,9', // duplicate id
    'broken,line', // wrong field count
    'a-6,Zeta,1',
  ].join('\n');
  const storage = new VirtualStorage();

  const report = await runImport(source, { storage, batchSize: 2 });

  assertEqual(report.errors.length, 5, 'all bad rows reported');
  assertDeepEqual(
    report.errors.map((e) => e.line),
    [3, 4, 5, 7, 8],
    'errors carry source line numbers',
  );
  const rows = await storage.allRows();
  assertDeepEqual(
    rows.map((r) => r.id).sort(),
    ['a-1', 'a-5', 'a-6'],
    'only valid rows are stored',
  );
  assertEqual(report.committedRows, 3, 'committedRows counts valid rows only');
  assert(report.batches.every((b) => b.status === 'committed'), 'all batches committed');
});

run();
