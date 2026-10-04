import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { CrashError } from "../src/fileLayer.ts";
import { System } from "../src/system.ts";
import type { ClrRecord, LogRecord } from "../src/types.ts";

function dir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "ptl-test-rec-"));
}

function clrs(log: LogRecord[]): ClrRecord[] {
  return log.filter((r) => r.type === "CLR") as ClrRecord[];
}

test("restart in the middle of rollbackTo: recovery finishes the undo via CLRs", () => {
  const work = dir();
  let sys = System.open(work);
  const t0 = sys.begin();
  sys.update(t0, "p", "base");
  sys.commit(t0);

  const t = sys.begin();
  sys.update(t, "p", "u1");
  sys.savepoint(t, "sp");
  sys.update(t, "p", "u2");
  sys.update(t, "p", "u3");
  sys.update(t, "p", "u4");

  sys.armCrashAfterAppends(2); // 2 of 3 CLRs become durable, then "power loss"
  assert.throws(() => sys.rollbackTo(t, "sp"), CrashError);

  sys = System.open(work); // restart on the same durable state
  const report = sys.recoveryReport;
  assert.deepEqual(report.loserTxns, [t]);
  assert.equal(sys.readPage("p"), "base"); // loser fully undone

  // every UPDATE compensated exactly once: pre-crash CLRs were skipped, not redone
  const all = clrs(sys.log.all());
  assert.equal(all.length, 4);
  assert.equal(new Set(all.map((c) => c.undidLSN)).size, 4);
});

test("crash mid-abort: recovery does not re-undo compensated updates", () => {
  const work = dir();
  let sys = System.open(work);
  const t = sys.begin();
  sys.update(t, "p", "a");
  sys.update(t, "p", "b");
  sys.update(t, "p", "c");

  sys.armCrashAfterAppends(2); // ABORT + 1 CLR durable, then crash
  assert.throws(() => sys.abort(t), CrashError);

  sys = System.open(work);
  assert.equal(sys.readPage("p"), "");
  const all = clrs(sys.log.all());
  assert.equal(all.length, 3);
  assert.equal(new Set(all.map((c) => c.undidLSN)).size, 3);
});

test("half-written checkpoint is ignored; recovery falls back to the last complete one", () => {
  const work = dir();
  let sys = System.open(work);
  const t1 = sys.begin();
  sys.update(t1, "x", "one");
  sys.commit(t1);
  const ckpt1 = sys.checkpoint();

  const t2 = sys.begin();
  sys.update(t2, "x", "two");
  sys.commit(t2);

  sys.armTornNextAppend(); // half-write CKPT_BEGIN of checkpoint #2
  sys.checkpoint();

  sys = System.open(work);
  const report = sys.recoveryReport;
  assert.equal(report.tornTailDetected, true); // half-written checkpoint frame found
  assert.equal(report.startedFromCheckpoint, ckpt1.beginLSN); // fell back to ckpt1
  assert.equal(sys.readPage("x"), "two"); // redo from ckpt1 DPT still correct
});

test("redo replays committed updates lost with the volatile buffer pool", () => {
  const work = dir();
  let sys = System.open(work);
  const t = sys.begin();
  sys.update(t, "p", "durable-via-log");
  sys.commit(t); // commit forces no page flush; pages only flush at checkpoint
  // "crash" without checkpoint: committed-dirty page was never flushed
  sys = System.open(work);
  assert.equal(sys.readPage("p"), "durable-via-log");
  assert.ok(sys.recoveryReport.redoneRecords >= 1);
});
