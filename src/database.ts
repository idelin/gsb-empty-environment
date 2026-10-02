import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { BufferPool, type Frame } from "./buffer.ts";
import {
  DiskManager,
  PAGE_HEADER_SIZE,
  PAGE_SIZE,
} from "./disk.ts";
import { LogManager } from "./log.ts";
import { recover, undoTransaction, type RecoveryReport } from "./recovery.ts";
import type {
  DirtyPageTable,
  LSN,
  PageId,
  TxnId,
  TxnTable,
  TxnTableEntry,
} from "./types.ts";
import { NULL_LSN } from "./types.ts";

export class Database {
  readonly disk: DiskManager;
  readonly log: LogManager;
  readonly buffer: BufferPool;
  readonly txnTable: TxnTable = new Map();
  readonly dpt: DirtyPageTable = new Map();
  readonly recoveryReport: RecoveryReport;
  readonly dir: string;
  private nextTxnId: TxnId = 1;
  private crashed = false;

  private constructor(dir: string) {
    this.dir = dir;
    this.disk = new DiskManager(join(dir, "data.db"));
    this.log = new LogManager(join(dir, "wal.log"));
    this.buffer = new BufferPool(this.disk, this.log);
    this.recoveryReport = recover(this);
  }

  static open(dir: string): Database {
    mkdirSync(dir, { recursive: true });
    return new Database(dir);
  }

  restoreTxnCounter(nextTxnId: TxnId): void {
    this.nextTxnId = Math.max(this.nextTxnId, nextTxnId);
  }

  private assertAlive(): void {
    if (this.crashed) {
      throw new Error("database instance is unusable after crash()");
    }
  }

  private mustTxn(txnId: TxnId): TxnTableEntry {
    const txn = this.txnTable.get(txnId);
    if (!txn) throw new Error(`unknown or finished transaction ${txnId}`);
    return txn;
  }

  allocatePage(): PageId {
    this.assertAlive();
    return this.disk.allocatePage();
  }

  begin(): TxnId {
    this.assertAlive();
    const txnId = this.nextTxnId++;
    const lsn = this.log.append({ type: "begin", txnId, prevLSN: NULL_LSN });
    this.txnTable.set(txnId, { txnId, status: "running", lastLSN: lsn });
    return txnId;
  }

  write(txnId: TxnId, pageId: PageId, offset: number, data: Buffer): void {
    this.assertAlive();
    const txn = this.mustTxn(txnId);
    if (offset < PAGE_HEADER_SIZE || offset + data.length > PAGE_SIZE) {
      throw new Error(
        `write out of bounds: offset=${offset} length=${data.length}`,
      );
    }
    const frame = this.buffer.fetchPage(pageId);
    const before = Buffer.from(frame.data.subarray(offset, offset + data.length));
    const lsn = this.log.append({
      type: "update",
      txnId,
      prevLSN: txn.lastLSN,
      pageId,
      offset,
      before: before.toString("hex"),
      after: data.toString("hex"),
    });
    data.copy(frame.data, offset);
    this.markDirty(frame, lsn);
    txn.lastLSN = lsn;
  }

  read(pageId: PageId, offset: number, length: number): Buffer {
    const frame = this.buffer.fetchPage(pageId);
    return Buffer.from(frame.data.subarray(offset, offset + length));
  }

  markDirty(frame: Frame, lsn: LSN): void {
    frame.pageLSN = lsn;
    if (!frame.dirty) {
      frame.dirty = true;
      if (!this.dpt.has(frame.pageId)) this.dpt.set(frame.pageId, lsn);
    }
  }

  commit(txnId: TxnId): void {
    this.assertAlive();
    const txn = this.mustTxn(txnId);
    const commitLSN = this.log.append({
      type: "commit",
      txnId,
      prevLSN: txn.lastLSN,
    });
    this.log.flush(commitLSN);
    this.log.append({ type: "end", txnId, prevLSN: commitLSN });
    this.txnTable.delete(txnId);
  }

  abort(txnId: TxnId): void {
    this.assertAlive();
    const txn = this.mustTxn(txnId);
    const abortLSN = this.log.append({
      type: "abort",
      txnId,
      prevLSN: txn.lastLSN,
    });
    txn.status = "aborted";
    txn.lastLSN = abortLSN;
    undoTransaction(this, txnId);
    const endLSN = this.log.append({
      type: "end",
      txnId,
      prevLSN: txn.lastLSN,
    });
    this.txnTable.delete(txnId);
    this.log.flush(endLSN);
  }

  checkpoint(): void {
    this.assertAlive();
    this.log.append({
      type: "checkpoint",
      txnTable: [...this.txnTable.values()],
      dpt: [...this.dpt.entries()],
    });
    this.log.flush();
    this.buffer.flushAll();
  }

  shutdown(): void {
    this.assertAlive();
    this.checkpoint();
    this.log.close();
    this.disk.close();
    this.crashed = true;
  }

  crash(): void {
    this.log.close();
    this.disk.close();
    this.crashed = true;
  }
}
