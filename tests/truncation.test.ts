import { test } from "node:test";
import assert from "node:assert/strict";
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import { rebuildFromLog, snapshotsEqual } from "../src/rebuild.ts";
import { System, TruncationRefusedError } from "../src/system.ts";
import type { LogRecord } from "../src/types.ts";

function dir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), "ptl-test-trunc-"));
}

test("retention boundary considers active txns, dirty-page redo and last checkpoint", () => {
  const sys = System.open(dir(), { maxRecordsPerSegment: 5 });

  const t1 = sys.begin();
  sys.update(t1, "p1", "x");
  sys.commit(t1);
  const ckpt = sys.checkpoint(); // flushes p1; DPT empty afterwards

  const t2 = sys.begin();
  const dirtyLSN = sys.update(t2, "p2", "y");
  sys.commit(t2); // p2 committed-dirty, not yet flushed -> redo start

  const t3 = sys.begin(); // still active: its whole chain must be retained
  sys.update(t3, "p3", "z");
  const activeFirstLSN = sys.txns.handle(t3).firstLSN;

  const plan = sys.planTruncation();
  const expected = Math.min(ckpt.beginLSN, dirtyLSN, activeFirstLSN);
  assert.equal(plan.boundary, expected);
  assert.ok(plan.reasons.some((r) => r.includes("active txn 3")));
  assert.ok(plan.reasons.some((r) => r.includes('dirty page "p2"')));
  assert.ok(plan.reasons.some((r) => r.includes("last complete checkpoint")));
  assert.ok(plan.reasons.some((r) => r.includes("<-- binding")));
});

test("truncation beyond the safe boundary is refused", () => {
  const sys = System.open(dir(), { maxRecordsPerSegment: 5 });
  const t = sys.begin();
  sys.update(t, "p", "v"); // active txn pins the log from its firstLSN
  const plan = sys.planTruncation();
  assert.throws(() => sys.truncateLog(plan.boundary + 1), TruncationRefusedError);
  // nothing was deleted
  assert.ok(sys.log.store.listSegments().length > 0);
  assert.equal(sys.log.firstAvailableLSN, 1);
});

test("recovery after truncation matches independent rebuild from the full log", () => {
  const work = dir();
  let sys = System.open(work, { maxRecordsPerSegment: 6 });

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
  sys.commit(t3);
  sys.checkpoint();

  const t4 = sys.begin(); // stays active across the truncation
  sys.update(t4, "p4", "t4-uncommitted");

  const fullLog: LogRecord[] = [...sys.log.all()];
  const plan = sys.planTruncation();
  const segmentsBefore = sys.log.store.listSegments().length;
  const { removed } = sys.truncateLog();
  assert.ok(removed.length > 0, "expected at least one segment to be reclaimed");
  assert.ok(sys.log.store.listSegments().length < segmentsBefore);

  sys = System.open(work, { maxRecordsPerSegment: 6 }); // restart on truncated log
  assert.equal(sys.log.firstAvailableLSN, plan.boundary);
  assert.deepEqual(sys.recoveryReport.loserTxns, [t4]); // active txn aborted by recovery

  const recovered = sys.pages.committedSnapshot();
  const rebuilt = rebuildFromLog(fullLog);
  const cmp = snapshotsEqual(rebuilt, recovered);
  assert.deepEqual(cmp.diffs, []);
  assert.ok(cmp.equal);
  assert.equal(recovered["p1"], "t1-c");
  assert.equal(recovered["p2"], "t2-b");
  assert.equal(recovered["p3"], "t3-a");
  assert.equal(recovered["p4"] ?? "", ""); // t4 never committed
});

test("with no complete checkpoint, reclamation is refused as far as recovery needs", () => {
  const sys = System.open(dir(), { maxRecordsPerSegment: 4 });
  const t = sys.begin();
  sys.update(t, "p", "v");
  sys.commit(t); // committed-dirty, never checkpointed
  const plan = sys.planTruncation();
  assert.ok(plan.reasons.some((r) => r.includes("no complete checkpoint")));
  assert.equal(plan.boundary, 1); // everything must be retained
  assert.throws(() => sys.truncateLog(2), TruncationRefusedError);
});
