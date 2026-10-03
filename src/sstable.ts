import { open, readFile } from 'node:fs/promises';

const MAGIC = 0x31535453; // 'SST1'
const TOMBSTONE = 0xffffffff;

export interface SsTableEntry {
  key: string;
  value: Buffer | null; // null = tombstone
}

// file layout: magic(4) | count(4) | [ keyLen(4) valueLen(4) key value ]*
// valueLen == 0xffffffff marks a tombstone.
export class SsTable {
  readonly filePath: string;
  private readonly entries: Map<string, Buffer | null>;

  private constructor(filePath: string, entries: Map<string, Buffer | null>) {
    this.filePath = filePath;
    this.entries = entries;
  }

  static async write(filePath: string, entries: SsTableEntry[]): Promise<void> {
    const sorted = [...entries].sort((a, b) => (a.key < b.key ? -1 : a.key > b.key ? 1 : 0));
    let total = 8;
    for (const e of sorted) {
      total += 8 + Buffer.byteLength(e.key) + (e.value === null ? 0 : e.value.length);
    }
    const buf = Buffer.allocUnsafe(total);
    buf.writeUInt32LE(MAGIC, 0);
    buf.writeUInt32LE(sorted.length, 4);
    let offset = 8;
    for (const e of sorted) {
      const keyBuf = Buffer.from(e.key);
      buf.writeUInt32LE(keyBuf.length, offset);
      buf.writeUInt32LE(e.value === null ? TOMBSTONE : e.value.length, offset + 4);
      offset += 8;
      keyBuf.copy(buf, offset);
      offset += keyBuf.length;
      if (e.value !== null) {
        e.value.copy(buf, offset);
        offset += e.value.length;
      }
    }
    const fh = await open(filePath, 'w');
    try {
      await fh.writeFile(buf);
      await fh.sync(); // durable before the covering WAL segment is reclaimed
    } finally {
      await fh.close();
    }
  }

  static async load(filePath: string): Promise<SsTable> {
    const buf = await readFile(filePath);
    if (buf.length < 8 || buf.readUInt32LE(0) !== MAGIC) {
      throw new Error(`invalid sstable file: ${filePath}`);
    }
    const count = buf.readUInt32LE(4);
    const entries = new Map<string, Buffer | null>();
    let offset = 8;
    for (let i = 0; i < count; i++) {
      const keyLen = buf.readUInt32LE(offset);
      const valueLen = buf.readUInt32LE(offset + 4);
      offset += 8;
      const key = buf.toString('utf8', offset, offset + keyLen);
      offset += keyLen;
      let value: Buffer | null = null;
      if (valueLen !== TOMBSTONE) {
        value = buf.subarray(offset, offset + valueLen);
        offset += valueLen;
      }
      entries.set(key, value);
    }
    return new SsTable(filePath, entries);
  }

  get(key: string): Buffer | null | undefined {
    if (!this.entries.has(key)) return undefined;
    return this.entries.get(key)!;
  }

  get entryCount(): number {
    return this.entries.size;
  }
}
