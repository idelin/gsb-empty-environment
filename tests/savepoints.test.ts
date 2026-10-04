import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { System } from "../src/system.ts";
import type { ClrRecord, LogRecord } from "../src/types.ts";

function dir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "ptl-test-sp-"));
}

function clrs(log: LogRecord[]): ClrRecord[] {
  return log.filter((r) => r.type === "CLR") as ClrRecord[];
}

test("nested savepoints: rollback to outer undoes inner updates", () => {
  const sys = System.open(dir());
  const t = sys.begin();
  sys.update(t, "p1", "v1");
  sys.savepoint(t, "sp1");
  sys.update(t, "p1", "v2");
  sys.savepoint(t, "sp2");
  sys.update(t, "p1", "v3");

  sys.rollbackTo(t, "sp1"); // must undo v3 and v2, keep v1
  sys.commit(t);
  assert.equal(sys.readPage("p1"), "v1");

  // CLR.after is the before-image of each undone update (undo of v3 -> "v2", of v2 -> "v1")
  const undone = clrs(sys.log.all()).map((c) => c.after);
  assert.deepEqual(undone.sort(), ["v1", "v2"]);
});

test("repeated rollback to the same savepoint is idempotent", () => {
  const sys = System.open(dir());
  const t = sys.begin();
  sys.update(t, "p1", "a");
  sys.savepoint(t, "s");
  sys.update(t, "p1", "b");
  sys.update(t, "p1", "c");

  const first = sys.rollbackTo(t, "s");
  assert.equal(first.length, 2); // CLRs for c and b
  const logSizeAfterFirst = sys.log.all().length;

  const second = sys.rollbackTo(t, "s");
  assert.deepEqual(second, []); // nothing left to compensate
  assert.equal(sys.log.all().length, logSizeAfterFirst); // no new records at all

  sys.commit(t);
  assert.equal(sys.readPage("p1"), "a");
});

test("rollback then new updates then commit keeps post-rollback writes", () => {
  const sys = System.open(dir());
  const t = sys.begin();
  sys.update(t, "p1", "a");
  sys.savepoint(t, "s");
  sys.update(t, "p1", "b");
  sys.rollbackTo(t, "s");
  sys.update(t, "p1", "c"); // new update after the rollback
  sys.commit(t);
  assert.equal(sys.readPage("p1"), "c");
});

test("savepoint name reuse overwrites the old binding", () => {
  const sys = System.open(dir());
  const t = sys.begin();
  sys.update(t, "p1", "a");
  sys.savepoint(t, "s");
  sys.update(t, "p1", "b");
  sys.savepoint(t, "s"); // rebind: "s" now points after b
  sys.update(t, "p1", "c");

  sys.rollbackTo(t, "s"); // must undo only c, not b
  sys.commit(t);
  assert.equal(sys.readPage("p1"), "b");
});

test("full abort skips updates already compensated by CLRs", () => {
  const sys = System.open(dir());
  const t0 = sys.begin();
  sys.update(t0, "p1", "base");
  sys.commit(t0);

  const t = sys.begin();
  sys.update(t, "p1", "a");
  sys.savepoint(t, "s");
  sys.update(t, "p1", "b");
  sys.update(t, "p1", "c");
  sys.rollbackTo(t, "s"); // compensates c and b
  sys.abort(t); // must undo only a; b and c already compensated

  const all = clrs(sys.log.all());
  assert.equal(all.length, 3, "exactly one CLR per update");
  const undid = all.map((c) => c.undidLSN).sort((x, y) => x - y);
  assert.equal(new Set(undid).size, 3, "no update was compensated twice");
  assert.equal(sys.readPage("p1"), "base");
});

test("CLR carries undoNext pointing at the next record to undo", () => {
  const sys = System.open(dir());
  const t = sys.begin();
  sys.update(t, "p1", "a");
  sys.savepoint(t, "s");
  const lsnB = sys.update(t, "p1", "b");
  const lsnC = sys.update(t, "p1", "c");
  sys.rollbackTo(t, "s");

  const all = clrs(sys.log.all());
  assert.equal(all.length, 2);
  const [clrC, clrB] = all;
  assert.equal(clrC.undidLSN, lsnC);
  assert.equal(clrC.undoNextLSN, lsnB); // chain continues at b
  assert.equal(clrB.undidLSN, lsnB);
  assert.equal(clrB.after, "a"); // redo image = before-image of update b
});
