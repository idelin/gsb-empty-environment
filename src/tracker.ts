import { createHasher } from './checksum.ts';
import type { Chunk } from './types.ts';

/**
 * 接收端事务完整性追踪器。
 *
 * 按序应用分片并校验：
 *  - seq 必须连续递增（乱序/丢失立即报错）
 *  - 必须以 BEGIN 开始、END 结束
 *  - 每个 DATA 分片的内容校验和必须匹配
 *  - END 携带的总行数、总分片数、全事务滚动校验和必须与实际接收一致
 */
export class TransactionTracker {
  readonly txId: string;
  private nextSeq = 0;
  private receivedRows = 0;
  private receivedChunks = 0;
  private readonly hasher = createHasher();
  private sawBegin = false;
  private finished = false;

  constructor(txId: string) {
    this.txId = txId;
  }

  get completed(): boolean {
    return this.finished;
  }

  get rows(): number {
    return this.receivedRows;
  }

  get chunks(): number {
    return this.receivedChunks;
  }

  apply(chunk: Chunk): void {
    if (chunk.txId !== this.txId) {
      throw new Error(`分片属于事务 ${chunk.txId}，当前追踪 ${this.txId}`);
    }
    if (chunk.seq !== this.nextSeq) {
      throw new Error(`分片乱序或丢失：期望 seq=${this.nextSeq}，收到 seq=${chunk.seq}`);
    }
    this.nextSeq++;

    switch (chunk.kind) {
      case 'BEGIN': {
        if (this.sawBegin) {
          throw new Error('重复的 BEGIN 分片');
        }
        this.sawBegin = true;
        break;
      }
      case 'DATA': {
        if (!this.sawBegin) {
          throw new Error('在 BEGIN 之前收到 DATA 分片');
        }
        if (this.finished) {
          throw new Error('在 END 之后收到 DATA 分片');
        }
        const chunkHasher = createHasher();
        for (const row of chunk.rows) {
          const serialized = JSON.stringify(row);
          chunkHasher.update(serialized);
          this.hasher.update(serialized);
        }
        if (chunkHasher.digest() !== chunk.checksum) {
          throw new Error(`DATA 分片校验和不匹配 (seq=${chunk.seq})`);
        }
        if (chunk.rowCount !== chunk.rows.length) {
          throw new Error(`DATA 分片行数声明不符 (seq=${chunk.seq})`);
        }
        this.receivedRows += chunk.rows.length;
        this.receivedChunks++;
        break;
      }
      case 'END': {
        if (!this.sawBegin) {
          throw new Error('在 BEGIN 之前收到 END 分片');
        }
        if (this.finished) {
          throw new Error('重复的 END 分片');
        }
        if (chunk.totalRows !== this.receivedRows) {
          throw new Error(`事务行数不一致：声明 ${chunk.totalRows}，实收 ${this.receivedRows}`);
        }
        if (chunk.totalChunks !== this.receivedChunks) {
          throw new Error(`事务分片数不一致：声明 ${chunk.totalChunks}，实收 ${this.receivedChunks}`);
        }
        if (chunk.checksum !== this.hasher.digest()) {
          throw new Error('事务整体校验和不匹配');
        }
        this.finished = true;
        break;
      }
    }
  }
}
