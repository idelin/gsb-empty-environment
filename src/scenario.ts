/**
 * Acceptance scenario: a WAL with 5 transactions that crashes after a
 * checkpoint. T1 and T2 are committed (winners); T3, T4, T5 are in flight
 * at the crash (losers). Pages P1 and P3 were flushed before the checkpoint;
 * P2 was never flushed, so its on-disk image is stale.
 *
 * Log layout (LSN -> record):
 *   1  BEGIN T1            10 UPDATE T3 P1.a 10->100
 *   2  UPDATE T1 P1.a      11 UPDATE T2 P2.b 20->21
 *   3  BEGIN T2            12 COMMIT T2
 *   4  UPDATE T2 P2.b      13 END T2
 *   5  UPDATE T1 P3.c      14 BEGIN T4
 *   6  COMMIT T1           15 UPDATE T4 P3.c 30->300
 *   7  END T1              16 BEGIN T5
 *   8  CHECKPOINT          17 UPDATE T5 P2.b 21->210
 *   9  BEGIN T3            18 UPDATE T3 P1.d null->7
 *  --- crash ---
 */

import { join } from 'node:path';
import { Storage } from './storage.ts';
import { Wal } from './wal.ts';

export interface CrashScenario {
  wal: Wal;
  storage: Storage;
  walPath: string;
  dbPath: string;
}

export const SCENARIO_EXPECTED = {
  checkpointLSN: 8,
  losers: ['T3', 'T4', 'T5'],
  winners: ['T1', 'T2'],
  redoApplied: [4, 10, 11, 15, 17, 18],
  finalPages: {
    P1: { a: 10 },
    P2: { b: 21 },
    P3: { c: 30 },
  },
} as const;

/** Build the WAL and the crashed on-disk image, then reopen both from disk. */
export async function buildCrashScenario(dir: string): Promise<CrashScenario> {
  const walPath = join(dir, 'wal.log');
  const dbPath = join(dir, 'db.json');

  const wal = await Wal.create(walPath);
  await Storage.create(dbPath, { P1: { a: 0 }, P2: { b: 0 }, P3: { c: 0 } });
  const storage = await Storage.open(dbPath);

  await wal.append({ kind: 'BEGIN', txnId: 'T1' });
  await wal.append({ kind: 'UPDATE', txnId: 'T1', pageId: 'P1', key: 'a', before: 0, after: 10 });
  await wal.append({ kind: 'BEGIN', txnId: 'T2' });
  await wal.append({ kind: 'UPDATE', txnId: 'T2', pageId: 'P2', key: 'b', before: 0, after: 20 });
  await wal.append({ kind: 'UPDATE', txnId: 'T1', pageId: 'P3', key: 'c', before: 0, after: 30 });
  await wal.append({ kind: 'COMMIT', txnId: 'T1' });
  await wal.append({ kind: 'END', txnId: 'T1' });

  // Simulate a buffer-pool flush of P1 and P3 (only) before the checkpoint.
  storage.setValue('P1', 'a', 10, 2);
  storage.setValue('P3', 'c', 30, 5);
  await storage.flush();

  await wal.append({
    kind: 'CHECKPOINT',
    txnId: null,
    txnTable: [{ txnId: 'T2', status: 'running', lastLSN: 4 }],
    dirtyPageTable: [
      { pageId: 'P2', recLSN: 4 },
      { pageId: 'P3', recLSN: 5 },
    ],
  });

  await wal.append({ kind: 'BEGIN', txnId: 'T3' });
  await wal.append({ kind: 'UPDATE', txnId: 'T3', pageId: 'P1', key: 'a', before: 10, after: 100 });
  await wal.append({ kind: 'UPDATE', txnId: 'T2', pageId: 'P2', key: 'b', before: 20, after: 21 });
  await wal.append({ kind: 'COMMIT', txnId: 'T2' });
  await wal.append({ kind: 'END', txnId: 'T2' });
  await wal.append({ kind: 'BEGIN', txnId: 'T4' });
  await wal.append({ kind: 'UPDATE', txnId: 'T4', pageId: 'P3', key: 'c', before: 30, after: 300 });
  await wal.append({ kind: 'BEGIN', txnId: 'T5' });
  await wal.append({ kind: 'UPDATE', txnId: 'T5', pageId: 'P2', key: 'b', before: 21, after: 210 });
  await wal.append({ kind: 'UPDATE', txnId: 'T3', pageId: 'P1', key: 'd', before: null, after: 7 });

  // --- crash: discard in-memory state, reopen from disk ---
  const crashedWal = await Wal.open(walPath);
  const crashedStorage = await Storage.open(dbPath);
  return { wal: crashedWal, storage: crashedStorage, walPath, dbPath };
}
