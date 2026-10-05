import test from "node:test";
import assert from "node:assert/strict";
import {
  DocumentEditError,
  IncrementalQuery,
  VersionedDocument,
  encodePath,
  type JsonValue,
} from "../src/index.ts";

test("result diffs track set and delete edits", () => {
  const doc = new VersionedDocument({ a: { b: [1, 2, 3] } } as JsonValue);
  const inc = new IncrementalQuery(doc, "$.a.b[*]");

  const initial = inc.sync();
  assert.equal(initial.version, 0);
  assert.deepEqual(initial.added.map((r) => r.value), [1, 2, 3]);

  doc.applyEdits([{ type: "set", path: ["a", "b", 1], value: 20 }]);
  const changed = inc.sync();
  assert.equal(changed.version, 1);
  assert.equal(changed.changed.length, 1);
  assert.deepEqual(encodePath(changed.changed[0].path), "/a/b/1");
  assert.equal(changed.changed[0].oldValue, 2);
  assert.equal(changed.changed[0].newValue, 20);
  assert.equal(changed.added.length + changed.removed.length, 0);

  doc.applyEdits([{ type: "delete", path: ["a", "b", 0] }]);
  const shifted = inc.sync();
  // path-based diff: surviving paths report value changes, the tail is removed
  assert.deepEqual(shifted.removed.map((r) => encodePath(r.path)), ["/a/b/2"]);
  assert.equal(shifted.added.length, 0);
  assert.deepEqual(
    shifted.changed.map((c) => `${encodePath(c.path)}:${JSON.stringify(c.oldValue)}->${JSON.stringify(c.newValue)}`),
    ["/a/b/0:1->20", "/a/b/1:20->3"],
  );

  const noop = inc.sync();
  assert.equal(noop.added.length + noop.removed.length + noop.changed.length, 0);
});

test("cursors stay bound to their document version", () => {
  const doc = new VersionedDocument({ list: ["a", "b", "c"] } as JsonValue);
  const inc = new IncrementalQuery(doc, "$.list[*]");
  inc.sync();

  const oldCursor = inc.cursor(["list", 0]);
  assert.equal(oldCursor.version, 0);

  doc.applyEdits([{ type: "delete", path: ["list", 0] }]);
  inc.sync();

  // Old cursor still resolves against version 0: index 0 is still "a".
  const oldResolution = inc.resolve(oldCursor);
  assert.ok(oldResolution.ok);
  assert.equal(oldResolution.value, "a");

  // A fresh cursor on the same path sees the shifted array.
  const newCursor = inc.cursor(["list", 0]);
  const newResolution = inc.resolve(newCursor);
  assert.ok(newResolution.ok);
  assert.equal(newResolution.value, "b");

  // Deleting the target makes only the new-version cursor unresolvable.
  doc.applyEdits([{ type: "delete", path: ["list", 0] }, { type: "delete", path: ["list", 0] }]);
  assert.equal(inc.resolve(inc.cursor(["list", 0])).ok, false);
  assert.equal(inc.resolve(oldCursor).ok, true);
});

test("set creates keys, delete removes them, diffs follow", () => {
  const doc = new VersionedDocument({ user: { name: "ada" } } as JsonValue);
  const inc = new IncrementalQuery(doc, "$.user.*");
  inc.sync();

  doc.applyEdits([{ type: "set", path: ["user", "email"], value: "a@x.dev" }]);
  const added = inc.sync();
  assert.deepEqual(added.added.map((r) => encodePath(r.path)), ["/user/email"]);

  doc.applyEdits([{ type: "delete", path: ["user", "email"] }]);
  const removed = inc.sync();
  assert.deepEqual(removed.removed.map((r) => encodePath(r.path)), ["/user/email"]);
});

test("edit errors locate the failing path", () => {
  const doc = new VersionedDocument({ a: { b: [1] } } as JsonValue);
  assert.throws(
    () => doc.applyEdits([{ type: "set", path: ["a", "x", "y"], value: 1 }]),
    (e: unknown) => {
      assert.ok(e instanceof DocumentEditError);
      assert.deepEqual(e.path, ["a", "x"]);
      assert.match(e.message, /\$\.a\.x/);
      return true;
    },
  );
  assert.throws(
    () => doc.applyEdits([{ type: "delete", path: ["a", "b", 5] }]),
    (e: unknown) => e instanceof DocumentEditError && e.path.length === 3,
  );
  assert.throws(
    () => doc.applyEdits([{ type: "set", path: ["a", "b", "0"], value: 1 }]),
    DocumentEditError,
  );
});

test("pruned versions make cursors unresolvable", () => {
  const doc = new VersionedDocument({ v: 1 } as JsonValue);
  const inc = new IncrementalQuery(doc, "$.v");
  inc.sync();
  const cursor = inc.cursor(["v"]);
  doc.applyEdits([{ type: "set", path: ["v"], value: 2 }]);
  doc.prune(1);
  assert.equal(inc.resolve(cursor).ok, false);
});
