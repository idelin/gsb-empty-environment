import assert from "node:assert/strict";
import { test } from "node:test";
import { buildTree } from "../src/builder.ts";
import { PathError } from "../src/errors.ts";
import { nodeToJson, nodeToRecords } from "../src/node.ts";
import { PathInterner } from "../src/path.ts";
import { treeToRecords, type PathRecord } from "../src/record.ts";
import { RecordStore } from "../src/store.ts";
import { mulberry32, randomTree, recordLine, shuffled } from "./helpers.ts";

function sourced(records: PathRecord[]) {
  return records.map((rec, i) => ({ rec, origin: `input#${i + 1}` }));
}

async function* toStream(records: PathRecord[]) {
  for (const item of sourced(records)) yield item;
}

test("scalar parent followed by a child path is a located conflict", async () => {
  const interner = new PathInterner();
  const a = interner.internPath([{ t: "k", k: "a" }]);
  const ab = interner.internPath([{ t: "k", k: "a" }, { t: "k", k: "b" }]);
  const records: PathRecord[] = [
    { path: interner.root, op: "obj" },
    { path: a, op: "val", v: 1 },
    { path: ab, op: "val", v: 2 },
  ];
  await assert.rejects(buildTree(toStream(records)), (err: unknown) => {
    assert.ok(err instanceof PathError);
    assert.equal(err.kind, "ParentTypeConflict");
    assert.deepEqual(err.path, [{ t: "k", k: "a" }, { t: "k", k: "b" }]);
    assert.match(err.message, /\$\.a\.b/);
    assert.equal(err.source, "input#3");
    return true;
  });
});

test("an index segment under an object parent cannot turn it into an array", async () => {
  const interner = new PathInterner();
  const idx0 = interner.internPath([{ t: "i", i: 0 }]);
  const records: PathRecord[] = [
    { path: interner.root, op: "obj" },
    { path: idx0, op: "val", v: "x" },
  ];
  await assert.rejects(buildTree(toStream(records)), (err: unknown) => {
    assert.ok(err instanceof PathError);
    assert.equal(err.kind, "SegmentTypeMismatch");
    assert.deepEqual(err.path, [{ t: "i", i: 0 }]);
    return true;
  });
});

test("conflicting duplicates fail; identical duplicates are merged", async () => {
  const interner = new PathInterner();
  const a = interner.internPath([{ t: "k", k: "a" }]);
  const dup: PathRecord[] = [
    { path: interner.root, op: "obj" },
    { path: a, op: "val", v: 1 },
    { path: a, op: "val", v: 1 },
    { path: a, op: "val", v: 1 },
  ];
  const ok = await buildTree(toStream(dup));
  assert.equal(ok.count, 2);

  const conflict: PathRecord[] = [
    { path: interner.root, op: "obj" },
    { path: a, op: "val", v: 1 },
    { path: a, op: "val", v: 2 },
  ];
  await assert.rejects(buildTree(toStream(conflict)), (err: unknown) => {
    assert.ok(err instanceof PathError);
    assert.equal(err.kind, "DuplicateDefinition");
    assert.deepEqual(err.path, [{ t: "k", k: "a" }]);
    assert.equal(err.source, "input#3");
    return true;
  });
});

test("missing parent breaks reference integrity with a located error", async () => {
  const interner = new PathInterner();
  const ab = interner.internPath([{ t: "k", k: "a" }, { t: "k", k: "b" }]);
  const records: PathRecord[] = [{ path: interner.root, op: "obj" }, { path: ab, op: "val", v: 1 }];
  await assert.rejects(buildTree(toStream(records)), (err: unknown) => {
    assert.ok(err instanceof PathError);
    assert.equal(err.kind, "MissingParent");
    assert.deepEqual(err.path, [{ t: "k", k: "a" }, { t: "k", k: "b" }]);
    return true;
  });
});

test("array index beyond the declared length is rejected", async () => {
  const interner = new PathInterner();
  const a = interner.internPath([{ t: "k", k: "a" }]);
  const a2 = interner.internPath([{ t: "k", k: "a" }, { t: "i", i: 2 }]);
  const records: PathRecord[] = [
    { path: interner.root, op: "obj" },
    { path: a, op: "arr", n: 2 },
    { path: a2, op: "val", v: 1 },
  ];
  await assert.rejects(buildTree(toStream(records)), (err: unknown) => {
    assert.ok(err instanceof PathError);
    assert.equal(err.kind, "IndexOutOfRange");
    return true;
  });
});

test("arbitrary chunking round-trips node-by-node equivalent trees", async (t) => {
  for (const seed of [1, 2, 3, 4]) {
    await t.test(`seed ${seed}`, async () => {
      const rnd = mulberry32(seed);
      const tree = randomTree(rnd, 6);
      const interner = new PathInterner();
      const all = shuffled([...treeToRecords(tree, interner)], rnd);
      const store = new RecordStore({ interner, maxBuffered: 1 + Math.floor(rnd() * 16) });
      let i = 0;
      while (i < all.length) {
        const size = 1 + Math.floor(rnd() * 13);
        await store.addAll(all.slice(i, i + size));
        i += size;
      }
      const { root } = await buildTree(store.records());
      const before = [...treeToRecords(tree, new PathInterner())].map(recordLine);
      const afterInterner = new PathInterner();
      const after = [...nodeToRecords(root, afterInterner, afterInterner.root)].map(recordLine);
      assert.deepEqual(after, before);
      assert.deepEqual(nodeToJson(root), tree);
    });
  }
});

test("depth 100k is built iteratively without recursion", async () => {
  const DEPTH = 100_000;
  const interner = new PathInterner();
  const store = new RecordStore({ interner });
  const rnd = mulberry32(42);
  let node = interner.root;
  let pending: PathRecord[] = [{ path: node, op: "obj" }];
  const flush = async () => {
    await store.addAll(pending);
    pending = [];
  };
  for (let d = 0; d < DEPTH; d++) {
    node = interner.intern(node, { t: "k", k: `n${d}` });
    pending.push(
      d === DEPTH - 1
        ? { path: node, op: "val", v: "bottom" }
        : { path: node, op: "obj" },
    );
    if (pending.length >= 1 + Math.floor(rnd() * 64)) await flush();
  }
  await flush();

  const { root, count } = await buildTree(store.records());
  assert.equal(count, DEPTH + 1);

  // Iterative descent to the bottom.
  let cur = root;
  let depth = 0;
  while (cur.kind === "obj") {
    cur = cur.children.get(`n${depth}`) as typeof cur;
    depth++;
  }
  assert.equal(depth, DEPTH);
  assert.deepEqual(cur, { kind: "val", v: "bottom" });

  // Iterative re-emission of the deep tree as records.
  const reInterner = new PathInterner();
  let emitted = 0;
  for (const _ of nodeToRecords(root, reInterner, reInterner.root)) emitted++;
  assert.equal(emitted, DEPTH + 1);
});
