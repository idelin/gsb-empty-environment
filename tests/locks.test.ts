import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { System } from "../src/system.ts";
import { LockConflictError } from "../src/pageStore.ts";

function dir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "ptl-test-lock-"));
}

test("strict txn-level exclusive locks: conflict, held to commit, then reusable", () => {
  const sys = System.open(dir());
  const t1 = sys.begin();
  const t2 = sys.begin();
  sys.update(t1, "A", "a1");

  assert.throws(() => sys.update(t2, "A", "nope"), LockConflictError);

  // lock is strict: still held after more work by t1, released only at commit
  sys.update(t1, "B", "b1");
  assert.throws(() => sys.update(t2, "A", "nope"), LockConflictError);

  sys.commit(t1);
  sys.update(t2, "A", "a2"); // now t2 can take it
  sys.commit(t2);
  assert.equal(sys.readPage("A"), "a2");
});

test("external readers only see committed versions", () => {
  const sys = System.open(dir());
  const t0 = sys.begin();
  sys.update(t0, "P", "committed-0");
  sys.commit(t0);

  const t1 = sys.begin();
  sys.update(t1, "P", "uncommitted-1");
  assert.equal(sys.readPage("P"), "committed-0"); // dirty read impossible

  const t2 = sys.begin();
  sys.update(t2, "Q", "q1");
  assert.equal(sys.readPage("Q"), ""); // never committed: invisible

  sys.commit(t1);
  assert.equal(sys.readPage("P"), "uncommitted-1"); // visible only after commit
  sys.abort(t2);
  assert.equal(sys.readPage("Q"), ""); // aborted: never visible
});

test("interleaved transactions on disjoint pages commit independently", () => {
  const sys = System.open(dir());
  const t1 = sys.begin();
  const t2 = sys.begin();
  sys.update(t1, "A", "a1");
  sys.update(t2, "B", "b1");
  sys.savepoint(t1, "s");
  sys.update(t1, "A", "a2");
  sys.update(t2, "B", "b2");
  sys.rollbackTo(t1, "s");
  sys.update(t1, "A", "a3");
  sys.commit(t2);
  assert.equal(sys.readPage("A"), ""); // t1 still uncommitted
  assert.equal(sys.readPage("B"), "b2");
  sys.commit(t1);
  assert.equal(sys.readPage("A"), "a3");
  assert.equal(sys.readPage("B"), "b2");
});
