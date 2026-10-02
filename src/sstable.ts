import { open, readFile } from 'node:fs/promises';
import { crc32 } from './crc32.ts';
import { TOMBSTONE } from './wal.ts';

const MAGIC = 0x4c535330; // 'LSS0'
const FOOTER_BYTES = 12; // count(4) + crc(4) + magic(4)

/**
 * Writes a Level-0 SSTable: length-prefixed key/value records followed by
 * a footer carrying the record count, a CRC32 of the data section and a
 * magic number. The file is fsynced before this resolves.
 */
export async function writeSSTable(
  filePath: string,
  entries: [string, Buffer | null][],
): Promise<void> {
  const chunks: Buffer[] = [];
  for (const [key, value] of entries) {
    const keyBuf = Buffer.from(key, 'utf8');
    const header = Buffer.allocUnsafe(8);
    header.writeUInt32LE(keyBuf.length, 0);
    header.writeUInt32LE(value === null ? TOMBSTONE : value.length, 4);
    chunks.push(header, keyBuf);
    if (value !== null) chunks.push(value);
  }
  const data = Buffer.concat(chunks);
  const footer = Buffer.allocUnsafe(FOOTER_BYTES);
  footer.writeUInt32LE(entries.length, 0);
  footer.writeUInt32LE(crc32(data), 4);
  footer.writeUInt32LE(MAGIC, 8);
  const fh = await open(filePath, 'w');
  try {
    await fh.write(Buffer.concat([data, footer]));
    await fh.sync();
  } finally {
    await fh.close();
  }
}

export class SSTableReader {
  readonly filePath: string;
  private readonly map: Map<string, Buffer | null>;

  private constructor(filePath: string, map: Map<string, Buffer | null>) {
    this.filePath = filePath;
    this.map = map;
  }

  static async load(filePath: string): Promise<SSTableReader> {
    const buf = await readFile(filePath);
    if (buf.length < FOOTER_BYTES) throw new Error(`SSTable too small: ${filePath}`);
    const footer = buf.subarray(buf.length - FOOTER_BYTES);
    if (footer.readUInt32LE(8) !== MAGIC) throw new Error(`Bad SSTable magic: ${filePath}`);
    const count = footer.readUInt32LE(0);
    const data = buf.subarray(0, buf.length - FOOTER_BYTES);
    if (crc32(data) !== footer.readUInt32LE(4)) {
      throw new Error(`SSTable CRC mismatch: ${filePath}`);
    }
    const map = new Map<string, Buffer | null>();
    let offset = 0;
    for (let i = 0; i < count; i++) {
      const keyLen = data.readUInt32LE(offset);
      const valueLen = data.readUInt32LE(offset + 4);
      const key = data.toString('utf8', offset + 8, offset + 8 + keyLen);
      offset += 8 + keyLen;
      if (valueLen === TOMBSTONE) {
        map.set(key, null);
      } else {
        map.set(key, Buffer.from(data.subarray(offset, offset + valueLen)));
        offset += valueLen;
      }
    }
    return new SSTableReader(filePath, map);
  }

  /** Returns Buffer for a hit, null for a tombstone, undefined for a miss. */
  get(key: string): Buffer | null | undefined {
    return this.map.get(key);
  }

  get entryCount(): number {
    return this.map.size;
  }
}
