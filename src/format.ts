/**
 * Binary layout of a read-only sharded uint32 set file ("SSIDB").
 *
 * All integers little-endian. IDs are uint32, strictly increasing per set.
 *
 * Header (fixed 76 bytes):
 *   magic        8 bytes  "SSIDB001"
 *   setId       32 bytes  utf8, NUL padded (max 31 bytes)
 *   version     32 bytes  utf8, NUL padded (max 31 bytes)
 *   shardCount   4 bytes  u32
 *
 * Directory (shardCount * 56 bytes, ordered by rangeStart):
 *   +0  rangeStart   4 bytes  u32  inclusive
 *   +4  rangeEnd     8 bytes  u64  exclusive (rangeStart <= rangeEnd <= 2^32; empty shard has count 0)
 *   +12 count        4 bytes  u32  number of ids in the shard
 *   +16 fragOffset   4 bytes  u32  absolute file offset of the fragment
 *   +20 fragLength   4 bytes  u32  fragment byte length
 *   +24 digest      32 bytes  sha256 of the fragment bytes
 *
 * Fragment:
 *   blockCount   4 bytes  u32
 *   per block:
 *     idCount    4 bytes  u32  (1..BLOCK_SIZE)
 *     base       4 bytes  u32  first id of the block
 *     deltas     varint[] (idCount - 1) LEB128 deltas, id[i] = id[i-1] + delta, delta >= 1
 */
import { createHash } from 'crypto';

export const MAGIC = 'SSIDB001';
export const HEADER_SIZE = 76;
export const DIR_ENTRY_SIZE = 56;
export const BLOCK_SIZE = 128;
export const MAX_NAME_BYTES = 31;

export interface ShardDirEntry {
  rangeStart: number;
  rangeEnd: number;
  count: number;
  fragOffset: number;
  fragLength: number;
  digest: string; // hex sha256 of fragment bytes
}

export interface SetFileHeader {
  setId: string;
  version: string;
  shards: ShardDirEntry[];
}

export class FormatError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'FormatError';
  }
}

export function sha256Hex(buf: Buffer): string {
  return createHash('sha256').update(buf).digest('hex');
}

export function encodeVarint(value: number): number[] {
  if (!Number.isInteger(value) || value < 0 || value > 0xffffffff) {
    throw new FormatError('varint value out of uint32 range: ' + value);
  }
  const out: number[] = [];
  let v = value;
  do {
    let byte = v % 128;
    v = Math.floor(v / 128);
    if (v > 0) byte += 128;
    out.push(byte);
  } while (v > 0);
  return out;
}

export function decodeVarint(buf: Buffer, pos: number): { value: number; next: number } {
  let result = 0;
  let shift = 1; // multiplicative scale to stay exact for uint32
  let p = pos;
  for (let i = 0; i < 5; i++) {
    if (p >= buf.length) throw new FormatError('truncated varint at offset ' + pos);
    const byte = buf[p];
    p += 1;
    result += (byte & 0x7f) * shift;
    if ((byte & 0x80) === 0) {
      if (result > 0xffffffff) throw new FormatError('varint overflows uint32 at offset ' + pos);
      return { value: result, next: p };
    }
    shift *= 128;
  }
  throw new FormatError('varint too long at offset ' + pos);
}

function writeFixedString(target: Buffer, offset: number, value: string, field: string): void {
  const bytes = Buffer.from(value, 'utf8');
  if (bytes.length > MAX_NAME_BYTES) {
    throw new FormatError(field + ' too long: max ' + MAX_NAME_BYTES + ' utf8 bytes');
  }
  bytes.copy(target, offset);
}

function readFixedString(buf: Buffer, offset: number): string {
  const slice = buf.slice(offset, offset + 32);
  const nul = slice.indexOf(0);
  return slice.slice(0, nul === -1 ? 32 : nul).toString('utf8');
}

export function encodeFragment(ids: number[]): Buffer {
  const parts: Buffer[] = [];
  const blockCount = Math.ceil(ids.length / BLOCK_SIZE);
  const head = Buffer.alloc(4);
  head.writeUInt32LE(blockCount, 0);
  parts.push(head);
  for (let b = 0; b < blockCount; b++) {
    const block = ids.slice(b * BLOCK_SIZE, (b + 1) * BLOCK_SIZE);
    const bh = Buffer.alloc(8);
    bh.writeUInt32LE(block.length, 0);
    bh.writeUInt32LE(block[0], 4);
    parts.push(bh);
    const deltas: number[] = [];
    for (let i = 1; i < block.length; i++) {
      const delta = block[i] - block[i - 1];
      if (delta < 1 || delta > 0xffffffff) {
        throw new FormatError('ids must be strictly increasing uint32');
      }
      deltas.push(...encodeVarint(delta));
    }
    parts.push(Buffer.from(deltas));
  }
  return Buffer.concat(parts);
}

export interface DecodedBlock {
  ids: Uint32Array;
  byteLength: number; // ids.length * 4, the decoded in-memory footprint
}

/** Decode a single block starting at `offset`. Returns the block and the offset of the next one. */
export function decodeBlockAt(frag: Buffer, offset: number): { block: DecodedBlock; next: number } {
  if (offset + 8 > frag.length) throw new FormatError('truncated block header at offset ' + offset);
  const idCount = frag.readUInt32LE(offset);
  const base = frag.readUInt32LE(offset + 4);
  if (idCount < 1 || idCount > BLOCK_SIZE) {
    throw new FormatError('invalid block idCount ' + idCount + ' at offset ' + offset);
  }
  const ids = new Uint32Array(idCount);
  ids[0] = base;
  let pos = offset + 8;
  let prev = base;
  for (let i = 1; i < idCount; i++) {
    const d = decodeVarint(frag, pos);
    pos = d.next;
    const value = prev + d.value;
    if (value > 0xffffffff) throw new FormatError('id overflows uint32 in block at offset ' + offset);
    ids[i] = value;
    prev = value;
  }
  return { block: { ids, byteLength: idCount * 4 }, next: pos };
}

export function fragmentBlockCount(frag: Buffer): number {
  if (frag.length < 4) throw new FormatError('fragment too short');
  return frag.readUInt32LE(0);
}

/** Decode every block of a fragment, verifying the encoding is well formed and consumes all bytes. */
export function decodeFragment(frag: Buffer): DecodedBlock[] {
  const blockCount = fragmentBlockCount(frag);
  const blocks: DecodedBlock[] = [];
  let offset = 4;
  for (let i = 0; i < blockCount; i++) {
    const r = decodeBlockAt(frag, offset);
    blocks.push(r.block);
    offset = r.next;
  }
  if (offset !== frag.length) {
    throw new FormatError('fragment has ' + (frag.length - offset) + ' trailing bytes');
  }
  return blocks;
}

/**
 * Build a complete set file. `ids` must be sorted unique uint32.
 * Shards are consecutive non-overlapping half-open ranges of `shardSize` ids.
 */
export function buildSetFile(setId: string, version: string, ids: number[], shardSize: number): Buffer {
  if (!Number.isInteger(shardSize) || shardSize < 1) {
    throw new FormatError('shardSize must be a positive integer');
  }
  for (let i = 0; i < ids.length; i++) {
    const id = ids[i];
    if (!Number.isInteger(id) || id < 0 || id > 0xffffffff) {
      throw new FormatError('id out of uint32 range: ' + id);
    }
    if (i > 0 && id <= ids[i - 1]) {
      throw new FormatError('ids must be strictly increasing (duplicate or unsorted at ' + id + ')');
    }
  }
  const shardCount = Math.ceil(ids.length / shardSize);
  const header = Buffer.alloc(HEADER_SIZE);
  header.write(MAGIC, 0, 8, 'ascii');
  writeFixedString(header, 8, setId, 'setId');
  writeFixedString(header, 40, version, 'version');
  header.writeUInt32LE(shardCount, 72);

  const dir = Buffer.alloc(shardCount * DIR_ENTRY_SIZE);
  const fragments: Buffer[] = [];
  let fragOffset = HEADER_SIZE + dir.length;
  for (let s = 0; s < shardCount; s++) {
    const shardIds = ids.slice(s * shardSize, (s + 1) * shardSize);
    const frag = encodeFragment(shardIds);
    const o = s * DIR_ENTRY_SIZE;
    dir.writeUInt32LE(shardIds[0], o);
    dir.writeBigUInt64LE(BigInt(shardIds[shardIds.length - 1]) + 1n, o + 4);
    dir.writeUInt32LE(shardIds.length, o + 12);
    dir.writeUInt32LE(fragOffset, o + 16);
    dir.writeUInt32LE(frag.length, o + 20);
    Buffer.from(sha256Hex(frag), 'hex').copy(dir, o + 24);
    fragments.push(frag);
    fragOffset += frag.length;
  }
  return Buffer.concat([header, dir, ...fragments]);
}

/** Build a set file from explicit shard ranges, allowing empty shards (used to test edge cases). */
export function buildSetFileWithShards(
  setId: string,
  version: string,
  shards: { rangeStart: number; rangeEnd: number; ids: number[] }[],
): Buffer {
  const header = Buffer.alloc(HEADER_SIZE);
  header.write(MAGIC, 0, 8, 'ascii');
  writeFixedString(header, 8, setId, 'setId');
  writeFixedString(header, 40, version, 'version');
  header.writeUInt32LE(shards.length, 72);
  const dir = Buffer.alloc(shards.length * DIR_ENTRY_SIZE);
  const fragments: Buffer[] = [];
  let fragOffset = HEADER_SIZE + dir.length;
  for (let s = 0; s < shards.length; s++) {
    const sh = shards[s];
    const frag = encodeFragment(sh.ids);
    const o = s * DIR_ENTRY_SIZE;
    dir.writeUInt32LE(sh.rangeStart, o);
    dir.writeBigUInt64LE(BigInt(sh.rangeEnd), o + 4);
    dir.writeUInt32LE(sh.ids.length, o + 12);
    dir.writeUInt32LE(fragOffset, o + 16);
    dir.writeUInt32LE(frag.length, o + 20);
    Buffer.from(sha256Hex(frag), 'hex').copy(dir, o + 24);
    fragments.push(frag);
    fragOffset += frag.length;
  }
  return Buffer.concat([header, dir, ...fragments]);
}

export function parseHeader(buf: Buffer): { setId: string; version: string; shardCount: number } {
  if (buf.length < HEADER_SIZE) throw new FormatError('file shorter than header');
  if (buf.slice(0, 8).toString('ascii') !== MAGIC) throw new FormatError('bad magic');
  return {
    setId: readFixedString(buf, 8),
    version: readFixedString(buf, 40),
    shardCount: buf.readUInt32LE(72),
  };
}

export function parseDirectory(buf: Buffer, shardCount: number): ShardDirEntry[] {
  const need = HEADER_SIZE + shardCount * DIR_ENTRY_SIZE;
  if (buf.length < need) throw new FormatError('file shorter than directory');
  const shards: ShardDirEntry[] = [];
  for (let s = 0; s < shardCount; s++) {
    const o = HEADER_SIZE + s * DIR_ENTRY_SIZE;
    shards.push({
      rangeStart: buf.readUInt32LE(o),
      rangeEnd: Number(buf.readBigUInt64LE(o + 4)),
      count: buf.readUInt32LE(o + 12),
      fragOffset: buf.readUInt32LE(o + 16),
      fragLength: buf.readUInt32LE(o + 20),
      digest: buf.slice(o + 24, o + 56).toString('hex'),
    });
  }
  return shards;
}
