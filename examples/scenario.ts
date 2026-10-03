/**
 * 验收场景构造器：生成一个“检查点之后崩溃”的 WAL。
 *
 * 时间线（单节点确定性时钟驱动，LSN 即时间）：
 *   T1: U(P1<-10) U(P2<-20) COMMIT          —— 已提交
 *   T2: U(P3<-30) COMMIT                    —— 已提交
 *   T3: U(P1<-11)                           —— 未提交（检查点前开始）
 *   flush P1                                —— steal：未提交数据落盘
 *   CHECKPOINT(T3 活跃, DPT={P1,P2,P3})
 *   T4: U(P2<-21)                           —— 未提交
 *   T5: U(P3<-31)                           —— 未提交
 *   T3: U(P4<-40)
 *   T5: U(P4<-41)
 *   flush P3                                —— steal：未提交数据落盘
 *   *** CRASH ***（缓冲池整体丢失）
 */
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { WalWriter } from '../src/wal.ts';
import { BufferPool, DiskStorage } from '../src/storage.ts';
import { TransactionManager } from '../src/txn-manager.ts';

export interface CrashScenario {
  dir: string;
  walPath: string;
  storagePath: string;
  txns: { committed: number[]; uncommitted: number[] };
  /** 恢复完成后各页期望值。 */
  expected: Record<string, number>;
}

export function buildCrashScenario(dir: string): CrashScenario {
  mkdirSync(dir, { recursive: true });
  const walPath = join(dir, 'wal.log');
  const storagePath = join(dir, 'db.json');

  const wal = new WalWriter(walPath);
  const buffer = new BufferPool(new DiskStorage(storagePath));
  const tm = new TransactionManager(wal, buffer);

  const t1 = tm.begin();
  tm.update(t1, 'P1', 10);
  tm.update(t1, 'P2', 20);
  tm.commit(t1);

  const t2 = tm.begin();
  tm.update(t2, 'P3', 30);
  tm.commit(t2);

  const t3 = tm.begin();
  tm.update(t3, 'P1', 11);
  buffer.flush('P1'); // steal：未提交的 11 落盘

  tm.checkpoint();

  const t4 = tm.begin();
  tm.update(t4, 'P2', 21);

  const t5 = tm.begin();
  tm.update(t5, 'P3', 31);

  tm.update(t3, 'P4', 40);
  tm.update(t5, 'P4', 41);
  buffer.flush('P3'); // steal：未提交的 31 落盘

  // 崩溃：易失缓冲池丢失；WAL 与磁盘页留存。关闭 fd 等价于掉电（不再写记录）。
  buffer.crash();
  wal.close();

  return {
    dir,
    walPath,
    storagePath,
    txns: { committed: [t1, t2], uncommitted: [t3, t4, t5] },
    expected: { P1: 10, P2: 20, P3: 30, P4: 0 },
  };
}
