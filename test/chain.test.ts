import { test } from "node:test";
import assert from "node:assert/strict";
import { EditChain, VersionedDocument } from "../src/chain.ts";
import { DEFAULT_COMPLETION_AFFINITY, edit, range } from "../src/types.ts";

test("versions tick once per recorded edit", () => {
  const doc = new VersionedDocument("hello");
  assert.equal(doc.version, 0);
  doc.insert(5, " world");
  assert.equal(doc.version, 1);
  doc.delete(0, 1);
  assert.equal(doc.version, 2);
  assert.equal(doc.getText(), "ello world");
});

test("range from an old version maps through a chain of later edits", () => {
  const doc = new VersionedDocument("foo");
  // completion requested at v0 over "foo" = [0, 3)
  doc.insert(3, "b"); // v1: "foob"
  doc.insert(4, "a"); // v2: "fooba"
  doc.insert(5, "r"); // v3: "foobar"
  const mapped = doc.chain.mapRange(range(0, 3), 0, DEFAULT_COMPLETION_AFFINITY);
  assert.deepEqual(mapped, { valid: true, range: { start: 0, end: 6 } });
});

test("an edit touching the interior invalidates the old range mid-chain", () => {
  const doc = new VersionedDocument("foo");
  doc.insert(3, "b"); // v1 — fine, boundary
  doc.delete(1, 2); // v2 — deletes interior char of [0, 4)
  const mapped = doc.chain.mapRange(range(0, 3), 0, DEFAULT_COMPLETION_AFFINITY);
  assert.deepEqual(mapped, { valid: false });
  // but the range was still valid at v1
  const atV1 = doc.chain.mapRange(range(0, 3), 0, DEFAULT_COMPLETION_AFFINITY, 1);
  assert.deepEqual(atV1, { valid: true, range: { start: 0, end: 4 } });
});

test("edits elsewhere in the document only shift the range", () => {
  const doc = new VersionedDocument("const x = fo");
  // range over "fo" = [10, 12); user inserts a newline at the top
  doc.insert(0, "// header\n");
  const mapped = doc.chain.mapRange(range(10, 12), 0, DEFAULT_COMPLETION_AFFINITY);
  assert.deepEqual(mapped, { valid: true, range: { start: 20, end: 22 } });
});

test("versions outside the tracked window are rejected", () => {
  const chain = new EditChain(0);
  chain.record(edit(0, 0, "a"));
  assert.ok(chain.hasVersion(0));
  assert.ok(chain.hasVersion(1));
  assert.ok(!chain.hasVersion(2));
  assert.ok(!chain.hasVersion(-1));
  assert.throws(() => chain.mapRange(range(0, 0), 2, DEFAULT_COMPLETION_AFFINITY), RangeError);
  assert.throws(() => chain.mapRange(range(0, 0), 1, DEFAULT_COMPLETION_AFFINITY, 0), RangeError);
});

test("mapOffset walks the whole chain", () => {
  const doc = new VersionedDocument("abcd");
  doc.insert(0, ">>"); // v1: ">>abcd"
  doc.delete(4, 6); // v2: ">>ab" (removes "cd")
  // offset 2 in v0 (between "b" and "c") shifts past the insertion,
  // then clamps to the deletion edge since "c" is gone.
  assert.equal(doc.chain.mapOffset(2, 0, "after"), 4);
  assert.equal(doc.getText(), ">>ab");
});
