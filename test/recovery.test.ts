import { test } from "node:test";
import assert from "node:assert/strict";
import { RecordType } from "../src/logrecord.ts";
import { MiniDB } from "../src/minidb.ts";
import { recover } from "../src/recovery.ts";
import { buildCrashScenario, expectedPage } from "../src/scenario.ts";
import { mapWal, scanWal } from "../src/wal.ts";
import { PageStore, getPageLSN } from "../src/pagestore.ts";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

test("ARIES recovery: 3 uncommitted + 2 committed txns, crash after checkpoint", async (t) => {
  const scenario = await buildCrashScenario();
  const { t1, t2, t3, t4, t5 } = scenario.txns;

  const preCrash = [...scanWal(mapWal(scenario.walPath))];
  assert.ok(
    preCrash.some((e) => e.record.type === RecordType.END_CHECKPOINT),
    "scenario must contain a checkpoint",
  );
  assert.ok(
    preCrash.some((e) => e.record.type === RecordType.CLR && e.record.txnId === t5),
    "scenario must contain a pre-crash CLR from the interrupted abort",
  );

  const store = await PageStore.open(scenario.dataPath);
  const report = await recover(scenario.walPath, store);

  await t.test("analysis rebuilds TT and DPT from the checkpoint forward", () => {
    const { txnTable, dpt, checkpointLSN, redoLSN } = report.analysis;
    assert.ok(checkpointLSN > 0);
    assert.equal(txnTable.get(t1)?.status, "running");
    assert.equal(txnTable.get(t3)?.status, "running");
    assert.equal(txnTable.get(t5)?.status, "aborting");
    assert.equal(txnTable.get(t4)?.status, "committed");
    assert.equal(txnTable.has(t2), false, "END removes committed txn from TT");
    assert.deepEqual([...dpt.keys()].sort(), [1, 2, 3]);
    assert.equal(redoLSN, Math.min(...dpt.values()));
  });

  await t.test("redo repeats history guarded by pageLSN", () => {
    // Pages 1 and 2 were flushed before the crash: their pre-checkpoint
    // updates (A, B, C) must be skipped. Everything later - including the
    // losers' updates D, F, I, G, H, the committed E and the pre-crash CLR -
    // must be repeated.
    assert.equal(report.redo.redone.length, 7);
    assert.equal(report.redo.skipped.length, 3);
    const redoneRecords = report.redo.redone.map((lsn) =>
      preCrash.find((e) => e.lsn === lsn)?.record,
    );
    const committedE = redoneRecords.find(
      (r) => r?.type === RecordType.UPDATE && r.txnId === t4,
    );
    assert.ok(committedE, "committed T4 update must be redone");
  });

  await t.test("undo rolls back exactly the losers and logs CLRs + ENDs", () => {
    assert.deepEqual([...report.undo.undoneTxns].sort(), [t1, t3, t5].sort());
    assert.ok(!report.undo.undoneTxns.includes(t4), "committed txn must not be undone");
    // 6 undone updates: H, G (page 2), D, F (page 3), A, C (page 1).
    assert.equal(report.undo.clrs.length, 6);
    assert.equal(report.undo.ends.length, 3);
  });

  await t.test("committed data persisted, uncommitted data reverted on disk", async () => {
    for (const pageId of [1, 2, 3]) {
      const page = await store.readPageRaw(pageId);
      const expect = expectedPage(pageId);
      assert.ok(
        page.subarray(8).equals(expect.subarray(8)),
        `page ${pageId} data mismatch`,
      );
    }
  });

  await t.test("pageLSN equals the newest log record applied to each page", async () => {
    const newest = new Map<number, number>();
    for (const { lsn, record } of scanWal(mapWal(scenario.walPath))) {
      if (record.type === RecordType.UPDATE || record.type === RecordType.CLR) {
        newest.set(record.pageId, lsn);
      }
    }
    for (const [pageId, lsn] of newest) {
      const page = await store.readPageRaw(pageId);
      assert.equal(getPageLSN(page), lsn, `page ${pageId} pageLSN`);
    }
  });

  await t.test("CLR chain: each CLR points at its update via prevLSN/undoNextLSN", () => {
    const post = [...scanWal(mapWal(scenario.walPath))];
    const byLsn = new Map(post.map((e) => [e.lsn, e.record]));
    for (const { record } of post) {
      if (record.type !== RecordType.CLR) continue;
      const target = byLsn.get(record.prevLSN);
      assert.ok(target, "CLR prevLSN must reference an existing record");
      assert.equal(target!.txnId, record.txnId, "CLR chain stays within its transaction");
      // Recovery-written CLRs compensate the UPDATE at prevLSN directly;
      // abort-time CLRs chain through the ABORT record first.
      assert.ok(
        target!.type === RecordType.UPDATE ||
          target!.type === RecordType.CLR ||
          target!.type === RecordType.ABORT,
      );
      if (target?.type === RecordType.UPDATE) {
        assert.equal(record.undoNextLSN, target.prevLSN);
      }
    }
  });

  await t.test("recovery is idempotent", async () => {
    const again = await recover(scenario.walPath, store);
    assert.equal(again.undo.undoneTxns.length, 0, "no losers remain");
    assert.equal(again.undo.clrs.length, 0, "no new CLRs");
    for (const pageId of [1, 2, 3]) {
      const page = await store.readPageRaw(pageId);
      assert.ok(page.subarray(8).equals(expectedPage(pageId).subarray(8)));
    }
  });

  await store.close();
  assert.ok(store.closed);
});

test("recovery without any checkpoint scans from the start of the log", async () => {
  const dir = mkdtempSync(join(tmpdir(), "aries-nockpt-"));
  const walPath = join(dir, "wal.log");
  const dataPath = join(dir, "data.db");
  const db = await MiniDB.create(walPath, dataPath);
  const tx = db.begin();
  await db.update(tx, 1, 0, Buffer.from("UNCOMMIT"));
  await db.crash();
  const store = await PageStore.open(dataPath);
  const report = await recover(walPath, store);
  assert.equal(report.analysis.checkpointLSN, 0);
  assert.deepEqual(report.undo.undoneTxns, [tx]);
  const page = await store.readPageRaw(1);
  assert.ok(page.subarray(8, 16).every((b) => b === 0), "uncommitted update reverted");
  await store.close();
});

test("recovery of an empty WAL is a no-op and leak-free", async () => {
  const dir = mkdtempSync(join(tmpdir(), "aries-empty-"));
  const walPath = join(dir, "wal.log");
  const dataPath = join(dir, "data.db");
  const db = await MiniDB.create(walPath, dataPath);
  await db.crash();
  const store = await PageStore.open(dataPath);
  const report = await recover(walPath, store);
  assert.equal(report.analysis.txnTable.size, 0);
  assert.equal(report.analysis.dpt.size, 0);
  assert.equal(report.redo.redone.length, 0);
  assert.equal(report.undo.clrs.length, 0);
  await store.close();
});
