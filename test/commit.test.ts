import { assert, test } from "./harness";
import { CancellationSource } from "../src/cancellation";
import { batchIdFor, commitBatches } from "../src/importer";
import {
  deferred,
  Gate,
  MemoryCheckpointStore,
  MemoryStorage,
} from "../src/memory";
import { ValidatedRow } from "../src/types";

function makeRows(count: number): ValidatedRow[] {
  const rows: ValidatedRow[] = [];
  for (let i = 0; i < count; i++) {
    rows.push({ line: i + 2, record: { name: `row-${i}` } });
  }
  return rows;
}

test("cancel before commit: zero visible writes", async () => {
  const storage = new MemoryStorage();
  const checkpoints = new MemoryCheckpointStore();
  const source = new CancellationSource();
  source.cancel();

  const result = await commitBatches(
    makeRows(10),
    { importId: "imp-1", batchSize: 3, cancellationToken: source.token },
    storage,
    checkpoints
  );

  assert.strictEqual(result.status, "cancelled");
  assert.strictEqual(result.batches.length, 0);
  assert.strictEqual(result.committedRows, 0);
  assert.strictEqual(result.remainingBatches, 4);
  assert.strictEqual(storage.attempts.length, 0, "no write attempts");
  assert.strictEqual(storage.totalRows(), 0, "no visible writes");
  assert.deepStrictEqual(
    await checkpoints.loadCommittedBatchIds("imp-1"),
    new Set(),
    "no checkpoints written"
  );
});

test("cancel during a batch: current batch returns real result, later batches never start", async () => {
  const storage = new MemoryStorage();
  const checkpoints = new MemoryCheckpointStore();
  const source = new CancellationSource();
  const gate = new Gate();
  const enteredSecondBatch = deferred<void>();

  storage.beforeWrite = async (batchId) => {
    if (batchId === batchIdFor("imp-2", 1)) {
      source.cancel(); // cancel while batch 1 is in flight
      enteredSecondBatch.resolve();
      await gate.wait(); // hold the batch mid-flight
    }
  };

  const commitPromise = commitBatches(
    makeRows(10), // 4 batches of 3
    { importId: "imp-2", batchSize: 3, cancellationToken: source.token },
    storage,
    checkpoints
  );

  await enteredSecondBatch.promise;
  let settled = false;
  commitPromise.then(() => {
    settled = true;
  });
  await new Promise((r) => setImmediate(r));
  assert.strictEqual(settled, false, "commit waits for the in-flight batch");

  gate.open();
  const result = await commitPromise;

  assert.strictEqual(result.status, "cancelled");
  assert.deepStrictEqual(
    result.batches.map((b) => b.batchId),
    [batchIdFor("imp-2", 0), batchIdFor("imp-2", 1)],
    "in-flight batch completed with a real result"
  );
  assert.deepStrictEqual(
    result.batches.map((b) => b.rowCount),
    [3, 3]
  );
  assert.strictEqual(result.committedRows, 6);
  assert.strictEqual(result.remainingBatches, 2);
  assert.deepStrictEqual(storage.committedBatchIds(), [
    batchIdFor("imp-2", 0),
    batchIdFor("imp-2", 1),
  ]);
  assert.deepStrictEqual(
    await checkpoints.loadCommittedBatchIds("imp-2"),
    new Set([batchIdFor("imp-2", 0), batchIdFor("imp-2", 1)]),
    "only finished batches are checkpointed"
  );
});

test("batch ids are stable across runs and instances", async () => {
  const run = async () => {
    const storage = new MemoryStorage();
    const checkpoints = new MemoryCheckpointStore();
    const result = await commitBatches(
      makeRows(7),
      { importId: "imp-3", batchSize: 3 },
      storage,
      checkpoints
    );
    return result.batches.map((b) => b.batchId);
  };
  const first = await run();
  const second = await run();
  assert.deepStrictEqual(first, second);
  assert.deepStrictEqual(first, [
    "imp-3/batch/000000",
    "imp-3/batch/000001",
    "imp-3/batch/000002",
  ]);
});

test("restart after full commit: all batches skipped, zero new writes", async () => {
  const storage = new MemoryStorage();
  const checkpoints = new MemoryCheckpointStore();
  const rows = makeRows(8);

  const first = await commitBatches(
    rows,
    { importId: "imp-4", batchSize: 3 },
    storage,
    checkpoints
  );
  assert.strictEqual(first.status, "committed");
  assert.strictEqual(first.batches.length, 3);

  // Simulate restart: new run, checkpoint log restored from durable snapshot.
  const restored = MemoryCheckpointStore.restore(checkpoints.snapshot());
  const attemptsBefore = storage.attempts.length;
  const second = await commitBatches(
    rows,
    { importId: "imp-4", batchSize: 3 },
    storage,
    restored
  );

  assert.strictEqual(second.status, "committed");
  assert.strictEqual(second.batches.length, 0, "no batch re-executed");
  assert.strictEqual(second.committedRows, 0);
  assert.deepStrictEqual(
    second.skippedBatchIds,
    first.batches.map((b) => b.batchId)
  );
  assert.strictEqual(storage.attempts.length, attemptsBefore, "no duplicate writes");
  assert.strictEqual(storage.totalRows(), 8, "rows written exactly once");
});

test("restart after cancellation: resumes remaining batches without duplicates", async () => {
  const storage = new MemoryStorage();
  const checkpoints = new MemoryCheckpointStore();
  const source = new CancellationSource();
  const rows = makeRows(10); // 4 batches of 3

  storage.beforeWrite = async (batchId) => {
    if (batchId === batchIdFor("imp-5", 1)) {
      source.cancel();
    }
  };
  const first = await commitBatches(
    rows,
    { importId: "imp-5", batchSize: 3, cancellationToken: source.token },
    storage,
    checkpoints
  );
  assert.strictEqual(first.status, "cancelled");
  assert.strictEqual(first.batches.length, 2);

  // Restart with a fresh (non-cancelled) token and restored checkpoints.
  const restored = MemoryCheckpointStore.restore(checkpoints.snapshot());
  const second = await commitBatches(
    rows,
    { importId: "imp-5", batchSize: 3 },
    storage,
    restored
  );

  assert.strictEqual(second.status, "committed");
  assert.deepStrictEqual(
    second.skippedBatchIds,
    [batchIdFor("imp-5", 0), batchIdFor("imp-5", 1)]
  );
  assert.deepStrictEqual(
    second.batches.map((b) => b.batchId),
    [batchIdFor("imp-5", 2), batchIdFor("imp-5", 3)]
  );
  assert.strictEqual(second.committedRows, 4);
  assert.strictEqual(storage.totalRows(), 10, "every row written exactly once");
  const attemptIds = storage.attempts.map((a) => a.batchId);
  assert.deepStrictEqual(
    attemptIds,
    [0, 1, 2, 3].map((i) => batchIdFor("imp-5", i)),
    "no batch attempted twice"
  );
});

test("cancel with empty input is a no-op commit", async () => {
  const storage = new MemoryStorage();
  const checkpoints = new MemoryCheckpointStore();
  const source = new CancellationSource();
  source.cancel();
  const result = await commitBatches(
    [],
    { importId: "imp-6", batchSize: 5, cancellationToken: source.token },
    storage,
    checkpoints
  );
  assert.strictEqual(result.status, "committed");
  assert.strictEqual(result.batches.length, 0);
  assert.strictEqual(storage.totalRows(), 0);
});
