/**
 * The acceptance crash scenario, shared by the demo and the recovery tests.
 *
 * Timeline (pages 1..3, 8-byte slots in the page data region):
 *   T1: P1[0]="AAAAAAAA"                                   (loser)
 *   T2: P2[0]="BBBBBBBB", commit (END written)             (winner)
 *   T3: P1[8]="CCCCCCCC", P3[0]="DDDDDDDD"                 (loser)
 *   --- fuzzy checkpoint (TT + DPT snapshot) ---
 *   flush P1 (steals uncommitted T1/T3 changes to disk)
 *   flush P2 (persists committed T2 change)
 *   T4: P2[8]="EEEEEEEE", commit (END lost in crash)       (winner)
 *   T5: P3[8]="FFFFFFFF", P3[16]="IIIIIIII", abort starts,
 *       one CLR undoes P3[16], crash hits mid-abort        (loser, aborting)
 *   T1: P2[16]="GGGGGGGG"
 *   T3: P2[24]="HHHHHHHH"
 *   --- crash: WAL durable, dirty pages 2 and 3 lost ---
 *
 * Expected after recovery:
 *   P1: all zero   (A, C rolled back - they had even reached disk)
 *   P2: [0]="BBBBBBBB" (persisted), [8]="EEEEEEEE" (redone),
 *       [16] and [24] zero (rolled back)
 *   P3: all zero   (D, F rolled back; I was undone pre-crash)
 */

import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { MiniDB } from "./minidb.ts";
import { PAGE_DATA_OFFSET, PAGE_SIZE } from "./pagestore.ts";

export const SLOT = 8;

export interface CrashScenario {
  dir: string;
  walPath: string;
  dataPath: string;
  txns: { t1: number; t2: number; t3: number; t4: number; t5: number };
}

export async function buildCrashScenario(dir?: string): Promise<CrashScenario> {
  const root = dir ?? mkdtempSync(join(tmpdir(), "aries-scenario-"));
  const walPath = join(root, "wal.log");
  const dataPath = join(root, "data.db");
  const db = await MiniDB.create(walPath, dataPath);
  const t1 = db.begin();
  const t2 = db.begin();
  const t3 = db.begin();
  const t4 = db.begin();
  const t5 = db.begin();
  await db.update(t1, 1, 0 * SLOT, Buffer.from("AAAAAAAA"));
  await db.update(t2, 2, 0 * SLOT, Buffer.from("BBBBBBBB"));
  await db.commit(t2); // committed, END written
  await db.update(t3, 1, 1 * SLOT, Buffer.from("CCCCCCCC"));
  await db.update(t3, 3, 0 * SLOT, Buffer.from("DDDDDDDD"));
  await db.checkpoint();
  await db.flushPage(1); // steal: uncommitted A/C reach disk
  await db.flushPage(2); // committed B persisted
  await db.update(t4, 2, 1 * SLOT, Buffer.from("EEEEEEEE"));
  await db.commit(t4, { writeEnd: false }); // committed, END lost in crash
  await db.update(t5, 3, 1 * SLOT, Buffer.from("FFFFFFFF"));
  await db.update(t5, 3, 2 * SLOT, Buffer.from("IIIIIIII"));
  await db.abort(t5, 1); // crash mid-abort: only the last update compensated
  await db.update(t1, 2, 2 * SLOT, Buffer.from("GGGGGGGG"));
  await db.update(t3, 2, 3 * SLOT, Buffer.from("HHHHHHHH"));
  await db.crash();
  return { dir: root, walPath, dataPath, txns: { t1, t2, t3, t4, t5 } };
}

/** Expected on-disk page image after a correct recovery. */
export function expectedPage(pageId: number): Buffer {
  const page = Buffer.alloc(PAGE_SIZE);
  if (pageId === 2) {
    page.set(Buffer.from("BBBBBBBB"), PAGE_DATA_OFFSET + 0 * SLOT);
    page.set(Buffer.from("EEEEEEEE"), PAGE_DATA_OFFSET + 1 * SLOT);
  }
  return page;
}
