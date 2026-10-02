/**
 * WAL (Write-Ahead Log) for the ARIES recovery protocol.
 *
 * Log records are persisted as JSON lines, one record per line, each carrying
 * a monotonically increasing LSN (Log Sequence Number).
 */

export type TxnStatus = 'running' | 'committed' | 'aborting';

export interface UpdateRecord {
  lsn: number;
  type: 'update';
  txnId: number;
  pageId: string;
  prevLSN: number;
  before: string;
  after: string;
}

/** Compensation Log Record: written when undoing an update. */
export interface ClrRecord {
  lsn: number;
  type: 'clr';
  txnId: number;
  pageId: string;
  prevLSN: number;
  /** LSN of the next record to undo after this CLR (0 = nothing left). */
  undoNextLSN: number;
  before: string;
  after: string;
}

export interface TxnRecord {
  lsn: number;
  type: 'commit' | 'abort' | 'end';
  txnId: number;
  prevLSN: number;
}

export interface CheckpointRecord {
  lsn: number;
  type: 'checkpoint';
  /** DirtyPageTable snapshot: pageId -> recLSN. */
  dirtyPages: Record<string, number>;
  /** TransactionTable snapshot: txnId -> { status, lastLSN }. */
  transactions: Record<string, { status: TxnStatus; lastLSN: number }>;
}

export type LogRecord = UpdateRecord | ClrRecord | TxnRecord | CheckpointRecord;

export class LogManager {
  private readonly filePath: string;
  private nextLSN: number;
  private tail: LogRecord[] = [];

  constructor(filePath: string, startLSN = 1) {
    this.filePath = filePath;
    this.nextLSN = startLSN;
  }

  /** Append a record (without LSN) and return the persisted record. */
  append(record: Omit<LogRecord, 'lsn'>): LogRecord {
    const full = { ...record, lsn: this.nextLSN++ } as LogRecord;
    this.tail.push(full);
    return full;
  }

  /** Flush buffered records to the WAL file (append-only). */
  async flush(): Promise<void> {
    if (this.tail.length === 0) return;
    const fs = await import('node:fs/promises');
    const lines = this.tail.map((r) => JSON.stringify(r)).join('\n') + '\n';
    await fs.appendFile(this.filePath, lines, 'utf8');
    this.tail = [];
  }

  get currentLSN(): number {
    return this.nextLSN - 1;
  }

  /** Read the whole WAL file back into memory, ordered by LSN. */
  static async readAll(filePath: string): Promise<LogRecord[]> {
    const fs = await import('node:fs/promises');
    let content: string;
    try {
      content = await fs.readFile(filePath, 'utf8');
    } catch {
      return [];
    }
    return content
      .split('\n')
      .filter((line) => line.trim().length > 0)
      .map((line) => JSON.parse(line) as LogRecord)
      .sort((a, b) => a.lsn - b.lsn);
  }
}
