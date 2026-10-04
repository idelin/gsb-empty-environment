import assert from "node:assert/strict";
import { test } from "./harness.mjs";
import { TextEditor } from "../dist/history.js";

// Deterministic PRNG so failures are reproducible.
function mulberry32(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// 1-byte, 2-byte, 3-byte, 4-byte scalars, a ZWJ sequence, and "".
const POOL = ["a", "b", "Z", "0", " ", "\n", "é", "ß", "中", "文", "𐍈", "😀", "🧡", "👩‍💻", ""];

function randomText(rnd) {
  const k = Math.floor(rnd() * 9); // 0..8 pieces; 0 yields the empty string
  let s = "";
  for (let i = 0; i < k; i++) s += POOL[Math.floor(rnd() * POOL.length)];
  return s;
}

test("model: randomized fuzz against an independent scalar-array model", () => {
  for (const seed of [1, 7, 42, 1337, 20261004]) {
    const rnd = mulberry32(seed);
    const editor = new TextEditor("");
    let model = []; // independent model: array of scalars
    const models = new Map(); // snapshot id -> model state
    models.set(editor.currentSnapshotId, []);
    const randInt = (n) => Math.floor(rnd() * n);

    for (let step = 0; step < 1500; step++) {
      const r = rnd();
      if (r < 0.4) {
        const offset = randInt(model.length + 1);
        const text = randomText(rnd);
        const id = editor.insert(offset, text);
        model = model.slice(0, offset).concat(Array.from(text), model.slice(offset));
        models.set(id, model);
      } else if (r < 0.75) {
        const a = randInt(model.length + 1);
        const b = randInt(model.length + 1);
        const s = Math.min(a, b);
        const t = Math.max(a, b);
        const id = editor.delete(s, t);
        model = model.slice(0, s).concat(model.slice(t));
        models.set(id, model);
      } else if (r < 0.85) {
        if (editor.undo()) model = models.get(editor.currentSnapshotId);
      } else if (r < 0.93) {
        if (editor.redo()) model = models.get(editor.currentSnapshotId);
      } else if (r < 0.97) {
        const ids = Array.from(models.keys());
        const id = ids[randInt(ids.length)];
        if (editor.checkout(id)) model = models.get(id);
      } else {
        const ids = Array.from(models.keys()).filter((id) => id !== editor.currentSnapshotId);
        if (ids.length > 0) {
          const id = ids[randInt(ids.length)];
          if (editor.releaseSnapshot(id)) models.delete(id);
        }
      }
      const expected = model.join("");
      const where = "seed=" + seed + " step=" + step;
      assert.equal(editor.text, expected, where + " text");
      assert.equal(editor.length, model.length, where + " scalar length");
      assert.equal(editor.byteLength, Buffer.byteLength(expected, "utf8"), where + " byte length");
      if (step % 100 === 0) editor.validate();
    }

    // Releasing every snapshot except the current one must reclaim all
    // nodes that are no longer shared.
    for (const id of Array.from(models.keys())) {
      if (id !== editor.currentSnapshotId) {
        assert.equal(editor.releaseSnapshot(id), true, "release " + id);
      }
    }
    assert.equal(
      editor.arena.live,
      editor.nodeCount,
      "seed=" + seed + ": only the surviving snapshot's nodes stay allocated"
    );
    editor.validate();
  }
});

test("model: cross-leaf delete with 4-byte characters on leaf boundaries", () => {
  const editor = new TextEditor("😀".repeat(101)); // 4-byte scalars across many leaves
  let model = Array.from(editor.text);
  editor.delete(31, 66); // straddles the 32-scalar leaf boundary
  model = model.slice(0, 31).concat(model.slice(66));
  assert.equal(editor.text, model.join(""));
  assert.equal(editor.length, 66);
  assert.equal(editor.byteLength, 66 * 4);
  editor.delete(0, 66); // delete everything that remains
  assert.equal(editor.text, "");
  assert.equal(editor.length, 0);
  editor.validate();
});

test("model: empty-string edits and delete-all then rebuild", () => {
  const editor = new TextEditor("a😀中".repeat(50)); // 150 scalars
  editor.insert(10, ""); // empty insert still commits a snapshot
  assert.equal(editor.length, 150);
  editor.delete(3, 3); // empty delete
  assert.equal(editor.length, 150);
  editor.delete(0, 150); // delete all
  assert.equal(editor.text, "");
  assert.equal(editor.length, 0);
  assert.equal(editor.byteLength, 0);
  editor.insert(0, "🧡");
  assert.equal(editor.text, "🧡");
  assert.equal(editor.undo(), true);
  assert.equal(editor.text, "");
  assert.equal(editor.undo(), true);
  assert.equal(editor.length, 150);
  editor.validate();
});
