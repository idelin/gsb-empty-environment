import { promises as fs } from 'node:fs';

export type LogKind =
  | 'begin'
  | 'update'
  | 'commit'
  | 'end'
  | 'clr'
  | 'begin_checkpoint'
  | 'end_checkpoint';

export type TxStatus = 'running' | 'committed';

export interface LogRecord {
  lsn: number;
  kind: LogKind;
  txId: number;
  prevLSN: number;
  pageId?: number;
  slot?: number;
  before?: number;
  after?: number;
  undoNextLSN?: number;
  beginLSN?: number;
  tt?: Array<[number, TxStatus, number]>;
  dpt?: Array<[number, number]>;
}

export class WAL {
  private buffer: LogRecord[] = [];
  private nextLSN: number;
  readonly path: string;

  private constructor(path: string, nextLSN: number) {
    this.path = path;
    this.nextLSN = nextLSN;
  }

  static async open(path: string): Promise<WAL> {
    const records = await WAL.readAll(path);
    const next = records.length === 0 ? 1 : records[records.length - 1].lsn + 1;
    return new WAL(path, next);
  }

  get bufferedCount(): number {
    return this.buffer.length;
  }

  get currentLSN(): number {
    return this.nextLSN - 1;
  }

  append(record: Omit<LogRecord, 'lsn'>): LogRecord {
    const full: LogRecord = { ...record, lsn: this.nextLSN++ };
    this.buffer.push(full);
    return full;
  }

  async flush(): Promise<void> {
    if (this.buffer.length === 0) return;
    const lines = this.buffer.map((r) => JSON.stringify(r)).join('\n') + '\n';
    await fs.appendFile(this.path, lines, 'utf8');
    this.buffer = [];
  }

  discardBuffer(): void {
    this.buffer = [];
  }

  static async readAll(path: string): Promise<LogRecord[]> {
    let text: string;
    try {
      text = await fs.readFile(path, 'utf8');
    } catch (err) {
      if ((err as { code?: string }).code === 'ENOENT') return [];
      throw err;
    }
    const records: LogRecord[] = [];
    for (const line of text.split('\n')) {
      if (line.length > 0) records.push(JSON.parse(line) as LogRecord);
    }
    return records;
  }
}
