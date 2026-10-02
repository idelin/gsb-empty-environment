import path from 'node:path';
import { Wal } from './wal.ts';
import { Storage } from './storage.ts';
import { BufferPool } from './buffer.ts';
import type { JsonValue, LSN, PageId, TxEntry, TxId } from './types.ts';

export interface RecoveryReport {
  analysis: {
    checkpointLSN: LSN;
    winners: TxId[];
    losers: TxId[];
    dpt: Record<PageId, LSN>;
  };
  redo: { fromLSN: LSN; replayed: number };
  undo: { losers: TxId[]; clrCount: number };
}

/**
 * 基于 WAL 的单机事务持久化与恢复管理器（ARIES 算法）。
 *
 * 正常运行：
 * - 每个写操作先 append 日志（前/后镜像），再修改缓冲池中的页；
 * - 提交时 flush 日志（NO-FORCE：不强制刷页）；
 * - 脏页刷盘前先 flush 对应日志（STEAL：允许未提交数据落盘）。
 *
 * 崩溃恢复（Database.recover）：
 * 1. Analysis —— 从最近的检查点扫描 WAL，重建事务表与脏页表（DPT），区分 winner/loser；
 * 2. Redo     —— 从 DPT 中最小 recLSN 起正向重放所有 UPDATE/CLR（重复历史，含未提交事务）；
 * 3. Undo     —— 对 loser 事务沿 prevLSN 链逆序回滚，写 CLR 保证幂等，最后写 END。
 */
export class Database {
  private wal: Wal;
  private storage: Storage;
  private buffer: BufferPool;
  private txTable = new Map<TxId, TxEntry>();
  private dpt = new Map<PageId, LSN>();
  private nextTxId = 1;
  private crashed = false;
  readonly dir: string;

  private constructor(dir: string) {
    this.dir = dir;
    this.wal = new Wal(path.join(dir, 'wal.log'));
    this.storage = new Storage(path.join(dir, 'data.json'));
    this.buffer = new BufferPool(this.storage, this.wal);
    for (const rec of this.wal.scan()) {
      if ('txId' in rec) this.nextTxId = Math.max(this.nextTxId, rec.txId + 1);
    }
  }

  /** 打开数据库（全新目录，或上次已正常关闭/已恢复）。 */
  static open(dir: string): Database {
    return new Database(dir);
  }

  /** 崩溃后重启：执行 ARIES 三阶段恢复，然后返回可用实例。 */
  static recover(dir: string): { db: Database; report: RecoveryReport } {
    const db = new Database(dir);
    const report = db.aries();
    return { db, report };
  }

  // ---------------- 正常事务处理 ----------------

  begin(): TxId {
    this.assertAlive();
    const txId = this.nextTxId++;
    const rec = this.wal.append({ type: 'BEGIN', txId, prevLSN: 0 });
    this.txTable.set(txId, { status: 'running', lastLSN: rec.lsn });
    return txId;
  }

  /** 写操作：先记日志（前/后镜像），后改内存页。 */
  update(txId: TxId, pageId: PageId, key: string, value: JsonValue): void {
    this.assertAlive();
    const tx = this.mustTx(txId);
    const before = this.buffer.read(pageId, key);
    const rec = this.wal.append({
      type: 'UPDATE',
      txId,
      prevLSN: tx.lastLSN,
      pageId,
      key,
      before,
      after: value,
    });
    tx.lastLSN = rec.lsn;
    this.buffer.apply(pageId, key, value, rec.lsn);
    if (!this.dpt.has(pageId)) this.dpt.set(pageId, rec.lsn);
  }

  /** 提交：写 COMMIT 并强制日志落盘（持久化点），随后写 END。 */
  commit(txId: TxId): void {
    this.assertAlive();
    const tx = this.mustTx(txId);
    const rec = this.wal.append({ type: 'COMMIT', txId, prevLSN: tx.lastLSN });
    this.wal.flush();
    tx.status = 'committed';
    tx.lastLSN = rec.lsn;
    this.wal.append({ type: 'END', txId, prevLSN: rec.lsn });
    this.txTable.delete(txId);
  }

  /** 主动回滚：写 ABORT，逆序撤销（写 CLR），最后写 END。 */
  abort(txId: TxId): void {
    this.assertAlive();
    const tx = this.mustTx(txId);
    const rec = this.wal.append({ type: 'ABORT', txId, prevLSN: tx.lastLSN });
    tx.status = 'aborting';
    tx.lastLSN = rec.lsn;
    this.undoTransaction(txId, tx);
    this.txTable.delete(txId);
  }

  read(pageId: PageId, key: string): JsonValue | undefined {
    this.assertAlive();
    return this.buffer.read(pageId, key);
  }

  /** 刷出指定脏页（模拟缓冲池置换 / STEAL）。 */
  flushPage(pageId: PageId): void {
    this.assertAlive();
    this.buffer.flushPage(pageId);
    this.dpt.delete(pageId);
  }

  flushAll(): void {
    this.assertAlive();
    for (const pageId of this.buffer.dirtyPageIds()) this.flushPage(pageId);
  }

  /** 模糊检查点：把当前事务表与 DPT 快照写入 WAL。 */
  checkpoint(): void {
    this.assertAlive();
    const txTable: Record<string, TxEntry> = {};
    for (const [id, entry] of this.txTable) txTable[id] = { ...entry };
    const dpt: Record<string, LSN> = {};
    for (const [pageId, lsn] of this.dpt) dpt[pageId] = lsn;
    this.wal.append({ type: 'CHECKPOINT', txTable, dpt });
    this.wal.flush();
  }

  /**
   * 模拟突然断电：丢弃全部易失状态（缓冲池、事务表、DPT、未 flush 的日志缓冲）。
   * 之后本实例不可再使用，请用 Database.recover(dir) 重启。
   */
  crash(): void {
    this.buffer.discard();
    this.txTable.clear();
    this.dpt.clear();
    this.crashed = true;
  }

  close(): void {
    this.flushAll();
    this.wal.close();
  }

  // ---------------- ARIES 恢复 ----------------

  private aries(): RecoveryReport {
    // ---------- 阶段 1：Analysis ----------
    // 从最近的检查点出发扫描 WAL，重建事务表与 DPT。
    const checkpointLSN = this.wal.lastCheckpointLSN();
    if (checkpointLSN > 0) {
      const cp = this.wal.get(checkpointLSN);
      if (cp.type === 'CHECKPOINT') {
        for (const [id, entry] of Object.entries(cp.txTable)) {
          this.txTable.set(Number(id), { ...entry });
        }
        for (const [pageId, lsn] of Object.entries(cp.dpt)) this.dpt.set(pageId, lsn);
      }
    }
    for (const rec of this.wal.scan(checkpointLSN > 0 ? checkpointLSN + 1 : 1)) {
      switch (rec.type) {
        case 'BEGIN':
          this.txTable.set(rec.txId, { status: 'running', lastLSN: rec.lsn });
          break;
        case 'UPDATE':
        case 'CLR': {
          const tx = this.txTable.get(rec.txId) ?? { status: 'running' as const, lastLSN: 0 };
          tx.lastLSN = rec.lsn;
          this.txTable.set(rec.txId, tx);
          if (!this.dpt.has(rec.pageId)) this.dpt.set(rec.pageId, rec.lsn);
          break;
        }
        case 'COMMIT': {
          const tx = this.mustTx(rec.txId);
          tx.status = 'committed';
          tx.lastLSN = rec.lsn;
          break;
        }
        case 'ABORT': {
          const tx = this.mustTx(rec.txId);
          tx.status = 'aborting';
          tx.lastLSN = rec.lsn;
          break;
        }
        case 'END':
          this.txTable.delete(rec.txId);
          break;
        case 'CHECKPOINT':
          break;
      }
    }
    const winners = [...this.txTable.entries()]
      .filter(([, e]) => e.status === 'committed')
      .map(([id]) => id);
    const losers = [...this.txTable.entries()]
      .filter(([, e]) => e.status !== 'committed')
      .map(([id]) => id);
    const dptSnapshot = Object.fromEntries(this.dpt);

    // ---------- 阶段 2：Redo ----------
    // 从 DPT 中最小 recLSN 开始正向扫描，重放所有 UPDATE/CLR（重复历史）。
    // 仅当日志记录在 DPT 覆盖范围内、且页上 pageLSN 落后于记录 LSN 时才重放。
    const redoLSN = this.dpt.size > 0 ? Math.min(...this.dpt.values()) : 0;
    let replayed = 0;
    if (redoLSN > 0) {
      for (const rec of this.wal.scan(redoLSN)) {
        if (rec.type !== 'UPDATE' && rec.type !== 'CLR') continue;
        const recLSN = this.dpt.get(rec.pageId);
        if (recLSN === undefined || recLSN > rec.lsn) continue;
        const page = this.buffer.fetch(rec.pageId);
        if (page.pageLSN >= rec.lsn) continue;
        this.buffer.apply(rec.pageId, rec.key, rec.after, rec.lsn);
        replayed++;
      }
    }

    // ---------- 阶段 3：Undo ----------
    // winner（已提交但未写 END）补写 END；loser 进入全局逆序回滚。
    let clrCount = 0;
    const chains = new Map<TxId, LSN>(); // loser txId -> 当前待处理链头 LSN
    for (const [txId, tx] of [...this.txTable.entries()]) {
      if (tx.status === 'committed') {
        this.wal.append({ type: 'END', txId, prevLSN: tx.lastLSN });
        this.txTable.delete(txId);
      } else {
        chains.set(txId, tx.lastLSN);
      }
    }
    // 标准 ARIES Undo：反复选取所有 loser 链中最大的 LSN 处理，
    // 保证交错事务严格按时间逆序回滚（避免前镜像互相覆盖）。
    while (chains.size > 0) {
      let maxTx: TxId = -1;
      let maxLSN: LSN = 0;
      for (const [txId, lsn] of chains) {
        if (lsn > maxLSN) {
          maxLSN = lsn;
          maxTx = txId;
        }
      }
      const tx = this.txTable.get(maxTx)!;
      const rec = this.wal.get(maxLSN);
      let next: LSN;
      if (rec.type === 'UPDATE') {
        const clr = this.wal.append({
          type: 'CLR',
          txId: maxTx,
          prevLSN: tx.lastLSN,
          pageId: rec.pageId,
          key: rec.key,
          after: rec.before,
          undoNextLSN: rec.prevLSN,
        });
        this.buffer.apply(rec.pageId, rec.key, rec.before, clr.lsn);
        if (!this.dpt.has(rec.pageId)) this.dpt.set(rec.pageId, clr.lsn);
        tx.lastLSN = clr.lsn;
        clrCount++;
        next = rec.prevLSN;
      } else if (rec.type === 'CLR') {
        // 已撤销过的记录直接跳过（崩溃发生在撤销中途时保证幂等）。
        next = rec.undoNextLSN;
      } else {
        next = 'prevLSN' in rec ? rec.prevLSN : 0;
      }
      if (next === 0) {
        this.wal.append({ type: 'END', txId: maxTx, prevLSN: tx.lastLSN });
        this.txTable.delete(maxTx);
        chains.delete(maxTx);
      } else {
        chains.set(maxTx, next);
      }
    }

    // 恢复完成：刷出全部脏页并强制日志落盘。
    this.buffer.flushAll();
    this.dpt.clear();
    this.wal.flush();

    return {
      analysis: {
        checkpointLSN,
        winners,
        losers,
        dpt: dptSnapshot,
      },
      redo: { fromLSN: redoLSN, replayed },
      undo: { losers, clrCount },
    };
  }

  /** 沿 prevLSN 链逆序撤销一个事务，返回写入的 CLR 数量。 */
  private undoTransaction(txId: TxId, tx: TxEntry): number {
    let clrCount = 0;
    let lsn = tx.lastLSN;
    while (lsn !== 0) {
      const rec = this.wal.get(lsn);
      if (rec.type === 'UPDATE') {
        const clr = this.wal.append({
          type: 'CLR',
          txId,
          prevLSN: tx.lastLSN,
          pageId: rec.pageId,
          key: rec.key,
          after: rec.before,
          undoNextLSN: rec.prevLSN,
        });
        this.buffer.apply(rec.pageId, rec.key, rec.before, clr.lsn);
        if (!this.dpt.has(rec.pageId)) this.dpt.set(rec.pageId, clr.lsn);
        tx.lastLSN = clr.lsn;
        clrCount++;
        lsn = rec.prevLSN;
      } else if (rec.type === 'CLR') {
        // 已撤销过的记录直接跳过（崩溃发生在撤销中途时保证幂等）。
        lsn = rec.undoNextLSN;
      } else {
        lsn = 'prevLSN' in rec ? rec.prevLSN : 0;
      }
    }
    const end = this.wal.append({ type: 'END', txId, prevLSN: tx.lastLSN });
    tx.lastLSN = end.lsn;
    return clrCount;
  }

  // ---------------- 工具 ----------------

  private mustTx(txId: TxId): TxEntry {
    const tx = this.txTable.get(txId);
    if (!tx) throw new Error(`transaction ${txId} is not active`);
    return tx;
  }

  private assertAlive(): void {
    if (this.crashed) throw new Error('database instance has crashed; reopen via Database.recover()');
  }
}
