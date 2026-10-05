import {
  existsSync,
  mkdirSync,
  openSync,
  readFileSync,
  writeSync,
  fsyncSync,
  truncateSync,
  closeSync,
} from 'node:fs';
import { dirname } from 'node:path';

const CRC_TABLE = (() => {
  const table = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    table[n] = c >>> 0;
  }
  return table;
})();

export function crc32(text: string): number {
  const bytes = Buffer.from(text, 'utf8');
  let crc = 0xffffffff;
  for (const b of bytes) crc = CRC_TABLE[(crc ^ b) & 0xff] ^ (crc >>> 8);
  return (crc ^ 0xffffffff) >>> 0;
}

export const RECORD_TYPES = new Set([
  'flow_start',
  'action_intent',
  'action_result',
  'compensation_intent',
  'compensation_result',
  'cancel_requested',
  'terminal',
]);

export interface JournalRecord {
  seq: number;
  ts: number;
  type: string;
  [key: string]: unknown;
}

export class JournalCorruptionError extends Error {
  readonly offset: number;
  constructor(message: string, offset: number) {
    super(message);
    this.name = 'JournalCorruptionError';
    this.offset = offset;
  }
}

function parseRecord(line: string, offset: number): JournalRecord {
  let obj: Record<string, unknown>;
  try {
    obj = JSON.parse(line);
  } catch {
    throw new JournalCorruptionError(
      `complete record at offset ${offset} is not valid JSON`,
      offset,
    );
  }
  const { crc, ...rest } = obj;
  if (typeof crc !== 'number') {
    throw new JournalCorruptionError(`record at offset ${offset} is missing its crc`, offset);
  }
  if (crc32(JSON.stringify(rest)) !== crc) {
    throw new JournalCorruptionError(`crc mismatch for record at offset ${offset}`, offset);
  }
  if (typeof rest.seq !== 'number' || typeof rest.type !== 'string' || !RECORD_TYPES.has(rest.type)) {
    throw new JournalCorruptionError(`record at offset ${offset} failed schema validation`, offset);
  }
  return rest as JournalRecord;
}

/**
 * Append-only write-ahead log. One JSON record per line, each line carrying a
 * CRC32 over the canonical JSON of the record without the crc field.
 *
 * Durability: a record is considered persisted only after append() returns,
 * which happens after write(2) + fsync(2) both succeed. Callers must treat any
 * record whose append() did not return as possibly absent after a crash.
 *
 * Recovery: the file is replayed sequentially. A trailing line that is not
 * newline-terminated is assumed to be a torn write of the LAST record and is
 * truncated. Any newline-terminated record that fails JSON/CRC/schema
 * validation is a complete-but-corrupt record and raises
 * JournalCorruptionError; recovery never silently skips records.
 */
export class Journal {
  readonly records: JournalRecord[] = [];
  truncatedBytes = 0;
  readonly path: string;
  private fd = -1;
  private seq = 0;

  constructor(path: string) {
    this.path = path;
    mkdirSync(dirname(path), { recursive: true });
    if (existsSync(path)) this.recover();
    this.fd = openSync(path, 'a');
  }

  private recover(): void {
    const content = readFileSync(this.path, 'utf8');
    let offset = 0;
    while (offset < content.length) {
      const nl = content.indexOf('\n', offset);
      if (nl === -1) {
        truncateSync(this.path, offset);
        this.truncatedBytes = content.length - offset;
        break;
      }
      const rec = parseRecord(content.slice(offset, nl), offset);
      this.records.push(rec);
      this.seq = rec.seq;
      offset = nl + 1;
    }
  }

  append(type: string, payload: Record<string, unknown>): JournalRecord {
    if (!RECORD_TYPES.has(type)) throw new Error(`unknown record type: ${type}`);
    const rec: JournalRecord = { seq: this.seq + 1, ts: Date.now(), type, ...payload };
    const body = JSON.stringify(rec);
    const line = body.slice(0, -1) + `,"crc":${crc32(body)}}\n`;
    writeSync(this.fd, line);
    fsyncSync(this.fd); // durability confirmation point
    this.seq = rec.seq;
    this.records.push(rec);
    return rec;
  }

  close(): void {
    if (this.fd >= 0) closeSync(this.fd);
    this.fd = -1;
  }
}
