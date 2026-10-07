"use strict";
/**
 * Validation of the history library through full document snapshot sequences.
 * Every step asserts the ENTIRE document (all objects, all fields), not just
 * the field touched by the last operation.
 */
const assert = require("assert");
const { History, deepEqual } = require("../dist/history.js");

const clone = (v) => JSON.parse(JSON.stringify(v));

const tests = [];
function test(name, fn) { tests.push([name, fn]); }

/** Simulated drawing editor driven by a History of full document snapshots. */
function makeEditor(initialDoc, historyOptions) {
  const history = new History(clone(initialDoc), historyOptions);
  let doc = clone(initialDoc);
  return {
    history,
    get doc() { return doc; },
    move(id, dx, dy) {
      doc.objects[id].x += dx;
      doc.objects[id].y += dy;
      history.commit(clone(doc), { label: `move:${id}`, mergeable: true });
    },
    resize(id, w) {
      doc.objects[id].w = w;
      history.commit(clone(doc), { label: `resize:${id}`, mergeable: true });
    },
    setColor(id, color) {
      doc.objects[id].color = color;
      history.commit(clone(doc), { label: `color:${id}` }); // not mergeable
    },
    setPos(id, x, y) {
      doc.objects[id].x = x;
      doc.objects[id].y = y;
      history.commit(clone(doc), { label: `setpos:${id}` }); // not mergeable
    },
    add(id, obj) {
      doc.objects[id] = obj;
      history.commit(clone(doc), { label: `add:${id}` });
    },
    undo() { const s = history.undo(); if (s !== undefined) doc = clone(s); },
    redo(branch) { const s = history.redo(branch); if (s !== undefined) doc = clone(s); },
  };
}

/** Assert the editor's live doc AND the history cursor both equal `expected`. */
function assertDoc(editor, expected) {
  assert.ok(
    deepEqual(editor.doc, expected),
    `live doc mismatch.\nexpected: ${JSON.stringify(expected)}\nactual:   ${JSON.stringify(editor.doc)}`,
  );
  assert.ok(
    deepEqual(editor.history.value, expected),
    `history snapshot mismatch.\nexpected: ${JSON.stringify(expected)}\nactual:   ${JSON.stringify(editor.history.value)}`,
  );
}

const DOC0 = {
  objects: {
    a: { x: 0, y: 0, w: 10, color: "red" },
    b: { x: 5, y: 5, w: 20, color: "blue" },
  },
};
const at = (id, x, y, extra = {}) => {
  const doc = clone(DOC0);
  Object.assign(doc.objects[id], { x, y }, extra);
  return doc;
};

// 1. Consecutive same-object moves merge into a single undo step.
test("consecutive same-label moves merge into one undo", () => {
  const ed = makeEditor(DOC0);
  ed.move("a", 1, 0);
  ed.move("a", 2, 0);
  ed.move("a", 3, 0);
  assert.strictEqual(ed.history.size, 1, "three moves collapse into one entry");
  assertDoc(ed, at("a", 6, 0));
  ed.undo();
  assertDoc(ed, DOC0);
  assert.strictEqual(ed.history.canUndo(), false);
  ed.redo();
  assertDoc(ed, at("a", 6, 0));
});

// 2. A different transaction in between prevents merging.
test("interleaved other-object operation prevents merge", () => {
  const ed = makeEditor(DOC0);
  ed.move("a", 1, 0);
  ed.move("b", 0, 1);
  ed.move("a", 1, 0);
  assert.strictEqual(ed.history.size, 3);
  ed.undo(); assertDoc(ed, at2({ a: { x: 1 }, b: { y: 6 } }));
  ed.undo(); assertDoc(ed, at("a", 1, 0));
  ed.undo(); assertDoc(ed, DOC0);
  assert.strictEqual(ed.history.canUndo(), false);
});

// helper for the doc where a moved +1 and b moved +1 in y
function at2(mods) {
  const doc = clone(DOC0);
  for (const [id, m] of Object.entries(mods)) Object.assign(doc.objects[id], m);
  return doc;
}

// 3. Same label but a non-mergeable commit in between prevents merging.
test("non-mergeable commit between same-label moves prevents merge", () => {
  const ed = makeEditor(DOC0);
  ed.move("a", 1, 0);
  ed.setColor("b", "green");
  ed.move("a", 1, 0);
  assert.strictEqual(ed.history.size, 3);
  ed.undo();
  assertDoc(ed, at2({ a: { x: 1 }, b: { color: "green" } }));
});

// 4. Undo/redo in between prevents merging (merge chain is broken by navigation).
test("undo/redo between same-label moves prevents merge", () => {
  const ed = makeEditor(DOC0);
  ed.move("a", 1, 0);
  ed.undo();
  ed.redo();
  ed.move("a", 1, 0);
  assert.strictEqual(ed.history.size, 2, "navigation broke the merge chain");
  ed.undo(); assertDoc(ed, at("a", 1, 0));
  ed.undo(); assertDoc(ed, DOC0);
});

// 5. Same object but different transaction kind (label) does not merge.
test("same object with different labels does not merge", () => {
  const ed = makeEditor(DOC0);
  ed.move("a", 1, 0);
  ed.resize("a", 42);
  assert.strictEqual(ed.history.size, 2);
  ed.undo(); assertDoc(ed, at("a", 1, 0));
  ed.undo(); assertDoc(ed, DOC0);
});

// 6. Branching after undo: old branch stays reachable.
test("undo then new commit forks a branch; both branches reachable", () => {
  const ed = makeEditor(DOC0);
  ed.setColor("a", "yellow");            // c1
  ed.setColor("b", "pink");              // c2
  ed.undo();                             // back at c1
  ed.add("c", { x: 9, y: 9, w: 1, color: "black" }); // c3 forks from c1
  const withC = at2({ a: { color: "yellow" } });
  withC.objects.c = { x: 9, y: 9, w: 1, color: "black" };
  assertDoc(ed, withC);
  ed.undo();
  assert.strictEqual(ed.history.branches(), 2);
  assert.strictEqual(ed.history.canRedo(), true);
  ed.redo();                             // default: most recent branch (c3)
  assertDoc(ed, withC);
  ed.undo();
  ed.redo(0);                            // explicit: original branch (c2)
  assertDoc(ed, at2({ a: { color: "yellow" }, b: { color: "pink" } }));
});

// 7. Capacity eviction drops the oldest undo steps on a linear history.
test("capacity eviction keeps only the newest entries", () => {
  const ed = makeEditor(DOC0, { capacity: 3 });
  for (let i = 1; i <= 5; i++) ed.setColor("a", `c${i}`);
  assert.strictEqual(ed.history.size, 3);
  assert.strictEqual(ed.history.depth, 3);
  ed.undo(); assertDoc(ed, at2({ a: { color: "c4" } }));
  ed.undo(); assertDoc(ed, at2({ a: { color: "c3" } }));
  ed.undo(); assertDoc(ed, at2({ a: { color: "c2" } }), );
  assert.strictEqual(ed.history.canUndo(), false, "oldest entries were evicted");
});

// 8. Save-point dirty marking with plain undo/redo.
test("save point: dirty toggles by content across undo/redo", () => {
  const ed = makeEditor(DOC0);
  assert.strictEqual(ed.history.isDirty(), false);
  ed.move("a", 10, 0);
  assert.strictEqual(ed.history.isDirty(), true);
  ed.history.markSaved();
  assert.strictEqual(ed.history.isDirty(), false);
  ed.undo();
  assert.strictEqual(ed.history.isDirty(), true, "content differs from saved");
  ed.redo();
  assert.strictEqual(ed.history.isDirty(), false);
});

// 9. Merging across a save point: returning to saved content is clean.
test("merge spanning a save point still detects return to saved content", () => {
  const ed = makeEditor(DOC0);           // saved = DOC0 (initial)
  ed.move("a", 10, 0);
  assert.strictEqual(ed.history.isDirty(), true);
  ed.move("a", -10, 0);                  // merges; content back to DOC0
  assert.strictEqual(ed.history.size, 1, "moves merged across the save point");
  assert.strictEqual(ed.history.isDirty(), false, "content equals saved content");
  ed.undo();
  assertDoc(ed, DOC0);
  assert.strictEqual(ed.history.isDirty(), false);
});

// 10. Save point created in the middle of a merge chain.
test("save point inside a merge chain: dirty follows content, not nodes", () => {
  const ed = makeEditor(DOC0);
  ed.move("a", 10, 0);                   // entry E = {a.x: 10}
  ed.history.markSaved();                // saved content = a@10
  ed.move("a", 5, 0);                    // merges into E -> a@15, spans save point
  assert.strictEqual(ed.history.size, 1);
  assert.strictEqual(ed.history.isDirty(), true, "a@15 != saved a@10");
  ed.move("a", -5, 0);                   // merges into E -> a@10 again
  assert.strictEqual(ed.history.size, 1);
  assert.strictEqual(ed.history.isDirty(), false, "back to saved content");
  assertDoc(ed, at("a", 10, 0));
  ed.undo();                             // merged entry undoes to DOC0
  assertDoc(ed, DOC0);
  assert.strictEqual(ed.history.isDirty(), true, "DOC0 != saved a@10");
  ed.redo();
  assert.strictEqual(ed.history.isDirty(), false);
});

// 11. Merged sequence that does NOT return to saved content stays dirty.
test("merge across save point with different end content stays dirty", () => {
  const ed = makeEditor(DOC0);
  ed.move("a", 10, 0);
  ed.move("a", 5, 0);
  assert.strictEqual(ed.history.isDirty(), true);
  ed.undo();
  assert.strictEqual(ed.history.isDirty(), false);
});

// 12. Dirty tracking survives capacity eviction of the saved node.
test("dirty stays content-based after eviction drops the saved node", () => {
  const ed = makeEditor(DOC0, { capacity: 2 }); // saved = DOC0
  ed.setColor("a", "c1");
  ed.setColor("a", "c2");
  ed.setColor("a", "c3");                // evicts DOC0's node (root promoted)
  assert.strictEqual(ed.history.isDirty(), true);
  ed.undo();
  ed.undo();                             // at c1, base of retained history
  assert.strictEqual(ed.history.canUndo(), false);
  assert.strictEqual(ed.history.isDirty(), true, "c1 != saved DOC0");
});

// 13. Eviction plus content coincidence: clean when content matches saved.
test("evicted save point still recognised by content", () => {
  const ed = makeEditor(DOC0, { capacity: 2 }); // saved = DOC0
  ed.move("a", 10, 0);                   // c1
  ed.setPos("a", 0, 0);                  // c2: content identical to DOC0
  ed.move("b", 1, 0);                    // c3 -> evicts c1, base becomes c2
  assert.strictEqual(ed.history.isDirty(), true);
  ed.undo();                             // at c2, content == DOC0 == saved
  assert.strictEqual(ed.history.isDirty(), false, "content matches evicted save point");
  ed.undo();                             // at c1, the evicted-truncated base
  assert.strictEqual(ed.history.canUndo(), false);
  assert.strictEqual(ed.history.isDirty(), true, "base c1 != saved DOC0");
});

// 14. Long mixed script validated against full document snapshots.
test("mixed script: merge + branch + save + capacity, full snapshots", () => {
  const ed = makeEditor(DOC0, { capacity: 4 });
  ed.move("a", 2, 0);                    // E1 (mergeable)
  ed.move("a", 2, 0);                    // merges into E1 -> a@4
  assert.strictEqual(ed.history.size, 1);
  ed.history.markSaved();                // saved = a@4
  const saved = at("a", 4, 0);
  ed.move("b", 0, 3);                    // E2 -> b@y8
  ed.move("a", 1, 0);                    // E3 (chain broken by E2) -> a@5
  ed.move("a", 1, 0);                    // merges into E3 -> a@6
  assert.strictEqual(ed.history.size, 3);
  assert.strictEqual(ed.history.isDirty(), true);
  ed.undo();                             // E3 undone -> a@4, b@8 == saved? b differs
  assert.strictEqual(ed.history.isDirty(), true);
  ed.undo();                             // E2 undone -> saved content
  assertDoc(ed, saved);
  assert.strictEqual(ed.history.isDirty(), false);
  ed.setColor("b", "gold");              // forks from saved node (E2 branch kept)
  const forked = at2({ a: { x: 4 }, b: { color: "gold" } });
  assertDoc(ed, forked);
  assert.strictEqual(ed.history.isDirty(), true);
  ed.undo();
  assert.strictEqual(ed.history.branches(), 2);
  ed.redo(0);                            // original branch: E2 (b moved)
  assertDoc(ed, at2({ a: { x: 4 }, b: { y: 8 } }));
  ed.redo();                             // E3 (a@6, merged)
  assertDoc(ed, at2({ a: { x: 6 }, b: { y: 8 } }));
  assert.strictEqual(ed.history.canRedo(), false);
});

// 15. Merging never swallows the base snapshot.
test("first commit never merges into the initial state", () => {
  const ed = makeEditor(DOC0);
  ed.move("a", 1, 0);
  ed.move("a", 1, 0);
  assert.strictEqual(ed.history.size, 1);
  ed.undo();
  assertDoc(ed, DOC0);
});

// 16. Commits without a label never merge, even if flagged mergeable.
test("unlabelled transactions never merge", () => {
  const h = new History(clone(DOC0));
  h.commit(clone(at("a", 1, 0)), { mergeable: true });
  h.commit(clone(at("a", 2, 0)), { mergeable: true });
  assert.strictEqual(h.size, 2);
});

let passed = 0;
let failed = 0;
for (const [name, fn] of tests) {
  try {
    fn();
    passed += 1;
    console.log(`ok   ${name}`);
  } catch (err) {
    failed += 1;
    console.error(`FAIL ${name}`);
    console.error(err && err.message ? err.message : err);
  }
}
console.log(`\n${passed} passed, ${failed} failed, ${tests.length} total`);
process.exit(failed === 0 ? 0 : 1);
