import fs from "node:fs";
import path from "node:path";
import { FileLayer, CrashError } from "./filelayer.ts";
import { LogManager } from "./log.ts";
import { PageStore } from "./pagestore.ts";
import { LockManager, LockConflictError } from "./locks.ts";
import { TransactionManager } from "./txn.ts";
import { recover } from "./recovery.ts";
import { computeRetentionBoundary, executeTruncation } from "./truncate.ts";
import { rebuildFinalPages, comparePages } from "./rebuild.ts";
import type { LogRecord } from "./types.ts";

const RUN_DIR = path.resolve("run");

function freshDir(name: string): string {
  const dir = path.join(RUN_DIR, name);
  fs.rmSync(dir, { recursive: true, force: true });
  fs.mkdirSync(dir, { recursive: true });
  return dir;
}

function stack(dir: string, capacity = 8) {
  const fl = new FileLayer(dir, capacity);
  const log = new LogManager(fl);
  const pages = new PageStore(dir);
  const locks = new LockManager();
  const tm = new TransactionManager(log, pages, locks);
  return { fl, log, pages, locks, tm };
}

function showCommitted(pages: PageStore, ids: string[]): string {
  return ids.map((p) => `${p}=${pages.readCommitted(p)}`).join(" ");
}

/* ------------------------------------------------------------------ */
function scenarioDemo(): void {
  console.log("\n=== scenario: demo (savepoints, rollback-then-commit, interleaved txns) ===");
  const dir = freshDir("demo");
  const { log, pages, tm, locks } = stack(dir);

  const t1 = tm.begin();
  const t2 = tm.begin();
  tm.update(t1, "A", 100);
  tm.savepoint(t1, "base");
  tm.update(t1, "A", 200);
  tm.update(t1, "B", 50);
  tm.savepoint(t1, "mid");
  tm.update(t1, "A", 300);
  tm.update(t2, "C", 7);
  console.log(`interleaved: T1 wrote A,B  T2 wrote C   committed view: ${showCommitted(pages, ["A", "B", "C"])}`);

  const r1 = tm.rollbackTo(t1, "mid");
  console.log(`T1 rollbackTo(mid): undid ${r1.undone} update(s), CLRs at lsn [${r1.clrs}]`);
  const r2 = tm.rollbackTo(t1, "mid");
  console.log(`T1 rollbackTo(mid) again: undid ${r2.undone} (idempotent)`);

  tm.update(t1, "A", 400); // new update after rollback
  tm.savepoint(t1, "base"); // reuse name: overwrites old binding
  tm.update(t1, "A", 450);
  const r3 = tm.rollbackTo(t1, "base");
  console.log(`savepoint name "base" reused; rollbackTo(base) now undoes ${r3.undone} update(s) back to the new binding`);
  tm.update(t1, "A", 500);

  try {
    tm.update(t2, "A", 999);
  } catch (e) {
    if (e instanceof LockConflictError) console.log(`T2 update A blocked: ${e.message}`);
  }

  tm.commit(t1);
  tm.commit(t2);
  console.log(`after both commit:        committed view: ${showCommitted(pages, ["A", "B", "C"])}`);
  console.log(`lock on A released: holder=${locks.holder("A") ?? "none"}`);

  const t3 = tm.begin();
  tm.update(t3, "A", 999);
  console.log(`T3 uncommitted A=999; external reader still sees A=${pages.readCommitted("A")}`);
  tm.abort(t3);
  console.log(`T3 aborted; A=${pages.readCommitted("A")} (CLR-driven undo, no dirty data leaked)`);
  console.log(`log now has ${log.records.length} records`);
}

/* ------------------------------------------------------------------ */
function scenarioCrashRollback(): void {
  console.log("\n=== scenario: crash-rollback (restart in the middle of rollbackTo) ===");
  const dir = freshDir("crash-rollback");
  let { fl, log, pages, tm } = stack(dir);

  const t1 = tm.begin();
  for (let i = 1; i <= 5; i++) tm.update(t1, `P${i}`, i * 10);
  tm.savepoint(t1, "sp");
  for (let i = 1; i <= 5; i++) tm.update(t1, `P${i}`, i * 100);
  const t2 = tm.begin();
  tm.update(t2, "Q", 1);
  tm.commit(t2);
  pages.flushEligible();
  pages.persistDisk();

  fl.crashAfterAppends = 2; // die after 2 CLRs of the rollback
  let crashed = false;
  try {
    tm.rollbackTo(t1, "sp");
  } catch (e) {
    if (e instanceof CrashError) crashed = true;
  }
  console.log(`crash injected during rollbackTo(sp): crashed=${crashed}, CLRs durable before crash: 2`);

  // restart
  fl.revive();
  ({ log, pages, tm } = stack(dir));
  const report = recover(log, pages);
  console.log(
    `recovery: checkpoint=${report.analysis.checkpointUsed} losers=[${report.analysis.losers}] ` +
      `redo applied=${report.redo.applied} undo wrote ${report.undo.clrsWritten.length} more CLRs`,
  );
  const clrs = log.records.filter((r) => r.type === "clr" && r.txn === t1);
  console.log(
    `T1 total CLRs=${clrs.length} (== its 10 updates, each compensated exactly once); ` +
      `final: ${["P1", "P2", "P3", "P4", "P5"].map((p) => `${p}=${pages.readCommitted(p)}`).join(" ")} Q=${pages.readCommitted("Q")}`,
  );
}

/* ------------------------------------------------------------------ */
function scenarioTornCheckpoint(): void {
  console.log("\n=== scenario: torn-checkpoint (half-written checkpoint end) ===");
  const dir = freshDir("torn-checkpoint");
  const { fl, log, pages, tm } = stack(dir);

  const t1 = tm.begin();
  tm.update(t1, "A", 1);
  tm.commit(t1);
  const good = tm.checkpoint();
  console.log(`complete checkpoint written at lsn ${good}`);

  const t2 = tm.begin();
  tm.update(t2, "B", 2);
  fl.tornNextAppend = true; // the chkpt_end record will be half-written
  const begin = log.append({ type: "chkpt_begin" });
  log.append({ type: "chkpt_end", beginLSN: begin.lsn, txns: tm.txnTable(), dirtyPages: pages.dirtyPageTable() });
  console.log(`second checkpoint end record torn (half-written to disk)`);

  // reload from disk: the torn record is reported as an anomaly and skipped
  const log2 = new LogManager(fl);
  console.log(`reloaded log anomalies: [${log2.anomalies.join("; ")}]`);
  const last = log2.lastCompleteCheckpoint();
  console.log(`lastCompleteCheckpoint() after reload -> lsn ${last ? last.lsn : "null"} (torn one ignored)`);

  const decision = computeRetentionBoundary({
    activeTxns: tm.txnTable(),
    dirtyPages: pages.dirtyPageTable(),
    lastCheckpoint: last,
  });
  for (const line of decision.rationale) console.log(`  ${line}`);
  const pages2 = new PageStore(dir);
  const report = recover(log2, pages2);
  console.log(
    `recovery anchored at checkpoint lsn ${report.analysis.checkpointUsed}: ` +
      `losers=[${report.analysis.losers}] A=${pages2.readCommitted("A")} B=${pages2.readCommitted("B")}`,
  );

  // second part: the ONLY checkpoint is torn -> truncation must be refused
  const dir2 = freshDir("torn-checkpoint-only");
  const s2 = stack(dir2);
  const t9 = s2.tm.begin();
  s2.tm.update(t9, "A", 1);
  s2.tm.commit(t9);
  s2.fl.tornNextAppend = true;
  const b2 = s2.log.append({ type: "chkpt_begin" });
  s2.log.append({ type: "chkpt_end", beginLSN: b2.lsn, txns: [], dirtyPages: [] });
  const log3 = new LogManager(s2.fl);
  const refused = computeRetentionBoundary({
    activeTxns: [],
    dirtyPages: [],
    lastCheckpoint: log3.lastCompleteCheckpoint(),
  });
  console.log(`only checkpoint torn -> truncation decision: ${refused.refusal}`);
}

/* ------------------------------------------------------------------ */
function scenarioTruncate(): void {
  console.log("\n=== scenario: truncate (retention boundary, recovery vs full-log rebuild) ===");
  const dir = freshDir("truncate");
  let { fl, log, pages, tm } = stack(dir, 6);

  const t2 = tm.begin();
  tm.update(t2, "A", 10);
  tm.update(t2, "B", 20);
  tm.commit(t2);
  const t3 = tm.begin();
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
  const t4 = tm.begin(); // will become a loser
  tm.update(t4, "B", 99);
  tm.update(t4, "C", 7);

  const fullLog: LogRecord[] = log.records.map((r) => ({ ...r }));
  console.log(`full log: ${fullLog.length} records, nextLSN=${log.nextLSN}`);

  const chkpt = log.lastCompleteCheckpoint();
  const decision = computeRetentionBoundary({
    activeTxns: tm.txnTable(),
    dirtyPages: pages.dirtyPageTable(),
    lastCheckpoint: chkpt,
  });
  console.log("retention decision:");
  for (const line of decision.rationale) console.log(`  ${line}`);

  const aggressive = computeRetentionBoundary({
    activeTxns: tm.txnTable(),
    dirtyPages: pages.dirtyPageTable(),
    lastCheckpoint: chkpt,
    requested: log.nextLSN - 1,
  });
  console.log(`  over-aggressive request -> ${aggressive.refusal}`);

  const res = executeTruncation(fl, decision);
  console.log(
    `truncated: removed [${res.removedSegments.join(", ") || "none"}], ` +
      `boundary=${res.boundary}, oldest retained segment starts at lsn ${res.retainedFrom}`,
  );

  // restart from the truncated log and recover
  ({ log, pages, tm } = stack(dir, 6));
  const report = recover(log, pages);
  console.log(
    `recovery from truncated log: checkpoint=${report.analysis.checkpointUsed} ` +
      `losers=[${report.analysis.losers}] redo applied=${report.redo.applied} undo CLRs=${report.undo.clrsWritten.length}`,
  );

  const expected = rebuildFinalPages(fullLog);
  const cmp = comparePages(expected, pages.committedSnapshot());
  console.log(
    `independent rebuild from full log vs recovery from truncated log: ${cmp.match ? "MATCH" : "MISMATCH"}`,
  );
  if (!cmp.match) for (const d of cmp.diffs) console.log(`  ${d}`);
  console.log(`final committed pages: ${JSON.stringify(pages.committedSnapshot())}`);
}

/* ------------------------------------------------------------------ */
const scenarios: Record<string, () => void> = {
  demo: scenarioDemo,
  "crash-rollback": scenarioCrashRollback,
  "torn-checkpoint": scenarioTornCheckpoint,
  truncate: scenarioTruncate,
};

const which = process.argv[2] ?? "all";
if (which === "all") {
  for (const fn of Object.values(scenarios)) fn();
} else if (scenarios[which]) {
  scenarios[which]();
} else {
  console.error(`unknown scenario "${which}". Available: ${Object.keys(scenarios).join(", ")}, all`);
  process.exit(1);
}
