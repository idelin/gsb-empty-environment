// Append-only journal with per-record CRC-32 framing.
//
// File protocol
// -------------
// The file is a sequence of lines, each exactly one record:
//
//     <crc32-hex-8> <json-body>\n
//
// where json-body = {"seq":N,"type":T,"workflowId":W,"data":{...}} and the CRC
// is computed over the raw bytes of json-body (not including the trailing \n).
//
// Persistence confirmation
// ------------------------
// A record is durable iff append() returned normally: append() performs a
// single write(2) of the whole line followed by fsync(2) on the journal fd,
// and only then publishes the record to the in-memory index. Callers must
// treat any action whose result record has not been confirmed this way as
// UNKNOWN (never as failed) and reconcile by idempotency key on recovery.
//
// Recovery / replay rules
// -----------------------
// 1. A missing or empty file replays to zero records.
// 2. If the file does not end with '\n', the final partial line is a torn
//    write from a crashed append; open() truncates the file back to the last
//    durable record. Truncation is tolerated ONLY for this last incomplete
//    record.
// 3. Every remaining (complete) line must match the framing, carry a valid
//    CRC, parse as JSON, and have a strictly consecutive seq. Any violation
//    is a complete-but-corrupt record and raises JournalCorruptionError;
//    recovery must not silently skip it.
import {
  closeSync,
  existsSync,
  fsyncSync,
  mkdirSync,
  openSync,
  readFileSync,
  statSync,
  truncateSync,
  writeSync,
} from 'node:fs';
import { dirname } from 'node:path';
import { crc32hex } from './crc32.ts';
import { JournalCorruptionError } from './errors.ts';

export interface JournalRecord {
  seq: number;
  type: string;
  workflowId: string;
  data: Record<string, unknown>;
}

export interface JournalOptions {
  /** Test hook invoked synchronously before each durable append (crash injection). */
  beforeAppend?: (nextSeq: number, record: JournalRecord) => void;
}

interface ReplayResult {
  records: JournalRecord[];
  /** Number of bytes occupied by complete, verified records. */
  durableBytes: number;
}

export class Journal {
  readonly records: JournalRecord[] = [];
  readonly path: string;
  private readonly options: JournalOptions;
  private fd: number;
  private nextSeq = 0;

  private constructor(path: string, options: JournalOptions) {
    this.path = path;
    this.options = options;
    mkdirSync(dirname(path), { recursive: true });
    this.fd = openSync(path, 'a');
  }

  /** Replay the file (enforcing the protocol), discard a torn tail, then open for appending. */
  static open(path: string, options: JournalOptions = {}): Journal {
    const { records, durableBytes } = Journal.replay(path);
    if (existsSync(path) && statSync(path).size > durableBytes) {
      // Torn tail from a crashed append: only the last, incomplete record may
      // be missing its newline, and replay() has already verified everything
      // before it. Cut the file back to the durable prefix.
      truncateSync(path, durableBytes);
    }
    const journal = new Journal(path, options);
    for (const record of records) {
      journal.records.push(record);
      journal.nextSeq = record.seq + 1;
    }
    return journal;
  }

  /**
   * Durably append a record. Returns only after write+fsync completed.
   * Synchronous on purpose: the check-and-append sequences built on top of
   * this (e.g. single-terminal-state) are atomic within the event loop.
   */
  append(type: string, workflowId: string, data: Record<string, unknown>): JournalRecord {
    const record: JournalRecord = { seq: this.nextSeq, type, workflowId, data };
    this.options.beforeAppend?.(this.nextSeq, record);
    const body = JSON.stringify(record);
    const line = `${crc32hex(body)} ${body}\n`;
    writeSync(this.fd, line);
    fsyncSync(this.fd);
    this.records.push(record);
    this.nextSeq += 1;
    return record;
  }

  close(): void {
    closeSync(this.fd);
  }

  /** Parse and verify all complete records; report where the durable prefix ends. */
  static replay(path: string): ReplayResult {
    if (!existsSync(path)) return { records: [], durableBytes: 0 };
    const raw = readFileSync(path, 'utf8');
    if (raw.length === 0) return { records: [], durableBytes: 0 };

    let complete = raw;
    if (!raw.endsWith('\n')) {
      const lastNewline = raw.lastIndexOf('\n');
      complete = lastNewline === -1 ? '' : raw.slice(0, lastNewline + 1);
    }
    if (complete.length === 0) return { records: [], durableBytes: 0 };

    const records: JournalRecord[] = [];
    const lines = complete.split('\n');
    for (let i = 0; i < lines.length - 1; i++) {
      const line = lines[i];
      const match = /^([0-9a-f]{8}) (.*)$/.exec(line);
      if (!match) {
        throw new JournalCorruptionError(`journal ${path}: line ${i + 1} is not a framed record`, i + 1);
      }
      const [, crc, body] = match;
      if (crc32hex(body) !== crc) {
        throw new JournalCorruptionError(`journal ${path}: line ${i + 1} failed CRC check`, i + 1);
      }
      let record: JournalRecord;
      try {
        record = JSON.parse(body) as JournalRecord;
      } catch {
        throw new JournalCorruptionError(`journal ${path}: line ${i + 1} is not valid JSON`, i + 1);
      }
      if (typeof record.seq !== 'number' || record.seq !== records.length) {
        throw new JournalCorruptionError(`journal ${path}: line ${i + 1} has non-consecutive seq`, i + 1);
      }
      records.push(record);
    }
    return { records, durableBytes: Buffer.byteLength(complete, 'utf8') };
  }
}
