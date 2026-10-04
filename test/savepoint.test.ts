import { test } from "node:test";
import assert from "node:assert/strict";
import { FileLayer } from "../src/filelayer.ts";
import { LogManager } from "../src/log.ts";
import { PageStore } from "../src/pagestore.ts";
import { LockManager } from "../src/locks.ts";
import { TransactionManager } from "../src/txn.ts";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

function stack() {
  const dir = mkdtempSync(join(tmpdir(), "ptl-sp-"));
  const fl = new FileLayer(dir);
  const log = new LogManager(fl);
  const pages = new PageStore();
  const tm = new TransactionManager(log, pages, new LockManager());
  return { fl, log, pages, tm };
}

test("nested savepoints: rollbackTo undoes only uncompensated updates after the savepoint", () => {
  const { tm, pages, log } = stack();
  const t = tm.begin();
  tm.update(t, "A", 1);
  tm.savepoint(t, "s1");
  tm.update(t, "A", 2);
  tm.update(t, "B", 1);
  tm.savepoint(t, "s2");
  tm.update(t, "A", 3);
  tm.update(t, "B", 2);

  const r1 = tm.rollbackTo(t, "s2");
  assert.equal(r1.undone, 2);
  assert.equal(pages.readForTxn("A"), 2);
  assert.equal(pages.readForTxn("B"), 1);

  // new update after rollback, then roll back further past the CLRs
  tm.update(t, "A", 5);
  const r2 = tm.rollbackTo(t, "s1");
  assert.equal(r2.undone, 3); // A=5, B=1, A=2 — the two existing CLRs are skipped
  assert.equal(pages.readForTxn("A"), 1);
  assert.equal(pages.readForTxn("B"), 0);

  tm.commit(t);
  assert.equal(pages.readCommitted("A"), 1);
  assert.equal(pages.readCommitted("B"), 0);

  const clrs = log.records.filter((r) => r.type === "clr");
  assert.equal(clrs.length, 5); // 2 + 3, each update compensated exactly once
});

test("repeated rollbackTo on the same savepoint is idempotent", () => {
  const { tm, log } = stack();
  const t = tm.begin();
  tm.update(t, "A", 1);
  tm.savepoint(t, "s");
  tm.update(t, "A", 2);
  tm.update(t, "A", 3);

  const before = log.records.length;
  const r1 = tm.rollbackTo(t, "s");
  assert.equal(r1.undone, 2);
  const mid = log.records.length;
  const r2 = tm.rollbackTo(t, "s");
  assert.equal(r2.undone, 0);
  assert.equal(log.records.length, mid); // no new records written
  assert.ok(mid > before);
  tm.commit(t);
});

test("savepoint name reuse overwrites the old binding", () => {
  const { tm, pages } = stack();
  const t = tm.begin();
  tm.update(t, "A", 1);
  tm.savepoint(t, "x");
  tm.update(t, "A", 2);
  tm.savepoint(t, "x"); // rebind
  tm.update(t, "A", 3);

  const r = tm.rollbackTo(t, "x");
  assert.equal(r.undone, 1); // only A=3, not A=2
  assert.equal(pages.readForTxn("A"), 2);
  tm.commit(t);
  assert.equal(pages.readCommitted("A"), 2);
});

test("CLR records carry undoNext pointing at the compensated update prevLSN", () => {
  const { tm, log } = stack();
  const t = tm.begin();
  const l1 = tm.update(t, "A", 1);
  const l2 = tm.update(t, "A", 2);
  tm.savepoint(t, "s");
  const l3 = tm.update(t, "A", 3);
  const r = tm.rollbackTo(t, "s");
  assert.equal(r.clrs.length, 1);
  const clr = log.get(r.clrs[0]);
  assert.equal(clr?.type, "clr");
  if (clr?.type === "clr") {
    assert.equal(clr.undoNext, l2); // prevLSN of the undone update l3
    assert.equal(clr.before, 3); // before-image of the CLR = after-image of the update
    assert.equal(clr.after, 2);
  }
  assert.equal(l3, l2 + 1);
  assert.equal(l1 + 1, l2);
  tm.commit(t);
});

test("external readers only see committed versions", () => {
  const { tm, pages } = stack();
  const t = tm.begin();
  tm.update(t, "P", 42);
  assert.equal(pages.readCommitted("P"), 0); // uncommitted invisible
  tm.commit(t);
  assert.equal(pages.readCommitted("P"), 42);
});
