import { test } from "node:test";
import assert from "node:assert/strict";
import { FileLayer, CrashError } from "../src/filelayer.ts";
import { LogManager } from "../src/log.ts";
import { PageStore } from "../src/pagestore.ts";
import { LockManager } from "../src/locks.ts";
import { TransactionManager } from "../src/txn.ts";
import { recover } from "../src/recovery.ts";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

function stack(dir: string) {
  const fl = new FileLayer(dir);
  const log = new LogManager(fl);
  const pages = new PageStore(dir);
  const tm = new TransactionManager(log, pages, new LockManager());
  return { fl, log, pages, tm };
}

test("restart in the middle of rollbackTo resumes undo without double compensation", () => {
  const dir = mkdtempSync(join(tmpdir(), "ptl-rc1-"));
  let { fl, log, pages, tm } = stack(dir);

  const t = tm.begin();
  for (let i = 1; i <= 5; i++) tm.update(t, `P${i}`, i);
  tm.savepoint(t, "sp");
  for (let i = 1; i <= 5; i++) tm.update(t, `P${i}`, i * 10);

  fl.crashAfterAppends = 2; // die after two CLRs of the rollback
  assert.throws(() => tm.rollbackTo(t, "sp"), CrashError);

  fl.revive();
  ({ log, pages, tm } = stack(dir));
  const report = recover(log, pages);

  // the still-active txn is a loser: recovery undoes ALL its updates,
  // skipping the 2 CLRs that survived the crash
  assert.deepEqual(report.analysis.losers, [t]);
  const clrs = log.records.filter((r) => r.type === "clr" && r.txn === t);
  assert.equal(clrs.length, 10); // 10 updates, each compensated exactly once
  assert.equal(report.undo.clrsWritten.length, 8);
  for (let i = 1; i <= 5; i++) assert.equal(pages.readCommitted(`P${i}`), 0);
});

test("restart in the middle of a full abort continues from the abort record", () => {
  const dir = mkdtempSync(join(tmpdir(), "ptl-rc2-"));
  let { fl, log, pages, tm } = stack(dir);

  const t = tm.begin();
  tm.update(t, "A", 1);
  tm.update(t, "B", 2);
  tm.update(t, "C", 3);

  fl.crashAfterAppends = 2; // abort record + one CLR survive
  assert.throws(() => tm.abort(t), CrashError);

  fl.revive();
  ({ log, pages, tm } = stack(dir));
  const report = recover(log, pages);
  assert.deepEqual(report.analysis.losers, [t]);
  assert.equal(report.undo.clrsWritten.length, 2); // remaining two updates
  const clrs = log.records.filter((r) => r.type === "clr" && r.txn === t);
  assert.equal(clrs.length, 3);
  assert.equal(pages.readCommitted("A"), 0);
  assert.equal(pages.readCommitted("B"), 0);
  assert.equal(pages.readCommitted("C"), 0);
});

test("redo repeats history from the dirty-page recLSN after a real page flush", () => {
  const dir = mkdtempSync(join(tmpdir(), "ptl-rc3-"));
  let { fl, log, pages, tm } = stack(dir);

  const t1 = tm.begin();
  tm.update(t1, "A", 10);
  tm.commit(t1);
  pages.flushEligible();
  pages.persistDisk(); // page cleaner: A=10 lands on disk
  tm.checkpoint();

  const t2 = tm.begin();
  tm.update(t2, "A", 20); // committed but never flushed
  tm.commit(t2);
  const t3 = tm.begin();
  tm.update(t3, "B", 5); // loser

  fl.revive();
  ({ log, pages, tm } = stack(dir));
  const report = recover(log, pages);
  assert.ok(report.analysis.checkpointUsed !== null);
  assert.deepEqual(report.analysis.losers, [t3]);
  assert.ok(report.redo.applied >= 1); // A=20 replayed onto the stale disk image
  assert.equal(pages.readCommitted("A"), 20);
  assert.equal(pages.readCommitted("B"), 0);
});

test("torn checkpoint end is ignored; recovery falls back to the previous complete checkpoint", () => {
  const dir = mkdtempSync(join(tmpdir(), "ptl-rc4-"));
  const { fl, log, pages, tm } = stack(dir);

  const t1 = tm.begin();
  tm.update(t1, "A", 1);
  tm.commit(t1);
  const good = tm.checkpoint();

  const t2 = tm.begin();
  tm.update(t2, "B", 2);
  fl.tornNextAppend = true;
  const begin = log.append({ type: "chkpt_begin" });
  log.append({ type: "chkpt_end", beginLSN: begin.lsn, txns: tm.txnTable(), dirtyPages: pages.dirtyPageTable() });

  // reload from disk: torn record is reported as an anomaly and skipped
  const log2 = new LogManager(fl);
  assert.equal(log2.anomalies.length, 1);
  assert.equal(log2.lastCompleteCheckpoint()?.lsn, good);

  const pages2 = new PageStore(dir);
  const report = recover(log2, pages2);
  assert.equal(report.analysis.checkpointUsed, good);
  assert.deepEqual(report.analysis.losers, [t2]);
  assert.equal(pages2.readCommitted("A"), 1);
  assert.equal(pages2.readCommitted("B"), 0);
});
