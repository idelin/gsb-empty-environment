import { test } from "node:test";
import assert from "node:assert/strict";
import { FileLayer } from "../src/filelayer.ts";
import { LogManager } from "../src/log.ts";
import { PageStore } from "../src/pagestore.ts";
import { LockManager, LockConflictError } from "../src/locks.ts";
import { TransactionManager } from "../src/txn.ts";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

function stack() {
  const dir = mkdtempSync(join(tmpdir(), "ptl-ab-"));
  const fl = new FileLayer(dir);
  const log = new LogManager(fl);
  const pages = new PageStore();
  const locks = new LockManager();
  const tm = new TransactionManager(log, pages, locks);
  return { fl, log, pages, locks, tm };
}

test("full abort skips updates already compensated by CLRs", () => {
  const { tm, log, pages } = stack();
  const t = tm.begin();
  tm.update(t, "A", 1);
  tm.savepoint(t, "s");
  tm.update(t, "A", 2);
  tm.update(t, "A", 3);

  const rb = tm.rollbackTo(t, "s");
  assert.equal(rb.undone, 2); // A=3, A=2 compensated

  const ab = tm.abort(t);
  assert.equal(ab.clrs.length, 1); // only A=1 still needs compensation

  const clrs = log.records.filter((r) => r.type === "clr" && r.txn === t);
  assert.equal(clrs.length, 3); // each of the 3 updates compensated exactly once
  assert.equal(pages.readCommitted("A"), 0);
});

test("strict txn-level exclusive locks: conflict detected, released at commit", () => {
  const { tm, locks } = stack();
  const t1 = tm.begin();
  const t2 = tm.begin();
  tm.update(t1, "A", 1);
  tm.update(t2, "B", 2);
  assert.equal(locks.holder("A"), t1);
  assert.throws(() => tm.update(t2, "A", 9), LockConflictError);
  tm.commit(t1);
  assert.equal(locks.holder("A"), undefined);
  tm.update(t2, "A", 9); // now fine
  tm.commit(t2);
});

test("interleaved transactions commit independently, readers see committed only", () => {
  const { tm, pages } = stack();
  const t1 = tm.begin();
  const t2 = tm.begin();
  tm.update(t1, "X", 1);
  tm.update(t2, "Y", 2);
  tm.update(t1, "Z", 3);
  assert.equal(pages.readCommitted("X"), 0);
  assert.equal(pages.readCommitted("Y"), 0);
  tm.commit(t2);
  assert.equal(pages.readCommitted("Y"), 2);
  assert.equal(pages.readCommitted("X"), 0); // t1 still active
  tm.commit(t1);
  assert.equal(pages.readCommitted("X"), 1);
  assert.equal(pages.readCommitted("Z"), 3);
});

test("abort releases locks so other txns can proceed", () => {
  const { tm, locks } = stack();
  const t1 = tm.begin();
  const t2 = tm.begin();
  tm.update(t1, "A", 1);
  assert.throws(() => tm.update(t2, "A", 2), LockConflictError);
  tm.abort(t1);
  assert.equal(locks.holder("A"), undefined);
  tm.update(t2, "A", 2);
  tm.commit(t2);
});
