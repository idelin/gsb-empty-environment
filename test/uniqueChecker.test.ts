import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, readdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

import {
  CompositeUniqueChecker,
  RecordValidationError,
  fromChunks,
  type Schema,
} from "../src/index.ts";

const tick = () => new Promise((resolve) => setImmediate(resolve));

async function* asyncChunks<T>(chunks: T[][]): AsyncGenerator<T[]> {
  for (const chunk of chunks) {
    await tick();
    yield chunk;
  }
}

describe("CompositeUniqueChecker (in-memory)", () => {
  it("reports first and duplicate positions for composite keys", async () => {
    const records = [
      { id: 1, name: "a" },
      { id: 2, name: "b" },
      { id: 1, name: "a" },
      { id: 2, name: "c" },
      { id: 1, name: "a" },
    ];
    const report = await new CompositeUniqueChecker({ fields: ["id", "name"] }).check(records);
    assert.equal(report.totalRecords, 5);
    assert.equal(report.uniqueKeyCount, 3);
    assert.equal(report.duplicateOccurrences, 2);
    assert.equal(report.spilled, false);
    assert.deepEqual(report.duplicates, [
      { key: [1, "a"], firstIndex: 0, duplicateIndex: 2 },
      { key: [1, "a"], firstIndex: 0, duplicateIndex: 4 },
    ]);
  });

  it("detects duplicates across chunk boundaries", async () => {
    const chunks = [
      [{ id: 1 }, { id: 2 }],
      [{ id: 3 }],
      [{ id: 1 }, { id: 4 }, { id: 3 }],
    ];
    const report = await new CompositeUniqueChecker({ fields: ["id"] }).check(
      fromChunks(asyncChunks(chunks)),
    );
    assert.equal(report.totalRecords, 6);
    assert.deepEqual(report.duplicates, [
      { key: [1], firstIndex: 0, duplicateIndex: 3 },
      { key: [3], firstIndex: 2, duplicateIndex: 5 },
    ]);
  });

  it("does not confuse keys containing separators", async () => {
    const records = [
      { a: "x|y", b: "z" },
      { a: "x", b: "y|z" },
      { a: "x|y", b: "z" },
    ];
    const report = await new CompositeUniqueChecker({ fields: ["a", "b"] }).check(records);
    assert.equal(report.uniqueKeyCount, 2);
    assert.deepEqual(report.duplicates, [
      { key: ["x|y", "z"], firstIndex: 0, duplicateIndex: 2 },
    ]);
  });

  it("treats same-looking values of different types as different keys", async () => {
    const records = [{ v: 1 }, { v: "1" }, { v: true }, { v: null }, {}, { v: 1 }];
    const report = await new CompositeUniqueChecker({ fields: ["v"] }).check(records);
    assert.equal(report.uniqueKeyCount, 5);
    assert.deepEqual(report.duplicates, [{ key: [1], firstIndex: 0, duplicateIndex: 5 }]);
  });

  it("treats missing key fields as a shared undefined value", async () => {
    const records = [{ id: 1 }, { name: "x" }, { name: "y" }];
    const report = await new CompositeUniqueChecker({ fields: ["id"] }).check(records);
    assert.deepEqual(report.duplicates, [{ key: [undefined], firstIndex: 1, duplicateIndex: 2 }]);
  });

  it("nullsEqual: true merges null keys, false keeps them distinct", async () => {
    const records = [{ id: null, s: "x" }, { id: null, s: "x" }, { id: null, s: "x" }];
    const merging = await new CompositeUniqueChecker({ fields: ["id", "s"] }).check(records);
    assert.equal(merging.duplicateOccurrences, 2);

    const distinct = await new CompositeUniqueChecker({
      fields: ["id", "s"],
      nullsEqual: false,
    }).check(records);
    assert.equal(distinct.duplicateOccurrences, 0);
    assert.equal(distinct.uniqueKeyCount, 3);
  });

  it("supports JSON Pointer fields for nested keys", async () => {
    const records = [
      { user: { id: 7 }, tag: "a" },
      { user: { id: 7 }, tag: "a" },
    ];
    const report = await new CompositeUniqueChecker({ fields: ["/user/id", "tag"] }).check(records);
    assert.deepEqual(report.duplicates, [{ key: [7, "a"], firstIndex: 0, duplicateIndex: 1 }]);
  });

  it("validates records against the schema subset and throws with both paths", async () => {
    const schema: Schema = {
      type: "object",
      properties: { id: { type: "integer" } },
      required: ["id"],
    };
    const checker = new CompositeUniqueChecker({ fields: ["id"], schema });
    await assert.rejects(
      checker.check([{ id: 1 }, { id: "oops" }]),
      (err: unknown) => {
        assert.ok(err instanceof RecordValidationError);
        assert.equal(err.recordIndex, 1);
        assert.equal(err.errors[0].instancePath, "/id");
        assert.equal(err.errors[0].rulePath, "/properties/id/type");
        return true;
      },
    );
  });
});

describe("CompositeUniqueChecker (spill to disk)", () => {
  function makeRecords(count: number, keySpace: number) {
    let state = 42;
    const next = () => {
      state = (Math.imul(state, 1103515245) + 12345) & 0x7fffffff;
      return state;
    };
    const records = [];
    for (let i = 0; i < count; i++) {
      records.push({ id: next() % keySpace, grp: `g${next() % 7}` });
    }
    return records;
  }

  it("produces identical results with and without spilling", async () => {
    const records = makeRecords(2000, 150);
    const reference = await new CompositeUniqueChecker({ fields: ["id", "grp"] }).check(records);

    const spillDir = await mkdtemp(join(tmpdir(), "unique-test-spill-"));
    try {
      const spilled = await new CompositeUniqueChecker({
        fields: ["id", "grp"],
        maxInMemoryKeys: 16,
        spillPartitions: 8,
        spillDir,
      }).check(records);

      assert.equal(spilled.spilled, true);
      assert.equal(spilled.totalRecords, reference.totalRecords);
      assert.equal(spilled.uniqueKeyCount, reference.uniqueKeyCount);
      assert.equal(spilled.duplicateOccurrences, reference.duplicateOccurrences);
      assert.deepEqual(spilled.duplicates, reference.duplicates);

      // spill files are cleaned up by default
      assert.deepEqual(await readdir(spillDir), []);
    } finally {
      await rm(spillDir, { recursive: true, force: true });
    }
  });

  it("spills even when duplicates were found before the memory limit", async () => {
    const records = [
      { id: 1 }, { id: 1 }, // duplicate found while still in memory
      { id: 2 }, { id: 3 }, { id: 4 }, // pushes distinct keys past the limit
      { id: 1 }, { id: 2 }, // duplicates found after spilling started
    ];
    const report = await new CompositeUniqueChecker({
      fields: ["id"],
      maxInMemoryKeys: 2,
      spillPartitions: 4,
    }).check(records);
    assert.equal(report.spilled, true);
    assert.deepEqual(report.duplicates, [
      { key: [1], firstIndex: 0, duplicateIndex: 1 },
      { key: [1], firstIndex: 0, duplicateIndex: 5 },
      { key: [2], firstIndex: 2, duplicateIndex: 6 },
    ]);
  });

  it("respects nullsEqual: false in spill mode", async () => {
    const records = Array.from({ length: 50 }, () => ({ id: null }));
    const report = await new CompositeUniqueChecker({
      fields: ["id"],
      nullsEqual: false,
      maxInMemoryKeys: 4,
      spillPartitions: 4,
    }).check(records);
    assert.equal(report.spilled, true);
    assert.equal(report.duplicateOccurrences, 0);
    assert.equal(report.uniqueKeyCount, 50);
  });

  it("keeps spill files when keepSpillFiles is true", async () => {
    const spillDir = await mkdtemp(join(tmpdir(), "unique-test-keep-"));
    try {
      const report = await new CompositeUniqueChecker({
        fields: ["id"],
        maxInMemoryKeys: 1,
        spillPartitions: 3,
        spillDir,
        keepSpillFiles: true,
      }).check([{ id: 1 }, { id: 2 }, { id: 1 }]);
      assert.equal(report.spilled, true);
      assert.equal(report.spillDirectory, spillDir);
      const files = await readdir(spillDir);
      assert.deepEqual(files.sort(), ["partition-0.jsonl", "partition-1.jsonl", "partition-2.jsonl"]);
    } finally {
      await rm(spillDir, { recursive: true, force: true });
    }
  });
});
