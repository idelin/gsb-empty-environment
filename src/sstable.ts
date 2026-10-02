// Minimal SSTable: sorted key/value entries + trailing CRC-32 checksum.
// Layout: "SST1" magic | entry count (uint32) | entries... | crc32 (uint32)
// Each entry: keyLen u16 | key | valueLen u32 | value.
// The checksum covers everything before it; readers must verify.

import * as fs from 'node:fs';
import { crc32 } from './crc32.ts';

const MAGIC = Buffer.from('SST1');
const TRAILER = 4;

export interface TableInfo {
  fileSize: number;
  checksum: number;
  smallest: string;
  largest: string;
}

export function buildSSTable(path: string, entries: Array<[string, string]>): TableInfo {
  const sorted = [...entries].sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0));
  const parts: Buffer[] = [];
  const head = Buffer.alloc(MAGIC.length + 4);
  MAGIC.copy(head, 0);
  head.writeUInt32LE(sorted.length, MAGIC.length);
  parts.push(head);
  for (const [k, v] of sorted) {
    const kb = Buffer.from(k, 'utf8');
    const vb = Buffer.from(v, 'utf8');
    const eh = Buffer.alloc(2 + 4);
    eh.writeUInt16LE(kb.length, 0);
    eh.writeUInt32LE(vb.length, 2);
    parts.push(eh, kb, vb);
  }
  const body = Buffer.concat(parts);
  const sum = crc32(body);
  const trailer = Buffer.alloc(TRAILER);
  trailer.writeUInt32LE(sum, 0);

  const fd = fs.openSync(path, 'w');
  try {
    fs.writeSync(fd, body);
    fs.writeSync(fd, trailer);
    fs.fsyncSync(fd); // file data must be durable before the MANIFEST commit
  } finally {
    fs.closeSync(fd);
  }

  return {
    fileSize: body.length + TRAILER,
    checksum: sum,
    smallest: sorted.length ? sorted[0][0] : '',
    largest: sorted.length ? sorted[sorted.length - 1][0] : '',
  };
}

export function readSSTable(path: string): Array<[string, string]> {
  const buf = fs.readFileSync(path);
  if (buf.length < MAGIC.length + 4 + TRAILER) throw new Error(`sstable too small: ${path}`);
  if (!buf.subarray(0, MAGIC.length).equals(MAGIC)) throw new Error(`bad magic: ${path}`);
  const body = buf.subarray(0, buf.length - TRAILER);
  const stored = buf.readUInt32LE(buf.length - TRAILER);
  if (crc32(body) !== stored) throw new Error(`checksum mismatch: ${path}`);

  const count = body.readUInt32LE(MAGIC.length);
  const entries: Array<[string, string]> = [];
  let off = MAGIC.length + 4;
  for (let i = 0; i < count; i++) {
    const klen = body.readUInt16LE(off);
    const vlen = body.readUInt32LE(off + 2);
    off += 6;
    const key = body.subarray(off, off + klen).toString('utf8');
    off += klen;
    const value = body.subarray(off, off + vlen).toString('utf8');
    off += vlen;
    entries.push([key, value]);
  }
  return entries;
}

export function verifySSTable(path: string, expectedChecksum: number): boolean {
  const buf = fs.readFileSync(path);
  if (buf.length < TRAILER) return false;
  return crc32(buf.subarray(0, buf.length - TRAILER)) === expectedChecksum;
}
