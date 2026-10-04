// Binary compact frame codec for persistent log entries.
//
// Frame layout (little-endian):
//   offset 0   u32   magic = 0x4D50584C ("MPXL")
//   offset 4   u8    format version
//   offset 5   u8    entry type (0 = noop, 1 = data)
//   offset 6   u16   header length (>= 32; extra bytes are forward-compat extensions)
//   offset 8   u64   term (paxos ballot, encoded)
//   offset 16  u64   log index (slot)
//   offset 24  u32   payload length
//   offset 28  u32   crc32 over header (crc field zeroed) + payload
//   offset 32  ..    optional header extensions (headerLen - 32 bytes)
//   offset headerLen .. payload bytes
//
// Compatibility rules:
//  - backward: a new reader accepts any version <= its own.
//  - forward: an old reader may accept newer frames because unknown header
//    extensions are skipped via headerLen; callers gate via `maxVersion`.
// Safety: every length field is bounds-checked against the source buffer
// before any view is created; oversized lengths are rejected as OVERFLOW.

import { Buffer } from 'node:buffer';
import { crc32Digest, crc32Start, crc32Update } from './crc32.ts';

export const MAGIC = 0x4d50584c;
export const FIXED_HEADER_LEN = 32;
export const FORMAT_VERSION = 1;
export const MAX_PAYLOAD_LEN = 64 * 1024 * 1024;

export const EntryType = { Noop: 0, Data: 1 } as const;
export type EntryTypeValue = (typeof EntryType)[keyof typeof EntryType];

const ZERO4 = new Uint8Array(4);
const EMPTY = new Uint8Array(0);

export class CodecError extends Error {
  readonly code: string;
  constructor(code: string, message: string) {
    super(message);
    this.name = 'CodecError';
    this.code = code;
  }
}

export interface EntryInput {
  term: bigint | number;
  index: bigint | number;
  type?: number;
  payload?: Uint8Array;
  version?: number;
  extensions?: Uint8Array;
}

export interface EntryView {
  readonly version: number;
  readonly type: number;
  readonly term: bigint;
  readonly index: bigint;
  readonly headerLen: number;
  readonly frameLen: number;
  readonly offset: number;
  readonly extensions: Buffer;
  readonly payload: Buffer;
}

export interface DecodeOptions {
  verifyCrc?: boolean;
  maxVersion?: number;
  maxPayloadLen?: number;
}

export function encodedSize(payloadLen: number, extensionsLen = 0): number {
  return FIXED_HEADER_LEN + extensionsLen + payloadLen;
}

export function encodeEntry(input: EntryInput): Buffer {
  const payload = input.payload ?? EMPTY;
  if (payload.length > MAX_PAYLOAD_LEN) {
    throw new CodecError('PAYLOAD_TOO_LARGE', `payload ${payload.length} exceeds ${MAX_PAYLOAD_LEN}`);
  }
  const ext = input.extensions ?? EMPTY;
  const headerLen = FIXED_HEADER_LEN + ext.length;
  if (headerLen > 0xffff) {
    throw new CodecError('HEADER_TOO_LARGE', `header ${headerLen} exceeds 65535`);
  }
  const buf = Buffer.allocUnsafe(headerLen + payload.length);
  buf.writeUInt32LE(MAGIC, 0);
  buf.writeUInt8(input.version ?? FORMAT_VERSION, 4);
  buf.writeUInt8(input.type ?? EntryType.Data, 5);
  buf.writeUInt16LE(headerLen, 6);
  buf.writeBigUInt64LE(BigInt(input.term), 8);
  buf.writeBigUInt64LE(BigInt(input.index), 16);
  buf.writeUInt32LE(payload.length, 24);
  buf.writeUInt32LE(0, 28);
  if (ext.length > 0) buf.set(ext, FIXED_HEADER_LEN);
  if (payload.length > 0) buf.set(payload, headerLen);
  let state = crc32Start();
  state = crc32Update(state, buf.subarray(0, 28));
  state = crc32Update(state, ZERO4);
  state = crc32Update(state, buf.subarray(32));
  buf.writeUInt32LE(crc32Digest(state), 28);
  return buf;
}

export function decodeEntryView(buf: Buffer, offset = 0, opts: DecodeOptions = {}): EntryView {
  if (!Number.isSafeInteger(offset) || offset < 0) {
    throw new CodecError('BAD_OFFSET', `invalid offset ${offset}`);
  }
  if (offset + FIXED_HEADER_LEN > buf.length) {
    throw new CodecError('TRUNCATED', `need ${FIXED_HEADER_LEN} header bytes, have ${buf.length - offset}`);
  }
  if (buf.readUInt32LE(offset) !== MAGIC) {
    throw new CodecError('BAD_MAGIC', `bad magic at offset ${offset}`);
  }
  const version = buf.readUInt8(offset + 4);
  const maxVersion = opts.maxVersion ?? Number.POSITIVE_INFINITY;
  if (version > maxVersion) {
    throw new CodecError('UNSUPPORTED_VERSION', `version ${version} > maxVersion ${maxVersion}`);
  }
  const type = buf.readUInt8(offset + 5);
  const headerLen = buf.readUInt16LE(offset + 6);
  if (headerLen < FIXED_HEADER_LEN) {
    throw new CodecError('BAD_HEADER', `headerLen ${headerLen} < ${FIXED_HEADER_LEN}`);
  }
  if (offset + headerLen > buf.length) {
    throw new CodecError('TRUNCATED', `header extends past buffer end`);
  }
  const payloadLen = buf.readUInt32LE(offset + 24);
  const maxPayload = opts.maxPayloadLen ?? MAX_PAYLOAD_LEN;
  if (payloadLen > maxPayload) {
    throw new CodecError('OVERFLOW', `payloadLen ${payloadLen} exceeds limit ${maxPayload}`);
  }
  const frameLen = headerLen + payloadLen;
  if (offset + frameLen > buf.length) {
    throw new CodecError('TRUNCATED', `frame of ${frameLen} bytes exceeds remaining ${buf.length - offset}`);
  }
  if (opts.verifyCrc !== false) {
    const expected = buf.readUInt32LE(offset + 28);
    let state = crc32Start();
    state = crc32Update(state, buf.subarray(offset, offset + 28));
    state = crc32Update(state, ZERO4);
    state = crc32Update(state, buf.subarray(offset + 32, offset + frameLen));
    const actual = crc32Digest(state);
    if (actual !== expected) {
      throw new CodecError('CRC_MISMATCH', `crc ${actual} != stored ${expected}`);
    }
  }
  return {
    version,
    type,
    term: buf.readBigUInt64LE(offset + 8),
    index: buf.readBigUInt64LE(offset + 16),
    headerLen,
    frameLen,
    offset,
    extensions: buf.subarray(offset + FIXED_HEADER_LEN, offset + headerLen),
    payload: buf.subarray(offset + headerLen, offset + frameLen),
  };
}

// Zero-copy scan over a fully mapped segment (e.g. an mmap-style image).
export function* scanFrames(buf: Buffer, opts: DecodeOptions = {}): Generator<EntryView> {
  let offset = 0;
  while (offset < buf.length) {
    const view = decodeEntryView(buf, offset, opts);
    yield view;
    offset += view.frameLen;
  }
}

// Incremental reader for streamed bytes; tolerates frames split across feeds.
export class FrameReader {
  private buf: Buffer | null = null;
  private readonly opts: DecodeOptions;

  constructor(opts: DecodeOptions = {}) {
    this.opts = opts;
  }

  get bufferedBytes(): number {
    return this.buf ? this.buf.length : 0;
  }

  feed(chunk: Buffer): EntryView[] {
    this.buf = this.buf ? Buffer.concat([this.buf, chunk]) : chunk;
    const out: EntryView[] = [];
    for (;;) {
      if (this.buf.length < FIXED_HEADER_LEN) break;
      let frameLen: number;
      try {
        const headerLen = this.buf.readUInt16LE(6);
        if (this.buf.readUInt32LE(0) !== MAGIC) throw new CodecError('BAD_MAGIC', 'bad magic in stream');
        if (headerLen < FIXED_HEADER_LEN) throw new CodecError('BAD_HEADER', `headerLen ${headerLen}`);
        const payloadLen = this.buf.readUInt32LE(24);
        const maxPayload = this.opts.maxPayloadLen ?? MAX_PAYLOAD_LEN;
        if (payloadLen > maxPayload) throw new CodecError('OVERFLOW', `payloadLen ${payloadLen}`);
        frameLen = headerLen + payloadLen;
      } catch (err) {
        this.buf = null;
        throw err;
      }
      if (this.buf.length < frameLen) break;
      const frame = this.buf.subarray(0, frameLen);
      this.buf = this.buf.subarray(frameLen);
      out.push(decodeEntryView(frame, 0, this.opts));
    }
    if (this.buf.length === 0) this.buf = null;
    return out;
  }
}
