import { crc32 } from './crc32.ts';
import { CodecError } from './errors.ts';
import { ByteReader } from './reader.ts';
import { ByteWriter } from './writer.ts';

/**
 * On-disk frame layout (little-endian):
 *   magic   u16   0x4D50 ("MP")
 *   version u8    schema version of the payload
 *   length  u32   payload byte length (<= MAX_FRAME_PAYLOAD)
 *   payload [length]
 *   crc32   u32   IEEE CRC over (magic|version|length|payload)
 */
export const FRAME_MAGIC = 0x4d50;
export const FRAME_VERSION = 2;
export const MIN_SUPPORTED_VERSION = 1;
export const MAX_FRAME_PAYLOAD = 64 * 1024 * 1024;
export const FRAME_HEADER_SIZE = 7;
export const FRAME_TRAILER_SIZE = 4;

export interface FrameView {
  /** Schema version carried by the frame. */
  version: number;
  /** Zero-copy view of the payload into the source buffer. */
  payload: Buffer;
  /** Offset of the first byte after this frame. */
  nextOffset: number;
  /** Total encoded size of this frame. */
  size: number;
}

export function encodeFrame(payload: Buffer, version = FRAME_VERSION): Buffer {
  if (payload.length > MAX_FRAME_PAYLOAD) {
    throw new CodecError('LENGTH_OVERFLOW', `payload ${payload.length} exceeds max ${MAX_FRAME_PAYLOAD}`);
  }
  const w = new ByteWriter(FRAME_HEADER_SIZE + payload.length + FRAME_TRAILER_SIZE);
  w.u16le(FRAME_MAGIC).u8(version).u32le(payload.length).bytes(payload);
  const head = w.finish();
  const crc = crc32(head);
  return Buffer.concat([head, Buffer.from([crc & 0xff, (crc >>> 8) & 0xff, (crc >>> 16) & 0xff, (crc >>> 24) & 0xff])]);
}

function checkVersion(version: number): void {
  if (version < MIN_SUPPORTED_VERSION || version > FRAME_VERSION) {
    throw new CodecError(
      'UNSUPPORTED_VERSION',
      `frame version ${version} outside supported range [${MIN_SUPPORTED_VERSION}, ${FRAME_VERSION}]`,
    );
  }
}

/**
 * Decode a frame at `offset`, returning zero-copy payload views into `buf`.
 * Throws CodecError('TRUNCATED') when the buffer ends mid-frame.
 */
export function decodeFrameView(buf: Buffer, offset = 0): FrameView {
  const r = new ByteReader(buf, offset);
  const magic = r.u16le();
  if (magic !== FRAME_MAGIC) {
    throw new CodecError('BAD_MAGIC', `bad frame magic 0x${magic.toString(16)} at offset ${offset}`);
  }
  const version = r.u8();
  checkVersion(version);
  const length = r.u32le();
  if (length > MAX_FRAME_PAYLOAD) {
    throw new CodecError('LENGTH_OVERFLOW', `frame length ${length} exceeds max ${MAX_FRAME_PAYLOAD}`);
  }
  const crcOffset = r.pos + length;
  const expectedCrc = (() => {
    const crcReader = new ByteReader(buf, crcOffset);
    return crcReader.u32le();
  })();
  const actualCrc = crc32(buf.subarray(offset, crcOffset));
  if (actualCrc !== expectedCrc) {
    throw new CodecError('BAD_CRC', `crc mismatch at offset ${offset}: expected ${expectedCrc}, got ${actualCrc}`);
  }
  const payload = buf.subarray(r.pos, crcOffset);
  return { version, payload, nextOffset: crcOffset + FRAME_TRAILER_SIZE, size: crcOffset + FRAME_TRAILER_SIZE - offset };
}

/**
 * Streaming-friendly decode: returns null when more bytes are needed,
 * throws on corruption.
 */
export function tryDecodeFrame(buf: Buffer, offset = 0): FrameView | null {
  if (buf.length - offset < FRAME_HEADER_SIZE) return null;
  const length = buf.readUInt32LE(offset + 3);
  if (length > MAX_FRAME_PAYLOAD) {
    throw new CodecError('LENGTH_OVERFLOW', `frame length ${length} exceeds max ${MAX_FRAME_PAYLOAD}`);
  }
  if (buf.length - offset < FRAME_HEADER_SIZE + length + FRAME_TRAILER_SIZE) return null;
  return decodeFrameView(buf, offset);
}
