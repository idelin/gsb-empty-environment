/**
 * 预写日志（WAL）管理器。
 *
 * 追加协议：单写者、同步追加、可选 fsync（提交记录强制落盘）。
 * LSN 由确定性时钟分配；追加路径上带异步互斥锁，保证并发调用下
 * LSN 分配与文件写入构成全序，不存在交错或丢失。
 */
import { closeSync, existsSync, fsyncSync, openSync, readFileSync, writeSync } from 'node:fs';
import {
  deserializeRecord,
  serializeRecord,
  type LogRecord,
  type LogRecordInput,
  type LSN,
  LogType,
  type EndCheckpointRecord,
} from './log-record.ts';
import { DeterministicClock } from './clock.ts';

export class WalWriter {
  readonly path: string;
  readonly clock: DeterministicClock;
  #fd: number;
  #closed = false;
  #tail: Promise<unknown> = Promise.resolve();

  constructor(path: string, clock?: DeterministicClock) {
    this.path = path;
    this.clock = clock ?? new DeterministicClock();
    if (existsSync(path)) {
      // 重启：时钟推进到已持久化的最大 LSN 之后。
      for (const record of readWal(path)) {
        this.clock.observe(record.lsn);
      }
    }
    this.#fd = openSync(path, 'a');
  }

  get isClosed(): boolean {
    return this.#closed;
  }

  /**
   * 追加一条记录并返回带 LSN 的完整记录。
   * 同步段内完成 LSN 分配与写入，Node 单线程语义下天然原子；
   * 异步并发调用通过 #tail 串行化，构成确定的全序。
   */
  append(input: LogRecordInput, options?: { fsync?: boolean }): LogRecord {
    if (this.#closed) {
      throw new Error('WAL append after close');
    }
    const record = { ...input, lsn: this.clock.tick() } as LogRecord;
    writeSync(this.#fd, serializeRecord(record) + '\n');
    if (options?.fsync) {
      fsyncSync(this.#fd);
    }
    return record;
  }

  /** 异步追加：与所有其他追加操作串行化（用于并发安全性验证）。 */
  appendAsync(input: LogRecordInput, options?: { fsync?: boolean }): Promise<LogRecord> {
    const run = this.#tail.then(() => this.append(input, options));
    this.#tail = run.catch(() => {});
    return run;
  }

  flush(): void {
    if (!this.#closed) {
      fsyncSync(this.#fd);
    }
  }

  close(): void {
    if (!this.#closed) {
      fsyncSync(this.#fd);
      closeSync(this.#fd);
      this.#closed = true;
    }
  }
}

/** 顺序读取整个 WAL，并校验 LSN 单调递增（拓扑约束）。 */
export function readWal(path: string): LogRecord[] {
  if (!existsSync(path)) {
    return [];
  }
  const content = readFileSync(path, 'utf8');
  const records: LogRecord[] = [];
  let previousLsn = 0;
  for (const line of content.split('\n')) {
    if (line.trim() === '') continue;
    const record = deserializeRecord(line);
    if (record.lsn <= previousLsn) {
      throw new Error(
        `corrupt WAL: non-monotonic LSN ${record.lsn} after ${previousLsn}`,
      );
    }
    previousLsn = record.lsn;
    records.push(record);
  }
  return records;
}

export class WalIndex {
  readonly records: LogRecord[];
  readonly #byLsn = new Map<LSN, number>();

  constructor(records: LogRecord[]) {
    this.records = records;
    records.forEach((record, index) => this.#byLsn.set(record.lsn, index));
  }

  get(lsn: LSN): LogRecord {
    const index = this.#byLsn.get(lsn);
    if (index === undefined) {
      throw new Error(`WAL record not found for LSN ${lsn}`);
    }
    return this.records[index];
  }

  /** 最后一个完整的 END_CHECKPOINT。 */
  lastEndCheckpoint(): EndCheckpointRecord | null {
    for (let i = this.records.length - 1; i >= 0; i -= 1) {
      const record = this.records[i];
      if (record.type === LogType.EndCheckpoint) {
        return record;
      }
    }
    return null;
  }

  /** 第一个 lsn >= fromLsn 的记录下标；不存在时返回 records.length。 */
  firstIndexAtOrAfter(fromLsn: LSN): number {
    let lo = 0;
    let hi = this.records.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (this.records[mid].lsn < fromLsn) lo = mid + 1;
      else hi = mid;
    }
    return lo;
  }
}
