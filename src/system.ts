import * as fs from 'node:fs';
import { BufferPool, DiskStorage } from './storage.ts';
import { LockManager } from './lockManager.ts';
import { RecoveryManager, type RecoveryReport } from './recovery.ts';
import { TransactionManager, type TxHooks } from './transaction.ts';
import { WriteAheadLog } from './wal.ts';
import type { LogRecord, PageId, PageImage } from './types.ts';

export interface DBSystemOptions {
  bufferPoolCapacity?: number;
  hooks?: TxHooks;
}

/**
 * 数据库系统门面：组合 WAL / 缓冲池 / 锁管理器 / 事务管理器。
 * 崩溃模拟 = 直接丢弃整个实例（内存态全部丢失），用同一目录重新 open 即重启。
 */
export class DBSystem {
  readonly wal: WriteAheadLog;
  readonly storage: DiskStorage;
  readonly bufferPool: BufferPool;
  readonly lockManager: LockManager;
  readonly tx: TransactionManager;
  private crashed = false;
  readonly dir: string;

  private constructor(dir: string, hooks: TxHooks, bufferPoolCapacity: number) {
    this.dir = dir;
    fs.mkdirSync(dir, { recursive: true });
    this.wal = new WriteAheadLog(dir);
    this.storage = new DiskStorage(dir);
    this.bufferPool = new BufferPool(
      this.storage,
      bufferPoolCapacity,
      () => this.wal.lastLSN,
    );
    this.lockManager = new LockManager();
    this.tx = new TransactionManager(this.wal, this.bufferPool, this.lockManager, hooks);
  }

  static open(dir: string, opts: DBSystemOptions = {}): DBSystem {
    return new DBSystem(dir, opts.hooks ?? {}, opts.bufferPoolCapacity ?? 8);
  }

  /** 模糊检查点：不强制刷页，仅把事务表/脏页表快照写入 END_CHECKPOINT */
  checkpoint(): void {
    this.assertAlive();
    const begin = this.wal.append({ type: 'BEGIN_CHECKPOINT' });
    this.wal.append({
      type: 'END_CHECKPOINT',
      beginLSN: begin.lsn,
      transactionTable: this.tx.snapshotTxTable(),
      dirtyPageTable: this.tx.snapshotDirtyPageTable(),
    });
  }

  /** 刷出全部脏页并同步清理脏页表 */
  flush(): void {
    this.assertAlive();
    const flushed = this.bufferPool.flushAll();
    this.tx.onPagesFlushed(flushed);
  }

  /** 执行 ARIES 三阶段恢复，随后刷盘使恢复结果持久化。hooks 用于故障注入测试 */
  recover(hooks: { afterAppend?: (rec: LogRecord) => void } = {}): RecoveryReport {
    this.assertAlive();
    const report = new RecoveryManager(this.wal, this.bufferPool, hooks).recover();
    this.flush();
    return report;
  }

  /** 模拟崩溃：此后本实例任何操作均抛错；内存态（缓冲池/事务表）视为丢失 */
  crash(): void {
    this.crashed = true;
  }

  assertAlive(): void {
    if (this.crashed) throw new Error('system has crashed');
  }

  /** 直接读取磁盘上的页镜像（绕过缓冲池，用于验证持久化结果） */
  readPersistedPage(pageId: PageId): PageImage {
    return this.storage.readPage(pageId);
  }
}
