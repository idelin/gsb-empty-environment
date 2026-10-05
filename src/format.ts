// Binary layout of a compact index segment. See FORMAT.md for the full spec.
// All integers are little-endian. Doc IDs are unsigned 32-bit.

import { crc32 } from './crc32.ts';
import { readUvarint, uvarintSize, writeUvarint } from './varint.ts';

export const MAGIC = Buffer.from('CIDX', 'ascii');
export const SUMMARY_MAGIC = Buffer.from('CSUM', 'ascii');
export const VERSION = 1;

// 4 magic + 1 version + 1 flags + 4 termCount + 6 * 8 section pointers.
export const HEADER_SIZE = 58;
// 4 magic + 4 termCount + 4 totalBlocks + 8 fileLength + 5 * 4 crc.
export const SUMMARY_SIZE = 36;
// Per-block skip-table entry inside a dictionary term record:
// u32 lastDocId + u32 count + u64 offset + u32 length.
export const BLOCK_META_SIZE = 20;

export const MAX_DOC_ID = 0xffffffff;
export const DEFAULT_BLOCK_SIZE = 128;
export const MAX_TERM_BYTES = 0xffff;

export class FormatError extends Error {
  code: string;
  offset: number;
  constructor(code: string, offset: number, message: string) {
    super(message);
    this.name = 'FormatError';
    this.code = code;
    this.offset = offset;
  }
}

export interface HeaderFields {
  termCount: number;
  dictOffset: number;
  dictLength: number;
  postingsOffset: number;
  postingsLength: number;
  summaryOffset: number;
  summaryLength: number;
}

export function encodeHeader(h: HeaderFields): Buffer {
  const buf = Buffer.alloc(HEADER_SIZE);
  MAGIC.copy(buf, 0);
  buf.writeUInt8(VERSION, 4);
  buf.writeUInt8(0, 5);
  buf.writeUInt32LE(h.termCount, 6);
  buf.writeBigUInt64LE(BigInt(h.dictOffset), 10);
  buf.writeBigUInt64LE(BigInt(h.dictLength), 18);
  buf.writeBigUInt64LE(BigInt(h.postingsOffset), 26);
  buf.writeBigUInt64LE(BigInt(h.postingsLength), 34);
  buf.writeBigUInt64LE(BigInt(h.summaryOffset), 42);
  buf.writeBigUInt64LE(BigInt(h.summaryLength), 50);
  return buf;
}

function readU64(buf: Buffer, offset: number): number {
  const v = buf.readBigUInt64LE(offset);
  if (v > BigInt(Number.MAX_SAFE_INTEGER)) {
    throw new FormatError('INTEGER_OVERFLOW', offset, 'u64 field exceeds 2^53-1');
  }
  return Number(v);
}

// Caller must guarantee buf.length >= HEADER_SIZE.
export function decodeHeader(buf: Buffer): HeaderFields {
  return {
    termCount: buf.readUInt32LE(6),
    dictOffset: readU64(buf, 10),
    dictLength: readU64(buf, 18),
    postingsOffset: readU64(buf, 26),
    postingsLength: readU64(buf, 34),
    summaryOffset: readU64(buf, 42),
    summaryLength: readU64(buf, 50),
  };
}

export interface SummaryFields {
  termCount: number;
  totalBlocks: number;
  fileLength: number;
  headerCrc: number;
  dictCrc: number;
  postingsCrc: number;
  summaryCrc: number;
}

export function encodeSummary(s: SummaryFields): Buffer {
  const buf = Buffer.alloc(SUMMARY_SIZE);
  SUMMARY_MAGIC.copy(buf, 0);
  buf.writeUInt32LE(s.termCount, 4);
  buf.writeUInt32LE(s.totalBlocks, 8);
  buf.writeBigUInt64LE(BigInt(s.fileLength), 12);
  buf.writeUInt32LE(s.headerCrc, 20);
  buf.writeUInt32LE(s.dictCrc, 24);
  buf.writeUInt32LE(s.postingsCrc, 28);
  buf.writeUInt32LE(s.summaryCrc, 32);
  return buf;
}

// Caller must guarantee [offset, offset + SUMMARY_SIZE) is inside buf.
export function decodeSummary(buf: Buffer, offset: number): SummaryFields {
  return {
    termCount: buf.readUInt32LE(offset + 4),
    totalBlocks: buf.readUInt32LE(offset + 8),
    fileLength: readU64(buf, offset + 12),
    headerCrc: buf.readUInt32LE(offset + 20),
    dictCrc: buf.readUInt32LE(offset + 24),
    postingsCrc: buf.readUInt32LE(offset + 28),
    summaryCrc: buf.readUInt32LE(offset + 32),
  };
}

export interface BlockMeta {
  lastDocId: number;
  count: number;
  offset: number;
  length: number;
}

export interface TermEntry {
  term: string;
  blocks: BlockMeta[];
}

// Parses the dictionary section. Throws FormatError on malformed input.
export function parseDictionary(
  buf: Buffer,
  offset: number,
  length: number,
  termCount: number,
): TermEntry[] {
  const end = offset + length;
  let pos = offset;
  const entries: TermEntry[] = [];
  for (let i = 0; i < termCount; i++) {
    if (pos + 2 > end) throw new FormatError('TRUNCATED', pos, 'dictionary term length field out of bounds');
    const termLen = buf.readUInt16LE(pos);
    pos += 2;
    if (pos + termLen > end) throw new FormatError('TRUNCATED', pos, 'dictionary term bytes out of bounds');
    const term = buf.toString('utf8', pos, pos + termLen);
    pos += termLen;
    if (pos + 4 > end) throw new FormatError('TRUNCATED', pos, 'dictionary block count out of bounds');
    const blockCount = buf.readUInt32LE(pos);
    pos += 4;
    const blocks: BlockMeta[] = [];
    for (let b = 0; b < blockCount; b++) {
      if (pos + BLOCK_META_SIZE > end) {
        throw new FormatError('TRUNCATED', pos, 'dictionary block entry out of bounds');
      }
      const lastDocId = buf.readUInt32LE(pos);
      const count = buf.readUInt32LE(pos + 4);
      const blockOffset = readU64(buf, pos + 8);
      const blockLength = buf.readUInt32LE(pos + 16);
      blocks.push({ lastDocId, count, offset: blockOffset, length: blockLength });
      pos += BLOCK_META_SIZE;
    }
    entries.push({ term, blocks });
  }
  if (pos !== end) {
    throw new FormatError('MALFORMED', pos, 'trailing bytes after dictionary entries');
  }
  return entries;
}

export interface BlockHeader {
  term: string;
  firstDocId: number;
  lastDocId: number;
  count: number;
  payloadCrc: number;
  headerSize: number;
  payloadOffset: number;
}

// Parses a posting block header inside [offset, end). Throws FormatError.
export function decodeBlockHeader(buf: Buffer, offset: number, end: number): BlockHeader {
  if (offset + 2 > end) throw new FormatError('TRUNCATED', offset, 'block term length field out of bounds');
  const termLen = buf.readUInt16LE(offset);
  const fixed = 2 + termLen + 16;
  if (offset + fixed > end) throw new FormatError('TRUNCATED', offset, 'block header out of bounds');
  const term = buf.toString('utf8', offset + 2, offset + 2 + termLen);
  let pos = offset + 2 + termLen;
  const firstDocId = buf.readUInt32LE(pos);
  const lastDocId = buf.readUInt32LE(pos + 4);
  const count = buf.readUInt32LE(pos + 8);
  const payloadCrc = buf.readUInt32LE(pos + 12);
  pos += 16;
  return { term, firstDocId, lastDocId, count, payloadCrc, headerSize: fixed, payloadOffset: pos };
}

// Decodes exactly `count` delta-encoded ids from [start, end).
// Verifies strict monotonicity, u32 range, header binding and exact consumption.
export function decodePayloadIds(
  buf: Buffer,
  start: number,
  end: number,
  count: number,
  firstDocId: number,
  lastDocId: number,
): number[] {
  const ids: number[] = [];
  let pos = start;
  let prev = 0;
  for (let i = 0; i < count; i++) {
    const r = readUvarint(buf, pos, end);
    if (!r.ok) {
      throw new FormatError(
        r.error === 'overflow' ? 'INTEGER_OVERFLOW' : 'TRUNCATED',
        pos,
        r.error === 'overflow' ? 'varint exceeds 32 bits' : 'posting payload truncated',
      );
    }
    let value: number;
    if (i === 0) {
      value = r.value;
    } else {
      if (r.value === 0) {
        throw new FormatError('NON_INCREASING_ID', pos, 'delta must be >= 1 (ids must strictly increase)');
      }
      value = prev + r.value;
      if (value > MAX_DOC_ID) {
        throw new FormatError('INTEGER_OVERFLOW', pos, `doc id ${value} exceeds u32 max`);
      }
    }
    ids.push(value);
    prev = value;
    pos = r.next;
  }
  if (count > 0) {
    if (ids[0] !== firstDocId) {
      throw new FormatError('BLOCK_HEADER_MISMATCH', start, 'payload first id != header firstDocId');
    }
    if (ids[count - 1] !== lastDocId) {
      throw new FormatError('BLOCK_HEADER_MISMATCH', pos - 1, 'payload last id != header lastDocId');
    }
  }
  if (pos !== end) {
    throw new FormatError('MALFORMED', pos, 'trailing bytes after posting payload');
  }
  return ids;
}

// Serializes one posting block: bound term + doc range + count + crc + payload.
export function encodeBlock(term: string, ids: number[]): Buffer {
  const termBuf = Buffer.from(term, 'utf8');
  let payloadSize = uvarintSize(ids[0]);
  for (let i = 1; i < ids.length; i++) payloadSize += uvarintSize(ids[i] - ids[i - 1]);
  const headerSize = 2 + termBuf.length + 16;
  const buf = Buffer.alloc(headerSize + payloadSize);
  buf.writeUInt16LE(termBuf.length, 0);
  termBuf.copy(buf, 2);
  let pos = 2 + termBuf.length;
  buf.writeUInt32LE(ids[0], pos);
  buf.writeUInt32LE(ids[ids.length - 1], pos + 4);
  buf.writeUInt32LE(ids.length, pos + 8);
  const crcOffset = pos + 12;
  pos += 16;
  const payloadStart = pos;
  pos = writeUvarint(buf, pos, ids[0]);
  for (let i = 1; i < ids.length; i++) {
    pos = writeUvarint(buf, pos, ids[i] - ids[i - 1]);
  }
  buf.writeUInt32LE(crc32(buf, payloadStart, pos), crcOffset);
  return buf;
}
