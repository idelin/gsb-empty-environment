import { test } from "node:test";
import assert from "node:assert/strict";
import { FileLayer } from "../src/filelayer.ts";
import { LogManager } from "../src/log.ts";
import { PageStore } from "../src/pagestore.ts";
import { LockManager } from "../src/locks.ts";
import { TransactionManager } from "../src/txn.ts";
import { recover } from "../src/recovery.ts";
import { computeRetentionBoundary, executeTruncation } from "../src/truncate.ts";
import { rebuildFinalPages, comparePages } from "../src/rebuild.ts";
import type { LogRecord } from "../src/types.ts";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

function stack(dir: string, capacity = 6) {
  const fl = new FileLayer(dir, capacity);
  const log = new LogManager(fl);
  const pages = new PageStore(dir);
  const tm = new TransactionManager(log, pages, new LockManager());
  return { fl, log, pages, tm };
}

test("retention boundary is the min of active-txn, dirty-page and checkpoint factors", () => {
  const dir = mkdtempSync(join(tmpdir(), "ptl-tr1-"));
  const { log, pages, tm } = stack(dir);

  const t1 = tm.begin();
  tm.update(t1, "L", 1); // long-lived txn, firstLSN early
  const t2 = tm.begin();
  tm.update(t2, "A", 1);
  tm.commit(t2);
  pages.flushEligible();
  tm.checkpoint();

  const d = computeRetentionBoundary({
    activeTxns: tm.txnTable(),
    dirtyPages: pages.dirtyPageTable(),
    lastCheckpoint: log.lastCompleteCheckpoint(),
  });
  assert.ok(d.ok);
  const names = d.factors.map((f) => f.name);
  assert.deepEqual(names, ["active-txns", "dirty-pages", "checkpoint"]);
  assert.equal(d.boundary, 1); // t1 firstLSN=1 dominates (its undo chain must survive)
  assert.ok(d.rationale.some((r) => r.includes("firstLSN=1")));
});

test("truncation is refused when there is no complete checkpoint", () => {
  const dir = mkdtempSync(join(tmpdir(), "ptl-tr2-"));
  const { log, pages, tm } = stack(dir);
  const t = tm.begin();
  tm.update(t, "A", 1);
  tm.commit(t);
  const d = computeRetentionBoundary({
    activeTxns: tm.txnTable(),
    dirtyPages: pages.dirtyPageTable(),
    lastCheckpoint: log.lastCompleteCheckpoint(),
  });
  assert.equal(d.ok, false);
  assert.match(d.refusal ?? "", /no complete checkpoint/);
  assert.throws(() => executeTruncation(new FileLayer(dir), d), /no complete checkpoint/);
});

test("truncation beyond the safe boundary is refused", () => {
  const dir = mkdtempSync(join(tmpdir(), "ptl-tr3-"));
  const { log, pages, tm } = stack(dir);
  const t = tm.begin();
  tm.update(t, "A", 1);
  tm.commit(t);
  tm.checkpoint();
  const safe = computeRetentionBoundary({
    activeTxns: tm.txnTable(),
    dirtyPages: pages.dirtyPageTable(),
    lastCheckpoint: log.lastCompleteCheckpoint(),
  });
  assert.ok(safe.ok);
  const greedy = computeRetentionBoundary({
    activeTxns: tm.txnTable(),
    dirtyPages: pages.dirtyPageTable(),
    lastCheckpoint: log.lastCompleteCheckpoint(),
    requested: (safe.boundary ?? 0) + 5,
  });
  assert.equal(greedy.ok, false);
  assert.match(greedy.refusal ?? "", /exceeds the safe boundary/);
});

test("recovery from truncated log matches independent rebuild from the full log", () => {
  const dir = mkdtempSync(join(tmpdir(), "ptl-tr4-"));
  let { fl, log, pages, tm } = stack(dir);

  const t2 = tm.begin();
  tm.update(t2, "A", 10);
  tm.update(t2, "B", 20);
  tm.commit(t2);
  const t3 = tm.begin(); // savepoint rollback, then commits
  tm.update(t3, "A", 11);
  tm.savepoint(t3, "s");
  tm.update(t3, "A", 12);
  tm.rollbackTo(t3, "s");
  tm.commit(t3);
  pages.flushEligible();
  pages.persistDisk();
  tm.checkpoint();
  const t1 = tm.begin(); // starts after the checkpoint, stays active
  tm.update(t1, "L", 1);
  const t4 = tm.begin(); // loser: never commits
  tm.update(t4, "B", 99);
  tm.update(t4, "C", 7);

  const fullLog: LogRecord[] = log.records.map((r) => ({ ...r }));

  const decision = computeRetentionBoundary({
    activeTxns: tm.txnTable(),
    dirtyPages: pages.dirtyPageTable(),
    lastCheckpoint: log.lastCompleteCheckpoint(),
  });
  assert.ok(decision.ok);
  const res = executeTruncation(fl, decision);
  assert.ok(res.removedSegments.length > 0); // something was actually reclaimed

  // restart from the truncated log
  ({ log, pages, tm } = stack(dir));
  const report = recover(log, pages);
  assert.deepEqual(report.analysis.losers, [t1, t4]);

  const expected = rebuildFinalPages(fullLog);
  const cmp = comparePages(expected, pages.committedSnapshot());
  assert.deepEqual(cmp.diffs, []);
  assert.equal(pages.readCommitted("A"), 11);
  assert.equal(pages.readCommitted("B"), 20); // t4 undo restored t2 value
  assert.equal(pages.readCommitted("C"), 0);
  assert.equal(pages.readCommitted("L"), 0); // t1 still active: invisible
});

test("loser undo after truncation needs retained chain: guard keeps oldest active firstLSN", () => {
  const dir = mkdtempSync(join(tmpdir(), "ptl-tr5-"));
  let { fl, log, pages, tm } = stack(dir, 4);

  const t1 = tm.begin();
  tm.update(t1, "A", 1); // firstLSN=1, stays active
  const t2 = tm.begin();
  for (let i = 0; i < 10; i++) tm.update(t2, `P${i}`, i);
  tm.commit(t2);
  pages.flushEligible();
  pages.persistDisk();
  tm.checkpoint();

  const decision = computeRetentionBoundary({
    activeTxns: tm.txnTable(),
    dirtyPages: pages.dirtyPageTable(),
    lastCheckpoint: log.lastCompleteCheckpoint(),
  });
  assert.equal(decision.boundary, 1); // pinned by t1, nothing can be reclaimed
  const res = executeTruncation(fl, decision);
  assert.equal(res.removedSegments.length, 0);

  // once t1 finishes and its pages are flushed, the boundary advances to the checkpoint
  tm.commit(t1);
  pages.flushEligible();
  const d2 = computeRetentionBoundary({
    activeTxns: tm.txnTable(),
    dirtyPages: pages.dirtyPageTable(),
    lastCheckpoint: log.lastCompleteCheckpoint(),
  });
  assert.ok((d2.boundary ?? 0) > 1);
});
