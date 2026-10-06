import { assert, test } from "./harness";
import { parseCsv } from "../src/csv";
import { rules, validateRows } from "../src/validate";
import { commitBatches } from "../src/importer";
import { MemoryCheckpointStore, MemoryStorage } from "../src/memory";

test("end to end: parse, validate isolates bad rows, commit stores only good rows", async () => {
  const csv = [
    "name,age",
    "alice,30",
    "bad-row,not-an-int",
    "bob,41",
    ",25",
    "carol,27",
  ].join("\n");

  const { rows } = parseCsv(csv);
  const validation = validateRows(rows, {
    name: [rules.required()],
    age: [rules.required(), rules.integer()],
  });

  assert.deepStrictEqual(
    validation.accepted.map((r) => r.record.name),
    ["alice", "bob", "carol"]
  );
  assert.deepStrictEqual(
    validation.rejected.map((r) => r.line),
    [3, 5]
  );

  const storage = new MemoryStorage();
  const checkpoints = new MemoryCheckpointStore();
  const result = await commitBatches(
    validation.accepted,
    { importId: "imp-e2e", batchSize: 2 },
    storage,
    checkpoints
  );

  assert.strictEqual(result.status, "committed");
  assert.strictEqual(result.committedRows, 3);
  assert.strictEqual(storage.totalRows(), 3);
  const storedNames: string[] = [];
  for (const batchId of storage.committedBatchIds()) {
    for (const row of storage.rowsOf(batchId)) {
      storedNames.push(row.record.name);
    }
  }
  assert.deepStrictEqual(storedNames, ["alice", "bob", "carol"]);
});
