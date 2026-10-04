import { DiskStorage, BufferPool } from './storage.ts';
import { Wal } from './wal.ts';
import { TransactionManager } from './txn-manager.ts';
import type { PageId } from './types.ts';

export interface CrashScenario {
  walPath: string;
  /** 崩溃瞬间的磁盘镜像（缓冲池已丢失） */
  crashedDisk: DiskStorage;
  /** 恢复完成后的期望页面值 */
  expected: Record<PageId, number>;
}

/**
 * 验收场景：5 个事务（T2、T4 已提交；T1、T3、T5 未提交），
 * 在 Checkpoint 之后崩溃。WAL 布局：
 *   1  T1 UPDATE A 0->10        9  T4 UPDATE E 0->50
 *   2  T2 UPDATE B 0->20       10  T3 UPDATE B 20->60
 *   3  T3 UPDATE C 0->30       11  T5 UPDATE D 40->70
 *   4  T1 UPDATE D 0->40       12  T4 COMMIT
 *   5  T2 COMMIT               13  T4 END
 *   6  T2 END                  14  T1 UPDATE E 50->80
 *   7  BEGIN_CHECKPOINT        -- 崩溃前仅刷出 A(1)、B(2)、C(3)
 *   8  END_CHECKPOINT
 */
export async function buildCrashScenario(walPath: string): Promise<CrashScenario> {
  const disk = new DiskStorage({ A: 0, B: 0, C: 0, D: 0, E: 0 });
  const pool = new BufferPool(disk);
  const wal = Wal.create(walPath);
  const tm = new TransactionManager(wal, pool);

  await tm.update('T1', 'A', 10);
  await tm.update('T2', 'B', 20);
  await tm.update('T3', 'C', 30);
  await tm.update('T1', 'D', 40);
  await tm.commit('T2'); // 已提交事务 #1

  pool.flushPage('B'); // 已提交数据落盘
  await tm.checkpoint();

  await tm.update('T4', 'E', 50);
  await tm.update('T3', 'B', 60);
  await tm.update('T5', 'D', 70);
  await tm.commit('T4'); // 已提交事务 #2
  await tm.update('T1', 'E', 80);

  pool.flushPage('A');
  pool.flushPage('C');
  await tm.quiesce();
  // 崩溃：缓冲池（含 D、E 的脏页与 B 的新值）整体丢失
  return {
    walPath,
    crashedDisk: disk,
    expected: { A: 0, B: 20, C: 0, D: 0, E: 50 },
  };
}
