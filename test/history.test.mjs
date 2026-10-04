import assert from "node:assert/strict";
import { test } from "./harness.mjs";
import { TextEditor } from "../dist/history.js";
import { InvalidEncodingError } from "../dist/utf8.js";

test("history: linear undo/redo", () => {
  const e = new TextEditor("");
  e.insert(0, "hello");
  e.insert(5, " 世界");
  assert.equal(e.text, "hello 世界");
  assert.equal(e.undo(), true);
  assert.equal(e.text, "hello");
  assert.equal(e.undo(), true);
  assert.equal(e.text, "");
  assert.equal(e.undo(), false);
  assert.equal(e.redo(), true);
  assert.equal(e.text, "hello");
  assert.equal(e.redo(), true);
  assert.equal(e.text, "hello 世界");
  assert.equal(e.redo(), false);
});

test("history: edit after undo creates a branch, redo stack is preserved", () => {
  const e = new TextEditor("");
  const idA = e.insert(0, "A"); // "A"
  const idB = e.insert(1, "B"); // "AB"
  assert.equal(e.undo(), true); // back to "A"
  const idC = e.insert(1, "C"); // "AC" — branch off idA
  assert.deepEqual(e.childrenOf(idA), [idB, idC]);
  assert.equal(e.text, "AC");
  assert.equal(e.undo(), true); // idA
  assert.equal(e.redo(), true); // most recent branch -> idC
  assert.equal(e.text, "AC");
  assert.equal(e.undo(), true);
  assert.equal(e.checkout(idB), true); // the other branch still exists
  assert.equal(e.text, "AB");
  assert.equal(e.checkout(idA), true);
  assert.equal(e.text, "A");
});

test("history: invalid encoding rejects the whole edit, history untouched", () => {
  const e = new TextEditor("ab😀");
  const snapsBefore = e.snapshotCount();
  const liveBefore = e.arena.live;
  const currentBefore = e.currentSnapshotId;
  assert.throws(() => e.insert(1, "\uD800"), InvalidEncodingError);
  assert.throws(() => e.insert(0, "😀".slice(0, 1)), InvalidEncodingError);
  assert.throws(() => new TextEditor("\uDC00"), InvalidEncodingError);
  assert.equal(e.text, "ab😀");
  assert.equal(e.snapshotCount(), snapsBefore);
  assert.equal(e.currentSnapshotId, currentBefore);
  assert.equal(e.arena.live, liveBefore);
});

test("history: releaseSnapshot detaches, reparents children, reclaims nodes", () => {
  const e = new TextEditor("");
  const id1 = e.insert(0, "😀".repeat(64));
  const id2 = e.insert(0, "中".repeat(64));
  const id3 = e.delete(0, 128); // empty text
  assert.equal(e.releaseSnapshot(e.currentSnapshotId), false, "cannot release current");
  assert.equal(e.releaseSnapshot(999), false, "unknown id");
  assert.equal(e.checkout(id2), true);
  const keepNodes = e.nodeCount;
  assert.equal(e.releaseSnapshot(0), true);
  assert.equal(e.has(0), false);
  assert.equal(e.releaseSnapshot(id1), true);
  assert.equal(e.releaseSnapshot(id3), true);
  assert.equal(e.arena.live, keepNodes, "only nodes of surviving snapshots remain");
  assert.equal(e.text, "中".repeat(64) + "😀".repeat(64));
  // id3 was a child of id2; after release it is gone but id2 is intact
  assert.equal(e.has(id3), false);
  assert.equal(e.checkout(id2), true);
});

test("history: byte and scalar lengths tracked through edits", () => {
  const e = new TextEditor("中"); // 1 scalar, 3 bytes
  assert.equal(e.length, 1);
  assert.equal(e.byteLength, 3);
  e.insert(1, "😀"); // 1 scalar, 4 bytes
  assert.equal(e.length, 2);
  assert.equal(e.byteLength, 7);
  e.delete(0, 1);
  assert.equal(e.length, 1);
  assert.equal(e.byteLength, 4);
  e.validate();
});
