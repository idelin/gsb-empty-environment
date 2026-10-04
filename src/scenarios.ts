// Scripted end-to-end scenarios, shared by the CLI and used as living docs.

import * as fs from "node:fs";
import { CrashError } from "./fileLayer.ts";
import { rebuildFromLog, snapshotsEqual } from "./rebuild.ts";
import { System, TruncationRefusedError } from "./system.ts";
import type { LogRecord } from "./types.ts";

export function freshDir(base: string, name: string): string {
  const dir = `${base}/${name}`;
  fs.rmSync(dir, { recursive: true, force: true });
  fs.mkdirSync(dir, { recursive: true });
  return dir;
}

export function scenarioNestedSavepoints(dir: string) {
  const sys = System.open(dir);
  const t = sys.begin();
  sys.update(t, "p1", "v1");
  sys.savepoint(t, "sp1");
  sys.update(t, "p1", "v2");
  sys.savepoint(t, "sp2");
  sys.update(t, "p1", "v3");

  const clr1 = sys.rollbackTo(t, "sp2"); // undo v3
  const clr2 = sys.rollbackTo(t, "sp2"); // idempotent: no new CLRs
  const clr3 = sys.rollbackTo(t, "sp1"); // undo v2 (sp2's update already compensated)

  sys.savepoint(t, "sp1"); // name reuse overwrites the old binding
  sys.update(t, "p1", "v4");
  sys.rollbackTo(t, "sp1"); // rolls back only v4 (binding moved)
  sys.update(t, "p1", "final");
  sys.commit(t);

  return {
    scenario: "nested-savepoints",
    clrsFromFirstRollback: clr1,
    clrsFromRepeatRollback: clr2,
    clrsFromOuterRollback: clr3,
    committedP1: sys.readPage("p1"),
    log: sys.log.all(),
  };
}

export function scenarioInterleaved(dir: string) {
  const sys = System.open(dir);
  const t1 = sys.begin();
  const t2 = sys.begin();
  sys.update(t1, "A", "a1");
  sys.update(t2, "B", "b1");
  sys.savepoint(t1, "s");
  sys.update(t1, "A", "a2");
  sys.update(t2, "B", "b2");

  const seenByReaderMidway = { A: sys.readPage("A"), B: sys.readPage("B") };

  let lockConflict: string | null = null;
  try {
    sys.update(t2, "A", "stolen"); // A is exclusively locked by t1
  } catch (e) {
    lockConflict = (e as Error).message;
  }

  sys.rollbackTo(t1, "s"); // A back to a1
  sys.update(t1, "A", "a3"); // new update after rollback
  sys.commit(t2);
  const seenAfterT2Commit = { A: sys.readPage("A"), B: sys.readPage("B") };
  sys.commit(t1);

  return {
    scenario: "interleaved",
    seenByReaderMidway,
    lockConflict,
    seenAfterT2Commit,
    final: { A: sys.readPage("A"), B: sys.readPage("B") },
    log: sys.log.all(),
  };
}

export function scenarioCrashMidRollback(dir: string) {
  let sys = System.open(dir);
  const t0 = sys.begin();
  sys.update(t0, "p", "base");
  sys.commit(t0);

  const t = sys.begin();
  sys.update(t, "p", "u1");
  sys.savepoint(t, "sp");
  sys.update(t, "p", "u2");
  sys.update(t, "p", "u3");
  sys.update(t, "p", "u4");

  // crash after 2 of the 3 CLRs of rollbackTo("sp") have been appended
  sys.armCrashAfterAppends(2);
  let crash: string | null = null;
  try {
    sys.rollbackTo(t, "sp");
  } catch (e) {
    if (e instanceof CrashError) crash = e.message;
    else throw e;
  }

  // restart on the same durable state; recovery finishes the rollback
  sys = System.open(dir);
  const report = sys.recoveryReport;
  return {
    scenario: "crash-mid-rollback",
    crash,
    recovery: report,
    committedP: sys.readPage("p"),
    log: sys.log.all(),
  };
}

export function scenarioTornCheckpoint(dir: string) {
  let sys = System.open(dir);
  const t1 = sys.begin();
  sys.update(t1, "x", "one");
  sys.commit(t1);
  const ckpt1 = sys.checkpoint();

  const t2 = sys.begin();
  sys.update(t2, "x", "two");
  sys.commit(t2);

  sys.armTornNextAppend(); // half-write the next record: CKPT_BEGIN of ckpt2
  const ckpt2 = sys.checkpoint();

  sys = System.open(dir); // restart
  const report = sys.recoveryReport;
  return {
    scenario: "torn-checkpoint",
    ckpt1,
    ckpt2,
    recovery: report,
    committedX: sys.readPage("x"),
    log: sys.log.all(),
  };
}

export function scenarioTruncateVerify(dir: string) {
  let sys = System.open(dir, { maxRecordsPerSegment: 6 });

  const t1 = sys.begin();
  sys.update(t1, "p1", "t1-a");
  sys.savepoint(t1, "s1");
  sys.update(t1, "p1", "t1-b");
  const t2 = sys.begin();
  sys.update(t2, "p2", "t2-a");
  sys.rollbackTo(t1, "s1");
  sys.update(t1, "p1", "t1-c");
  sys.commit(t1);
  sys.update(t2, "p2", "t2-b");
  sys.commit(t2);
  sys.checkpoint();

  const t3 = sys.begin();
  sys.update(t3, "p3", "t3-a");
  sys.commit(t3); // committed-dirty, deliberately not checkpointed: pins redo start

  const t4 = sys.begin(); // stays active: pins its whole txn chain
  sys.update(t4, "p4", "t4-uncommitted");

  // full log, kept aside for the independent rebuild
  const fullLog: LogRecord[] = [...sys.log.all()];

  const plan = sys.planTruncation();

  let refused: string | null = null;
  try {
    sys.truncateLog(plan.boundary + 3); // beyond the safe boundary
  } catch (e) {
    if (e instanceof TruncationRefusedError) refused = e.message;
    else throw e;
  }

  const truncated = sys.truncateLog(); // at the safe boundary

  // restart on the truncated log and recover
  sys = System.open(dir, { maxRecordsPerSegment: 6 });
  const recovered = sys.pages.committedSnapshot();

  // independent rebuild from the full (pre-truncation) log
  const rebuilt = rebuildFromLog(fullLog);
  const cmp = snapshotsEqual(rebuilt, recovered);

  return {
    scenario: "truncate-verify",
    plan,
    refused,
    removedSegments: truncated.removed,
    firstAvailableLSN: sys.log.firstAvailableLSN,
    recovery: sys.recoveryReport,
    recovered,
    rebuilt: Object.fromEntries([...rebuilt.entries()].sort()),
    match: cmp.equal,
    diffs: cmp.diffs,
    log: sys.log.all(),
  };
}

export const SCENARIOS: Record<string, (dir: string) => unknown> = {
  "nested-savepoints": scenarioNestedSavepoints,
  interleaved: scenarioInterleaved,
  "crash-mid-rollback": scenarioCrashMidRollback,
  "torn-checkpoint": scenarioTornCheckpoint,
  "truncate-verify": scenarioTruncateVerify,
};
