import assert from "node:assert/strict";
import { test } from "./harness.mjs";
import { Rope, RopeArena, LEAF_MAX_SCALARS } from "../dist/rope.js";
import { InvalidEncodingError } from "../dist/utf8.js";

const scalarsOf = (s) => Array.from(s);

test("rope: round-trip and metrics", () => {
  const arena = new RopeArena();
  const samples = ["", "hello", "😀".repeat(100), "aé中😀".repeat(50), "x"];
  for (const s of samples) {
    const rope = Rope.from(arena, s);
    assert.equal(rope.toString(), s);
    assert.equal(rope.length, scalarsOf(s).length);
    assert.equal(rope.byteLength, Buffer.byteLength(s, "utf8"));
    rope.validate();
    rope.dispose();
  }
  assert.equal(arena.live, 0);
});

test("rope: long text becomes a multi-leaf tree", () => {
  const arena = new RopeArena();
  const s = "abcdefghij😀".repeat(20); // 220 scalars > LEAF_MAX_SCALARS
  const rope = Rope.from(arena, s);
  assert.ok(rope.nodeCount > 2, "expected several nodes, got " + rope.nodeCount);
  rope.validate();
  rope.dispose();
});

test("rope: insert at boundaries", () => {
  const arena = new RopeArena();
  const base = Rope.from(arena, "中😀e");
  assert.equal(base.insert(0, "x").toString(), "x中😀e");
  assert.equal(base.insert(3, "x").toString(), "中😀ex");
  assert.equal(base.insert(1, "🧡").toString(), "中🧡😀e");
  assert.equal(base.insert(2, "").toString(), "中😀e");
  base.dispose();
});

test("rope: delete ranges, including cross-leaf", () => {
  const arena = new RopeArena();
  const unit = "ab😀中"; // 4 scalars, 8 bytes
  const s = unit.repeat(30); // 120 scalars, many leaves
  const rope = Rope.from(arena, s);
  const model = scalarsOf(s);
  const result = rope.delete(10, 110);
  assert.equal(result.toString(), model.slice(0, 10).join("") + model.slice(110).join(""));
  assert.equal(result.length, 20);
  result.validate();
  const all = rope.delete(0, 120);
  assert.equal(all.toString(), "");
  assert.equal(all.length, 0);
  assert.equal(all.byteLength, 0);
  const none = rope.delete(5, 5);
  assert.equal(none.toString(), s);
  rope.dispose();
});

test("rope: 4-byte characters are never split at any offset", () => {
  const arena = new RopeArena();
  const s = "😀".repeat(3 * LEAF_MAX_SCALARS + 5); // spans several leaves
  const rope = Rope.from(arena, s);
  const model = scalarsOf(s);
  for (let k = 0; k <= model.length; k++) {
    const ins = rope.insert(k, "🧡");
    assert.equal(ins.toString(), model.slice(0, k).join("") + "🧡" + model.slice(k).join(""));
    ins.dispose();
    if (k < model.length) {
      const del = rope.delete(k, k + 1);
      assert.equal(del.toString(), model.slice(0, k).join("") + model.slice(k + 1).join(""));
      del.validate();
      del.dispose();
    }
  }
  rope.dispose();
  assert.equal(arena.live, 0);
});

test("rope: empty text operations", () => {
  const arena = new RopeArena();
  const empty = Rope.empty(arena);
  assert.equal(empty.toString(), "");
  assert.equal(empty.length, 0);
  assert.equal(empty.byteLength, 0);
  const r1 = empty.insert(0, "");
  assert.equal(r1.toString(), "");
  const r2 = empty.insert(0, "a");
  assert.equal(r2.toString(), "a");
  const fromEmpty = Rope.from(arena, "");
  assert.equal(fromEmpty.length, 0);
  const a = Rope.from(arena, "a");
  const r3 = a.delete(0, 1);
  assert.equal(r3.toString(), "");
  const r4 = a.delete(0, 0);
  assert.equal(r4.toString(), "a");
  for (const r of [empty, r1, r2, fromEmpty, a, r3, r4]) r.dispose();
  assert.equal(arena.live, 0);
});

test("rope: invalid encoding rejects the whole edit atomically", () => {
  const arena = new RopeArena();
  const rope = Rope.from(arena, "hello 😀");
  const liveBefore = arena.live;
  const countBefore = rope.nodeCount;
  assert.throws(() => rope.insert(1, "\uD800"), InvalidEncodingError);
  assert.throws(() => rope.insert(0, "ok\uDC00nope"), InvalidEncodingError);
  assert.throws(() => Rope.from(arena, "x\uDC00"), InvalidEncodingError);
  assert.equal(rope.toString(), "hello 😀");
  assert.equal(rope.nodeCount, countBefore);
  assert.equal(arena.live, liveBefore, "rejected edit must not allocate nodes");
  rope.dispose();
});

test("rope: offset range errors", () => {
  const arena = new RopeArena();
  const rope = Rope.from(arena, "abc");
  assert.throws(() => rope.insert(-1, "x"), RangeError);
  assert.throws(() => rope.insert(4, "x"), RangeError);
  assert.throws(() => rope.insert(1.5, "x"), RangeError);
  assert.throws(() => rope.delete(-1, 2), RangeError);
  assert.throws(() => rope.delete(2, 1), RangeError);
  assert.throws(() => rope.delete(0, 4), RangeError);
  rope.dispose();
});

test("rope: persistence — old snapshots unchanged and share unchanged nodes", () => {
  const arena = new RopeArena();
  const base = Rope.from(arena, "x😀中".repeat(40)); // 120 scalars
  const baseText = base.toString();
  const liveBefore = arena.live;
  const edited = base.insert(80, "INSERTED");
  assert.equal(base.toString(), baseText, "old snapshot must be immutable");
  assert.equal(base.length, 120);
  assert.equal(edited.length, 128);
  const newNodes = arena.live - liveBefore;
  assert.ok(
    newNodes < base.nodeCount,
    "structural sharing: " + newNodes + " new nodes vs " + base.nodeCount + " total"
  );
  edited.dispose();
  assert.equal(arena.live, liveBefore, "disposing the edit reclaims its private nodes");
  base.dispose();
  assert.equal(arena.live, 0);
});
