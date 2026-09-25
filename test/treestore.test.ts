import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import * as fs from "node:fs/promises";
import * as os from "node:os";
import * as path from "node:path";
import { test } from "node:test";
import { PathError } from "../src/errors.ts";
import { nodeToJson } from "../src/node.ts";
import { treeToRecords } from "../src/record.ts";
import { TreeStore } from "../src/treestore.ts";
import { k, recordLine } from "./helpers.ts";

test("subtree move keeps old cursors on their version and yields an inverse patch", async () => {
  const store = await TreeStore.open();
  const original = { a: { x: 1, y: [10, 20] }, b: {}, s: "scalar" };
  const v1 = await store.commit(treeToRecords(original, store.interner));

  // Prefix cursor created before the move, pinned to v1.
  const cursorA = store.cursor(k("a"), v1);

  const { version: v2, inverse } = await store.move(k("a", "x"), k("b", "y"));
  assert.equal(v2.id, v1.id + 1);

  // The pre-move cursor still reads the old version.
  const oldLines = [...cursorA.records()].map(recordLine);
  assert.deepEqual(oldLines, ["$.a obj", "$.a.x val:1", "$.a.y arr:2", "$.a.y[0] val:10", "$.a.y[1] val:20"]);

  // The new version reflects the move.
  assert.deepEqual(nodeToJson(store.current!.root), { a: { y: [10, 20] }, b: { y: 1 }, s: "scalar" });
  const moved = store.cursor(k("b", "y"));
  assert.deepEqual([...moved.records()].map(recordLine), ["$.b.y val:1"]);
  assert.equal(store.cursor(k("a", "x")).node(), undefined);

  // The inverse patch restores the original tree exactly.
  await store.applyPatch(inverse);
  assert.deepEqual(nodeToJson(store.current!.root), original);
});

test("a failed move does not modify the published tree", async () => {
  const store = await TreeStore.open();
  await store.commit(treeToRecords({ a: { x: 1 }, s: "scalar" }, store.interner));
  const before = store.current;

  await assert.rejects(store.move(k("a", "x"), k("s", "y")), (err: unknown) => {
    assert.ok(err instanceof PathError);
    assert.equal(err.kind, "ParentTypeConflict");
    assert.match(err.message, /\$\.s\.y/);
    return true;
  });
  await assert.rejects(store.move(k("a"), k("a", "deeper")), (err: unknown) => {
    return err instanceof PathError && err.kind === "MoveIntoSelf";
  });
  await assert.rejects(store.move(k("missing"), k("b")), (err: unknown) => {
    return err instanceof PathError && err.kind === "NotFound";
  });
  await assert.rejects(store.move(k("a", "x"), k("a", "x")), (err: unknown) => {
    return err instanceof PathError && err.kind === "MoveIntoSelf";
  });
  await assert.rejects(store.move(k("a", "x"), k("s")), (err: unknown) => {
    return err instanceof PathError && err.kind === "TargetExists";
  });

  assert.equal(store.current, before); // same published version object
  assert.deepEqual(nodeToJson(store.current!.root), { a: { x: 1 }, s: "scalar" });
});

test("move into an array appends via a header update and keeps siblings", async () => {
  const store = await TreeStore.open();
  await store.commit(treeToRecords({ list: ["a", "b"], item: "c" }, store.interner));
  const { inverse } = await store.move(k("item"), [{ t: "k", k: "list" }, { t: "i", i: 2 }]);
  assert.deepEqual(nodeToJson(store.current!.root), { list: ["a", "b", "c"] });
  await store.applyPatch(inverse);
  assert.deepEqual(nodeToJson(store.current!.root), { list: ["a", "b"], item: "c" });
});

test("del patches create missing keys and array holes; inverse restores them", async () => {
  const store = await TreeStore.open();
  await store.commit(treeToRecords({ a: { b: 1, c: 2 }, arr: [1, 2, 3] }, store.interner));
  const { inverse } = await store.applyPatch([
    { path: store.interner.internPath(k("a", "b")), op: "del" },
    { path: store.interner.internPath([{ t: "k", k: "arr" }, { t: "i", i: 1 }]), op: "del" },
  ]);
  const json = nodeToJson(store.current!.root) as { a: object; arr: number[] };
  assert.deepEqual(json.a, { c: 2 });
  assert.equal(json.arr.length, 3);
  assert.equal(1 in json.arr, false); // deletion leaves a hole, not a shift
  await store.applyPatch(inverse);
  assert.deepEqual(nodeToJson(store.current!.root), { a: { b: 1, c: 2 }, arr: [1, 2, 3] });
});

test("prefix query returns exactly the subtree records", async () => {
  const store = await TreeStore.open();
  await store.commit(
    treeToRecords({ a: { b: { c: 1 }, d: 2 }, e: 3 }, store.interner),
  );
  const cursor = store.cursor(k("a", "b"));
  assert.deepEqual([...cursor.records()].map(recordLine), ["$.a.b obj", "$.a.b.c val:1"]);
  assert.equal(store.cursor(k("a", "nope")).node(), undefined);
});

test("crash after index write but before HEAD publish recovers the old version", async () => {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), "jts-crash-"));
  try {
    const store = await TreeStore.open({ dir });
    await store.commit(treeToRecords({ v: 1 }, store.interner));
    await store.commit(treeToRecords({ v: 2 }, store.interner), { crashBeforePublish: true });

    // The interrupted commit never became visible, even in memory.
    assert.deepEqual(nodeToJson(store.current!.root), { v: 1 });
    assert.ok(existsSync(path.join(dir, "index-2.jsonl"))); // written...
    assert.equal(await fs.readFile(path.join(dir, "HEAD"), "utf8"), "1"); // ...but never published

    // Recovery ignores and removes the orphan index: no half tree exposed.
    const recovered = await TreeStore.open({ dir });
    assert.equal(recovered.current!.id, 1);
    assert.deepEqual(nodeToJson(recovered.current!.root), { v: 1 });
    assert.ok(!existsSync(path.join(dir, "index-2.jsonl")));

    // The store keeps working after recovery.
    await recovered.commit(treeToRecords({ v: 3 }, recovered.interner));
    assert.deepEqual(nodeToJson(recovered.current!.root), { v: 3 });
    const again = await TreeStore.open({ dir });
    assert.deepEqual(nodeToJson(again.current!.root), { v: 3 });
  } finally {
    await fs.rm(dir, { recursive: true, force: true });
  }
});
