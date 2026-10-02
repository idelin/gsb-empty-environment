/**
 * 验收场景：构造包含 2 个已提交事务、3 个未提交事务、
 * 1 个检查点、1 个崩溃时正在 abort 的事务的 WAL，然后模拟崩溃。
 */

import { WriteAheadLog } from './log.ts';
import { BufferPool, DiskManager } from './storage.ts';
import { TransactionManager } from './txn.ts';

export interface CrashScenario {
  wal: WriteAheadLog;
  disk: DiskManager;
  committed: { t1: number; t2: number };
  losers: { t3: number; t4: number; t5: number };
  aborting: { t6: number };
}

/**
 * 时间线（LSN 见注释）：
 *   T1 提交、T2 跨检查点提交；T3/T4/T5 崩溃时未提交；T6 崩溃时 abort 到一半。
 */
export function buildCrashScenario(walPath: string | null = null): CrashScenario {
  const wal = new WriteAheadLog(walPath);
  const disk = new DiskManager();
  const pool = new BufferPool(disk);
  const tm = new TransactionManager(wal, pool);

  const t1 = tm.begin();
  tm.update(t1, 1, 10, 100);            // LSN 1
  tm.update(t1, 2, 5, 50);              // LSN 2
  tm.commit(t1);                        // LSN 3-4 (COMMIT, END)

  const t2 = tm.begin();
  tm.update(t2, 1, 20, 200);            // LSN 5
  const t3 = tm.begin();
  tm.update(t3, 1, 10, 111);            // LSN 6  未提交，覆盖 T1 的已提交值
  const t4 = tm.begin();
  tm.update(t4, 2, 7, 77);              // LSN 7  未提交

  tm.checkpoint();                      // LSN 8  TT={T2,T3,T4} DPT={p1:1, p2:2}，脏页全部落盘（含未提交数据: steal）

  tm.update(t2, 2, 8, 88);              // LSN 9
  tm.commit(t2);                        // LSN 10-11

  const t5 = tm.begin();
  tm.update(t5, 1, 30, 133);            // LSN 12 未提交
  tm.update(t5, 2, 9, 99);              // LSN 13 未提交
  tm.update(t3, 1, 40, 144);            // LSN 14 未提交

  const t6 = tm.begin();
  tm.update(t6, 1, 50, 5);              // LSN 15
  tm.update(t6, 2, 11, 11);             // LSN 16
  // T6 开始 abort 但崩溃在半途：只来得及补偿第二条更新
  const abortRec = wal.append({ type: 'ABORT', lsn: 0, txnId: t6, prevLSN: 16 }); // LSN 17
  void abortRec;
  const page2 = pool.fetchPage(2);
  wal.append({                          // LSN 18 CLR: 撤销 LSN 16
    type: 'CLR', lsn: 0, txnId: t6, prevLSN: 17,
    pageId: 2, offset: 11, after: 0, undoNextLSN: 15,
  });
  page2.write(11, 0);
  page2.pageLSN = 18;
  pool.markDirty(2, 18);

  pool.flushPage(1);                    // 部分落盘：p1 到 LSN 15；p2 的已提交写（LSN 9）将丢失
  pool.crash();                         // 崩溃：丢弃全部内存状态

  return { wal, disk, committed: { t1, t2 }, losers: { t3, t4, t5 }, aborting: { t6 } };
}

/** 恢复后磁盘上应有的最终值。 */
export const EXPECTED_FINAL: Array<[number, number, number]> = [
  [1, 10, 100], // T1 已提交（T3 的覆盖被回滚）
  [1, 20, 200], // T2 已提交
  [1, 30, 0],   // T5 未提交 -> 回滚
  [1, 40, 0],   // T3 未提交 -> 回滚
  [1, 50, 0],   // T6 abort 半途 -> 恢复时补完回滚
  [2, 5, 50],   // T1 已提交
  [2, 7, 0],    // T4 未提交 -> 回滚
  [2, 8, 88],   // T2 已提交（崩溃时丢失 -> Redo 重做）
  [2, 9, 0],    // T5 未提交 -> 回滚
  [2, 11, 0],   // T6 已在崩溃前补偿
];
