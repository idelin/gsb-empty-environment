/**
 * Write-ahead log: framed record persistence and memory-mapped style scanning.
 *
 * File layout:
 *   header: u32 magic "ARW1", u16 formatVersion, u16 headerSize,
 *           u16 flags, u16 reserved, u32 reserved   (16 bytes total)
 *   frames: u32 payloadLen, u32 crc32(payload), payload[payloadLen]
 *
 * The LSN of a record is the absolute file offset of its frame, so LSNs are
 * stable pointers usable for direct random access (readRecordAt).
 *
 * Scanning maps the whole file into one contiguous Buffer (the mmap
 * surrogate) and yields records whose image fields alias that buffer —
 * no payload bytes are ever copied. A truncated or CRC-invalid tail is
 * treated as the logical end of log (crash tolerance); strict mode turns
 * both into CorruptionError for corruption-detection tests.
 */

import { open, type FileHandle } from "node:fs/promises";
import { readFileSync } from "node:fs";
import {
  BoundedReader,
  BoundedWriter,
  CorruptionError,
  UnsupportedVersionError,
  crc32,
} from "./binary.ts";
import { FORMAT_VERSION, decodeRecord, encodeRecord, type LogRecord } from "./logrecord.ts";

export const WAL_MAGIC = 0x31575241; // "ARW1" little-endian
export const WAL_HEADER_SIZE = 16;
export const FRAME_HEADER_SIZE = 8;

export interface WalHeader {
  version: number;
  flags: number;
}

export function encodeWalHeader(version: number, flags = 0): Buffer {
  const w = new BoundedWriter(WAL_HEADER_SIZE);
  w.writeU32(WAL_MAGIC);
  w.writeU16(version);
  w.writeU16(WAL_HEADER_SIZE);
  w.writeU16(flags);
  w.writeU16(0);
  w.writeU32(0);
  return Buffer.from(w.toBuffer());
}

export function decodeWalHeader(mapped: Uint8Array): WalHeader {
  if (mapped.length < WAL_HEADER_SIZE) {
    throw new CorruptionError("WAL shorter than header", 0);
  }
  const r = new BoundedReader(mapped, 0, WAL_HEADER_SIZE);
  const magic = r.readU32();
  if (magic !== WAL_MAGIC) throw new CorruptionError("bad WAL magic", 0);
  const version = r.readU16();
  const headerSize = r.readU16();
  const flags = r.readU16();
  if (headerSize < WAL_HEADER_SIZE) {
    throw new CorruptionError(`bad header size ${headerSize}`, 6);
  }
  if (version > FORMAT_VERSION) {
    throw new UnsupportedVersionError(
      `WAL format version ${version} newer than supported ${FORMAT_VERSION}`,
    );
  }
  return { version, flags };
}

/** Serialized concurrent access: appends are chained through a promise mutex. */
export class WalWriter {
  private fh: FileHandle;
  private offset: number;
  private chain: Promise<unknown> = Promise.resolve();
  private closedFlag = false;

  private constructor(fh: FileHandle, offset: number) {
    this.fh = fh;
    this.offset = offset;
  }

  static async create(path: string): Promise<WalWriter> {
    const fh = await open(path, "w");
    await fh.write(encodeWalHeader(FORMAT_VERSION));
    return new WalWriter(fh, WAL_HEADER_SIZE);
  }

  /** Open an existing WAL for append, truncating any torn tail first. */
  static async openForRecovery(path: string): Promise<WalWriter> {
    const mapped = mapWal(path);
    let end = WAL_HEADER_SIZE;
    for (const frame of scanWalFrames(mapped)) end = frame.nextOffset;
    const fh = await open(path, "r+");
    await fh.truncate(end);
    return new WalWriter(fh, end);
  }

  get closed(): boolean {
    return this.closedFlag;
  }

  /** File offset where the next record will be placed. */
  get currentLSN(): number {
    return this.offset;
  }

  /** Append a record; assigns and returns its LSN. Safe for concurrent callers. */
  append(rec: LogRecord): Promise<number> {
    return this.enqueue(async () => {
      const lsn = this.offset;
      const payload = encodeRecord({ ...rec, lsn });
      const frame = new BoundedWriter(FRAME_HEADER_SIZE + payload.length);
      frame.writeU32(payload.length);
      frame.writeU32(crc32(payload));
      frame.writeBytes(payload);
      const buf = Buffer.from(frame.toBuffer());
      await this.fh!.write(buf, 0, buf.length, this.offset);
      this.offset += buf.length;
      return lsn;
    });
  }

  /** fsync: make all appended records durable. */
  flush(): Promise<void> {
    return this.enqueue(async () => {
      await this.fh!.sync();
    });
  }

  close(): Promise<void> {
    return this.enqueue(async () => {
      this.closedFlag = true;
      await this.fh!.close();
    });
  }

  private enqueue<T>(op: () => Promise<T>): Promise<T> {
    if (this.closedFlag) return Promise.reject(new Error("WAL writer is closed"));
    const run = this.chain.then(op);
    this.chain = run.catch(() => {});
    return run;
  }
}

/** Map a WAL file into a single contiguous buffer (mmap surrogate). */
export function mapWal(path: string): Buffer {
  return readFileSync(path);
}

export interface ScannedFrame {
  lsn: number;
  nextOffset: number;
  payload: Uint8Array;
}

export interface ScanOptions {
  /** Byte offset to start from; must be a frame boundary. */
  from?: number;
  /** Throw CorruptionError on torn tail / CRC mismatch instead of stopping. */
  strict?: boolean;
}

/** Iterate raw frames. Payloads are zero-copy views into `mapped`. */
export function* scanWalFrames(mapped: Buffer, opts: ScanOptions = {}): Generator<ScannedFrame> {
  decodeWalHeader(mapped);
  let offset = opts.from ?? WAL_HEADER_SIZE;
  if (offset < WAL_HEADER_SIZE || offset > mapped.length) {
    throw new CorruptionError("scan start out of bounds", offset);
  }
  while (offset + FRAME_HEADER_SIZE <= mapped.length) {
    const len = mapped.readUInt32LE(offset);
    const crc = mapped.readUInt32LE(offset + 4);
    const end = offset + FRAME_HEADER_SIZE + len;
    if (end > mapped.length) {
      if (opts.strict) throw new CorruptionError("truncated frame at tail", offset);
      break; // torn write from crash: logical end of log
    }
    const payload = mapped.subarray(offset + FRAME_HEADER_SIZE, end);
    if (crc32(payload) !== crc) {
      if (opts.strict) throw new CorruptionError("frame CRC mismatch", offset);
      break;
    }
    yield { lsn: offset, nextOffset: end, payload };
    offset = end;
  }
  if (opts.strict && offset !== mapped.length) {
    throw new CorruptionError("torn tail: trailing partial frame header", offset);
  }
}

export interface ScannedRecord {
  lsn: number;
  nextOffset: number;
  record: LogRecord;
}

/** Iterate decoded records. Image fields alias `mapped` (zero-copy). */
export function* scanWal(mapped: Buffer, opts: ScanOptions = {}): Generator<ScannedRecord> {
  for (const frame of scanWalFrames(mapped, opts)) {
    yield { lsn: frame.lsn, nextOffset: frame.nextOffset, record: decodeRecord(frame.payload) };
  }
}

/** Random-access a single record by LSN. */
export function readRecordAt(mapped: Buffer, lsn: number): LogRecord {
  if (lsn < WAL_HEADER_SIZE || lsn + FRAME_HEADER_SIZE > mapped.length) {
    throw new CorruptionError("LSN out of bounds", lsn);
  }
  const len = mapped.readUInt32LE(lsn);
  const crc = mapped.readUInt32LE(lsn + 4);
  const end = lsn + FRAME_HEADER_SIZE + len;
  if (end > mapped.length) throw new CorruptionError("record overruns buffer", lsn);
  const payload = mapped.subarray(lsn + FRAME_HEADER_SIZE, end);
  if (crc32(payload) !== crc) throw new CorruptionError("frame CRC mismatch", lsn);
  return decodeRecord(payload);
}
