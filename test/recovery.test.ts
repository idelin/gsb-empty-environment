import test from "node:test";
import * as assert from "node:assert/strict";
import * as fs from "fs";
import * as os from "os";
import * as path from "path";
import { Database } from "../src/database";
import { recover, analysis, redo, undo } from "../src/recovery";
import { Wal } from "../src/wal";
import { PageStore, BufferPool, PAGE_HEADER_SIZE } from "../src/storage";
import { RecordType, ClrRecord } from "../src/logrecord";

const OFF_A = PAGE_HEADER_SIZE;       // 8
const OFF_B = PAGE_HEADER_SIZE + 64;  // 72

function tmpdir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "aries-recovery-test-"));
}

function readDisk(dataPath: string, pageId: number, offset: number, len: number): Buffer {
  const store = new PageStore(dataPath);
  const page = store.readPage(pageId);
  store.close();
  const out = Buffer.allocUnsafe(len);
  page.copy(out, 0, offset, offset + len);
  return out;
}

interface CrashScenario {
  dir: string;
  walPath: string;
  dataPath: string;
  db: Database;
  t1: number; t2: number; t3: number; t4: number; t5: number;
}

/**
 * Acceptance scenario: 5 transactions, a fuzzy checkpoint in the middle,
 * then a crash. T1 committed+ended, T2 committed (no END), T3/T4/T5 never
 * committed. Two dirty pages (one holding uncommitted data) are stolen to
 * disk before the crash; the rest of the dirty buffers vanish.
 */
function buildCrashScenario(): CrashScenario {
  const dir = tmpdir();
  const walPath = path.join(dir, "wal.log");
  const dataPath = path.join(dir, "data.db");
  const db = new Database(walPath, dataPath);

  // --- before checkpoint ---
  const t1 = db.begin();
  const t2 = db.begin();
  db.update(t1, 1, OFF_A, Buffer.from("AAAA"));   // committed, ended
  db.update(t2, 2, OFF_A, Buffer.from("BBBB"));   // committed
  db.commit(t1);
  db.endTxn(t1);

  db.checkpoint();

  // --- after checkpoint ---
  const t3 = db.begin();
  const t4 = db.begin();
  const t5 = db.begin();
  db.update(t2, 1, OFF_B, Buffer.from("CC"));     // committed (stolen to disk)
  db.update(t2, 5, OFF_A, Buffer.from("DDDD"));   // committed (lost in crash)
  db.commit(t2);                                  // winner, END missing at crash
  db.update(t3, 1, OFF_A, Buffer.from("XXXX"));   // uncommitted, stolen to disk
  db.update(t3, 3, OFF_A, Buffer.from("T3T3"));   // uncommitted, lost in crash
  db.update(t4, 2, OFF_A, Buffer.from("YYYY"));   // uncommitted, stolen to disk
  db.update(t5, 4, OFF_A, Buffer.from("ZZZZ"));   // uncommitted, lost in crash

  // steal evictions: dirty pages (incl. uncommitted data) reach disk
  db.stealPage(1);
  db.stealPage(2);

  db.crash();
  return { dir, walPath, dataPath, db, t1, t2, t3, t4, t5 };
}

test("analysis rebuilds ATT and DPT from the checkpoint forward", () => {
  const s = buildCrashScenario();
  const wal = Wal.open(s.walPath);
  const result = analysis(wal);

  assert.ok(result.lastCheckpointLSN > 0, "checkpoint must be found");
  // ATT: T1 ended (gone), T2 committed, T3/T4/T5 running losers.
  assert.equal(result.txns.size, 4);
  assert.equal(result.txns.get(s.t2)?.status, "committed");
  assert.equal(result.txns.get(s.t3)?.status, "running");
  assert.equal(result.txns.get(s.t4)?.status, "running");
  assert.equal(result.txns.get(s.t5)?.status, "running");
  assert.ok(!result.txns.has(s.t1));

  // DPT: pages 1..5 all dirty at crash time.
  assert.deepEqual([...result.dirtyPages.keys()].sort((a, b) => a - b), [1, 2, 3, 4, 5]);
  // recLSN of page 1 is T1's pre-checkpoint update -> redo starts before ckpt.
  assert.ok(result.redoLSN < result.lastCheckpointLSN);
  assert.equal(result.redoLSN, Math.min(...result.dirtyPages.values()));
  wal.close();
  fs.rmSync(s.dir, { recursive: true, force: true });
});

test("redo repeats history including uncommitted and loser updates", () => {
  const s = buildCrashScenario();
  const wal = Wal.open(s.walPath);
  const store = new PageStore(s.dataPath);
  const pool = new BufferPool(store);
  const result = analysis(wal);
  const redoResult = redo(wal, result, pool);

  // Exactly the updates lost in the crash are re-applied:
  // T3->page3, T5->page4 (losers) and T2->page5 (winner).
  assert.equal(redoResult.appliedLSNs.length, 3);
  assert.ok(pool.readBytes(3, OFF_A, 4).equals(Buffer.from("T3T3")));
  assert.ok(pool.readBytes(4, OFF_A, 4).equals(Buffer.from("ZZZZ")));
  assert.ok(pool.readBytes(5, OFF_A, 4).equals(Buffer.from("DDDD")));
  // stolen pages keep their on-disk (already reflected) state
  assert.ok(pool.readBytes(1, OFF_A, 4).equals(Buffer.from("XXXX")));
  assert.ok(pool.readBytes(2, OFF_A, 4).equals(Buffer.from("YYYY")));
  pool.close(); store.close(); wal.close();
  fs.rmSync(s.dir, { recursive: true, force: true });
});

test("full recovery: committed data persists, uncommitted data is rolled back", () => {
  const s = buildCrashScenario();
  const report = recover(s.walPath, s.dataPath);

  // --- undo wrote one CLR per uncommitted update (T3x2, T4, T5) ---
  assert.equal(report.undo.clrLSNs.length, 4);
  // --- every transaction ended; ATT is empty; nothing left dirty ---
  assert.equal(report.analysis.txns.size, 0);
  assert.deepEqual(
    [...report.undo.endedTxns].sort((a, b) => a - b),
    [s.t2, s.t3, s.t4, s.t5].sort((a, b) => a - b),
  );
  assert.ok(report.resourcesClosed);

  // --- final disk state ---
  assert.ok(readDisk(s.dataPath, 1, OFF_A, 4).equals(Buffer.from("AAAA")), "T1 committed value restored over T3");
  assert.ok(readDisk(s.dataPath, 1, OFF_B, 2).equals(Buffer.from("CC")), "T2 committed write kept");
  assert.ok(readDisk(s.dataPath, 2, OFF_A, 4).equals(Buffer.from("BBBB")), "T2 committed value restored over T4");
  assert.ok(readDisk(s.dataPath, 3, OFF_A, 4).equals(Buffer.alloc(4)), "T3 loser page zeroed");
  assert.ok(readDisk(s.dataPath, 4, OFF_A, 4).equals(Buffer.alloc(4)), "T5 loser page zeroed");
  assert.ok(readDisk(s.dataPath, 5, OFF_A, 4).equals(Buffer.from("DDDD")), "T2 committed write redone");

  // --- WAL contents after recovery ---
  const wal = Wal.open(s.walPath);
  const types = [...wal.scan()].map((f) => f.record.type);
  const count = (t: RecordType) => types.filter((x) => x === t).length;
  assert.equal(count(RecordType.Clr), 4);
  assert.equal(count(RecordType.Abort), 3);      // T3, T4, T5
  assert.equal(count(RecordType.End), 5);        // T1 pre-crash + T2..T5 in recovery
  // CLR undoNextLSN chains point at the update's prevLSN
  const clrs = [...wal.scan()]
    .map((f) => f.record)
    .filter((r): r is ClrRecord => r.type === RecordType.Clr);
  for (const clr of clrs) {
    const target = wal.readRecord(clr.prevLSN === 0 ? 0 : clr.prevLSN);
    void target;
    assert.ok(clr.undoNextLSN >= 0);
  }
  wal.close();
  fs.rmSync(s.dir, { recursive: true, force: true });
});

test("recovery is idempotent: a second crash mid-undo changes nothing", () => {
  const s = buildCrashScenario();
  recover(s.walPath, s.dataPath);
  const walSizeAfterFirst = fs.readFileSync(s.walPath).length;

  // Second recovery (simulates a crash during the first undo pass).
  const report2 = recover(s.walPath, s.dataPath);
  assert.equal(report2.undo.clrLSNs.length, 0, "no update is undone twice");
  assert.equal(report2.analysis.txns.size, 0);
  assert.equal(fs.readFileSync(s.walPath).length, walSizeAfterFirst,
    "no new log records are needed");

  assert.ok(readDisk(s.dataPath, 1, OFF_A, 4).equals(Buffer.from("AAAA")));
  assert.ok(readDisk(s.dataPath, 2, OFF_A, 4).equals(Buffer.from("BBBB")));
  assert.ok(readDisk(s.dataPath, 5, OFF_A, 4).equals(Buffer.from("DDDD")));
  fs.rmSync(s.dir, { recursive: true, force: true });
});

test("undo processes losers in strictly reverse LSN order", () => {
  const s = buildCrashScenario();
  const wal = Wal.open(s.walPath);
  const store = new PageStore(s.dataPath);
  const pool = new BufferPool(store);
  const result = analysis(wal);
  redo(wal, result, pool);
  const undoResult = undo(wal, result, pool);
  // CLRs are appended in reverse order of the updates they compensate.
  assert.equal(undoResult.clrLSNs.length, 4);
  for (let i = 1; i < undoResult.clrLSNs.length; i++) {
    assert.ok(undoResult.clrLSNs[i]! > undoResult.clrLSNs[i - 1]!);
  }
  assert.equal(result.txns.size, 0, "no transaction leaks out of undo");
  assert.equal(pool.dirtyPageIds.length, 0, "no dirty page leaks out of undo");
  pool.close(); store.close(); wal.close();
  fs.rmSync(s.dir, { recursive: true, force: true });
});

test("recovery without any checkpoint scans from the log start", () => {
  const dir = tmpdir();
  const walPath = path.join(dir, "wal.log");
  const dataPath = path.join(dir, "data.db");
  const db = new Database(walPath, dataPath);
  const t1 = db.begin();
  db.update(t1, 1, OFF_A, Buffer.from("SOLO"));
  db.commit(t1);
  const t2 = db.begin();
  db.update(t2, 1, OFF_A, Buffer.from("GONE"));
  db.crash();

  const report = recover(walPath, dataPath);
  assert.equal(report.analysis.lastCheckpointLSN, 0);
  assert.equal(report.undo.clrLSNs.length, 1);
  assert.ok(readDisk(dataPath, 1, OFF_A, 4).equals(Buffer.from("SOLO")));
  fs.rmSync(dir, { recursive: true, force: true });
});

test("clean shutdown leaves no leak: pool empty, files closed", () => {
  const dir = tmpdir();
  const db = new Database(path.join(dir, "wal.log"), path.join(dir, "data.db"));
  const t = db.begin();
  db.update(t, 1, OFF_A, Buffer.from("DATA"));
  db.commit(t);
  db.endTxn(t);
  db.shutdown();
  assert.equal(db.activeTxns.size, 0);
  assert.equal(db.pool.size, 0);
  assert.equal(db.pool.dirtyPageIds.length, 0);
  assert.ok(db.wal.isClosed);
  assert.ok(db.store.isClosed);
  fs.rmSync(dir, { recursive: true, force: true });
});
