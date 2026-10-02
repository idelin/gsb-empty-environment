/**
 * 预写日志（WAL）：LSN 单调递增的追加式日志，支持 JSON Lines 文件持久化。
 */

import { appendFileSync, readFileSync, writeFileSync } from 'node:fs';

export type LSN = number;

export type TxnStatus = 'RUNNING' | 'COMMITTED' | 'ABORTING';

export interface TxnTableEntry {
  txnId: number;
  status: TxnStatus;
  lastLSN: LSN;
}

export interface UpdateRecord {
  type: 'UPDATE';
  lsn: LSN;
  txnId: number;
  prevLSN: LSN;
  pageId: number;
  offset: number;
  before: number;
  after: number;
}

export interface CLRRecord {
  type: 'CLR';
  lsn: LSN;
  txnId: number;
  prevLSN: LSN;
  pageId: number;
  offset: number;
  after: number;
  undoNextLSN: LSN;
}

export interface CommitRecord {
  type: 'COMMIT';
  lsn: LSN;
  txnId: number;
  prevLSN: LSN;
}

export interface AbortRecord {
  type: 'ABORT';
  lsn: LSN;
  txnId: number;
  prevLSN: LSN;
}

export interface EndRecord {
  type: 'END';
  lsn: LSN;
  txnId: number;
  prevLSN: LSN;
}

export interface CheckpointRecord {
  type: 'CHECKPOINT';
  lsn: LSN;
  txnTable: TxnTableEntry[];
  dirtyPageTable: Array<[number, LSN]>;
}

export type LogRecord =
  | UpdateRecord
  | CLRRecord
  | CommitRecord
  | AbortRecord
  | EndRecord
  | CheckpointRecord;

export class WriteAheadLog {
  private records: LogRecord[] = [];
  private readonly filePath: string | null;

  constructor(filePath: string | null = null) {
    this.filePath = filePath;
    if (filePath !== null) writeFileSync(filePath, '');
  }

  get length(): number {
    return this.records.length;
  }

  /** 追加一条记录并分配 LSN（从 1 开始）；有文件时同步落盘。 */
  append(record: LogRecord): LogRecord {
    const stored = { ...record, lsn: this.records.length + 1 } as LogRecord;
    this.records.push(stored);
    if (this.filePath !== null) {
      appendFileSync(this.filePath, JSON.stringify(stored) + '\n');
    }
    return stored;
  }

  record(lsn: LSN): LogRecord {
    if (lsn < 1 || lsn > this.records.length) {
      throw new RangeError(`invalid LSN ${lsn}`);
    }
    return this.records[lsn - 1];
  }

  *scanFrom(lsn: LSN): IterableIterator<LogRecord> {
    for (let i = Math.max(1, lsn); i <= this.records.length; i++) {
      yield this.records[i - 1];
    }
  }

  lastCheckpointLSN(): LSN {
    for (let i = this.records.length - 1; i >= 0; i--) {
      if (this.records[i].type === 'CHECKPOINT') return this.records[i].lsn;
    }
    return 0;
  }

  static loadFromFile(filePath: string): WriteAheadLog {
    const wal = new WriteAheadLog(null);
    const content = readFileSync(filePath, 'utf8');
    for (const line of content.split('\n')) {
      if (line.trim() === '') continue;
      const record = JSON.parse(line) as LogRecord;
      if (record.lsn !== wal.records.length + 1) {
        throw new Error(`corrupt WAL: expected LSN ${wal.records.length + 1}, got ${record.lsn}`);
      }
      wal.records.push(record);
    }
    return wal;
  }
}
