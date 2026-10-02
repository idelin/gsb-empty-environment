import { open, readFile } from 'node:fs/promises';
import type { FileHandle } from 'node:fs/promises';
import { crc32 } from './crc32.ts';

export const TOMBSTONE = 0xffffffff;
const HEADER_BYTES = 12; // crc(4) + keyLen(4) + valueLen(4)

export interface WalEntry {
  key: Buffer;
  value: Buffer | null; // null = tombstone (delete)
}

/**
 * Write-ahead log segment. One segment backs exactly one MemTable;
 * when the MemTable freezes, the segment is sealed (fsync + close)
 * and a fresh segment is created for the successor MemTable.
 */
export class Wal {
  private readonly fh: FileHandle;
  readonly filePath: string;

  private constructor(fh: FileHandle, filePath: string) {
    this.fh = fh;
    this.filePath = filePath;
  }

  static async create(filePath: string): Promise<Wal> {
    const fh = await open(filePath, 'w');
    return new Wal(fh, filePath);
  }

  async append(key: Buffer, value: Buffer | null): Promise<void> {
    const valueLen = value === null ? TOMBSTONE : value.length;
    const body = Buffer.allocUnsafe(8 + key.length + (value === null ? 0 : value.length));
    body.writeUInt32LE(key.length, 0);
    body.writeUInt32LE(valueLen >>> 0, 4);
    key.copy(body, 8);
    if (value !== null) value.copy(body, 8 + key.length);
    const header = Buffer.allocUnsafe(4);
    header.writeUInt32LE(crc32(body), 0);
    await this.fh.write(Buffer.concat([header, body]));
  }

  /** fsync + close: the segment is immutable after this point. */
  async seal(): Promise<void> {
    await this.fh.sync();
    await this.fh.close();
  }
}

/**
 * Replays a sealed (or crashed) segment. Stops at the first corrupt or
 * truncated record, returning the valid prefix — the classic WAL
 * recovery contract.
 */
export async function replayWal(filePath: string): Promise<WalEntry[]> {
  const buf = await readFile(filePath);
  const entries: WalEntry[] = [];
  let offset = 0;
  while (offset + HEADER_BYTES <= buf.length) {
    const expectedCrc = buf.readUInt32LE(offset);
    const keyLen = buf.readUInt32LE(offset + 4);
    const valueLen = buf.readUInt32LE(offset + 8);
    const storedValueLen = valueLen === TOMBSTONE ? 0 : valueLen;
    const end = offset + HEADER_BYTES + keyLen + storedValueLen;
    if (end > buf.length) break; // torn write at tail
    const body = buf.subarray(offset + 4, end);
    if (crc32(body) !== expectedCrc) break; // corruption: stop replay
    entries.push({
      key: Buffer.from(body.subarray(8, 8 + keyLen)),
      value: valueLen === TOMBSTONE ? null : Buffer.from(body.subarray(8 + keyLen)),
    });
    offset = end;
  }
  return entries;
}
