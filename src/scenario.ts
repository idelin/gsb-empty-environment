/**
 * Builds the acceptance-test crash scenario:
 *
 *   T1 (committed), T2 (committed), T3/T4/T5 (uncommitted at crash),
 *   with a CHECKPOINT in the middle and only some dirty pages flushed to
 *   disk before the "crash" (steal / no-force buffer policy).
 */

import { DiskManager } from "./storage.ts";
import { WalManager } from "./wal.ts";
import { LogType } from "./types.ts";

export interface CrashScenario {
  walPath: string;
  diskPath: string;
  expectedAfterRecovery: Record<string, string>;
}

export function buildCrashScenario(dir: string): CrashScenario {
  const walPath = `${dir}/wal.log`;
  const diskPath = `${dir}/disk.json`;
  const wal = WalManager.create(walPath);
  const disk = DiskManager.create(diskPath);

  // Base image: all pages exist on disk with their initial contents.
  disk.writePage("P1", "A", 0);
  disk.writePage("P2", "B", 0);
  disk.writePage("P3", "C", 0);
  disk.writePage("P4", "D", 0);
  disk.writePage("P5", "E", 0);

  const begin = (t: number) =>
    wal.append({ type: LogType.BEGIN, txnId: t, prevLSN: 0 }).lsn;
  const update = (t: number, prev: number, page: string, before: string, after: string) =>
    wal.append({
      type: LogType.UPDATE,
      txnId: t,
      prevLSN: prev,
      pageId: page,
      beforeImage: before,
      afterImage: after,
    }).lsn;
  const commit = (t: number, prev: number) =>
    wal.append({ type: LogType.COMMIT, txnId: t, prevLSN: prev }).lsn;

  // T1: P1 A -> A1
  begin(1);
  const t1u1 = update(1, 1, "P1", "A", "A1");
  // T2: P2 B -> B1
  begin(2);
  const t2u1 = update(2, 3, "P2", "B", "B1");
  // T3 (loser): P1 A1 -> A2
  begin(3);
  const t3u1 = update(3, 5, "P1", "A1", "A2");

  // Checkpoint with ATT + DPT snapshot.
  wal.appendCheckpoint(
    [
      [1, "RUNNING", t1u1],
      [2, "RUNNING", t2u1],
      [3, "RUNNING", t3u1],
    ],
    [
      ["P1", 2],
      ["P2", 4],
    ],
  );

  // T1: P3 C -> C1, commits. P3 is never flushed before the crash.
  const t1u2 = update(1, t1u1, "P3", "C", "C1");
  commit(1, t1u2);

  // T4 (loser): P2 B1 -> B2
  begin(4);
  const t4u1 = update(4, 10, "P2", "B1", "B2");

  // T2: P4 D -> D1, commits. P4 is flushed before the crash.
  const t2u2 = update(2, t2u1, "P4", "D", "D1");
  commit(2, t2u2);

  // T5 (loser): P5 E -> E1, never flushed.
  begin(5);
  update(5, 14, "P5", "E", "E1");

  // --- CRASH ---: only a subset of dirty pages reached disk (steal/no-force).
  disk.writePage("P1", "A2", t3u1); // T3's uncommitted value was stolen to disk
  disk.writePage("P2", "B2", t4u1); // T4's uncommitted value was stolen to disk
  disk.writePage("P4", "D1", t2u2); // T2's committed value safely flushed

  return {
    walPath,
    diskPath,
    expectedAfterRecovery: {
      P1: "A1", // T3's stolen A2 is undone back to T1's committed A1
      P2: "B1", // T4's stolen B2 is undone back to T2's committed B1
      P3: "C1", // T1 committed but unflushed: restored by REDO
      P4: "D1", // T2 committed and flushed: survives untouched
      P5: "E", // T5 uncommitted and unflushed: before image preserved
    },
  };
}
