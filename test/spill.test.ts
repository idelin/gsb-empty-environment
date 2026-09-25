import assert from "node:assert/strict";
import * as fs from "node:fs/promises";
import * as os from "node:os";
import * as path from "node:path";
import { test } from "node:test";
import { buildTree } from "../src/builder.ts";
import { nodeToJson, nodeToRecords } from "../src/node.ts";
import { PathInterner } from "../src/path.ts";
import { treeToRecords } from "../src/record.ts";
import { RecordStore } from "../src/store.ts";
import { mulberry32, randomTree, recordLine, shuffled } from "./helpers.ts";

test("memory pressure spills sorted runs to disk and merges them streaming", async () => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "jts-spill-"));
  try {
    const rnd = mulberry32(7);
    const interner = new PathInterner();
    // Deterministically large tree (~4k records) with random subtrees.
    const tree = { kids: Array.from({ length: 800 }, () => randomTree(rnd, 5)) };
    const all = [...treeToRecords(tree, interner)];
    assert.ok(all.length > 3000, `expected a large tree, got ${all.length}`);
    const shuffledAll = shuffled(all, rnd);
    const store = new RecordStore({ interner, spillDir: dir, maxBuffered: 16 });
    for (const rec of shuffledAll) await store.add(rec);
    assert.ok(store.spillFiles.length > 10, "expected many spill runs");
    const { root, count } = await buildTree(store.records());
    assert.equal(count, all.length);
    const before = [...treeToRecords(tree, new PathInterner())].map(recordLine);
    const reInterner = new PathInterner();
    const after = [...nodeToRecords(root, reInterner, reInterner.root)].map(recordLine);
    assert.deepEqual(after, before);
    assert.deepEqual(nodeToJson(root), tree);
    await store.close();
    assert.deepEqual(await fs.readdir(dir), []);
  } finally {
    await fs.rm(dir, { recursive: true, force: true });
  }
});

test("spilled runs carry origins so errors can be located in files", async () => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "jts-origin-"));
  try {
    const interner = new PathInterner();
    const a = interner.internPath([{ t: "k", k: "a" }]);
    const ab = interner.internPath([{ t: "k", k: "a" }, { t: "k", k: "b" }]);
    const store = new RecordStore({ interner, spillDir: dir, maxBuffered: 2 });
    await store.addAll([
      { path: interner.root, op: "obj" },
      { path: a, op: "val", v: 1 },
      { path: ab, op: "val", v: 2 },
    ]);
    await assert.rejects(buildTree(store.records()), (err: unknown) => {
      assert.ok(err instanceof Error);
      assert.match(err.message, /run-\d+\.jsonl:\d+/);
      return true;
    });
    await store.close();
  } finally {
    await fs.rm(dir, { recursive: true, force: true });
  }
});
