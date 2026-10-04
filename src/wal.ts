/**
 * Write-ahead log file: length+CRC framed records, LSN = byte offset of the
 * frame in the file. Appends are serialized through an async mutex so
 * concurrent writers can never interleave a frame or duplicate an LSN.
 *
 * Parsing is "memory-mapped style": the file is materialized as one Buffer
 * and frames are decoded lazily from it; record byte-slices are zero-copy
 * views into that buffer.
 */

import * as fs from "fs";
import { crc32, MAX_FRAME_PAYLOAD, CodecError } from "./codec";
import { LogRecord, serializeRecord, deserializeRecord } from "./logrecord";

export const FRAME_HEADER_SIZE = 8; // u32 payloadLen + u32 crc32

export interface ScannedFrame {
  lsn: number;
  record: LogRecord;
}

export interface ScanResult {
  frames: number;
  /** Offset where valid frames end (== file size unless the tail is torn). */
  validEnd: number;
  tornTail: boolean;
}

export class Wal {
  private fd: number;
  private endOffset = 0;
  private tail: Promise<unknown> = Promise.resolve();
  private closed = false;

  private constructor(public readonly path: string) {
    this.fd = fs.openSync(path, "a+");
    this.endOffset = this.findValidEnd();
    // Truncate a torn tail left by a crash mid-write.
    fs.ftruncateSync(this.fd, this.endOffset);
  }

  static open(path: string): Wal {
    return new Wal(path);
  }

  get isClosed(): boolean {
    return this.closed;
  }

  /** LSN that the next appended record will receive. */
  get nextLSN(): number {
    return this.endOffset;
  }

  /** Walks frames and returns the offset just past the last valid one. */
  private findValidEnd(): number {
    const size = fs.fstatSync(this.fd).size;
    if (size === 0) return 0;
    const buf = Buffer.allocUnsafe(size);
    fs.readSync(this.fd, buf, 0, size, 0);
    let offset = 0;
    while (offset + FRAME_HEADER_SIZE <= size) {
      const payloadLen = buf.readUInt32LE(offset);
      if (payloadLen === 0 || payloadLen > MAX_FRAME_PAYLOAD) break;
      const frameEnd = offset + FRAME_HEADER_SIZE + payloadLen;
      if (frameEnd > size) break; // torn: payload never fully written
      const crc = buf.readUInt32LE(offset + 4);
      const payload = buf.subarray(offset + FRAME_HEADER_SIZE, frameEnd);
      if (crc32(payload) !== crc) break; // torn/corrupt payload
      offset = frameEnd;
    }
    return offset;
  }

  private writeFrame(payload: Buffer): number {
    const lsn = this.endOffset;
    const frame = Buffer.allocUnsafe(FRAME_HEADER_SIZE + payload.length);
    frame.writeUInt32LE(payload.length, 0);
    frame.writeUInt32LE(crc32(payload), 4);
    payload.copy(frame, FRAME_HEADER_SIZE);
    fs.writeSync(this.fd, frame, 0, frame.length, lsn);
    fs.fsyncSync(this.fd);
    this.endOffset = lsn + frame.length;
    return lsn;
  }

  /**
   * Synchronous append. Must not be called while async appends are in
   * flight; recovery and single-threaded writers use this path.
   */
  appendSync(rec: LogRecord): number {
    this.assertOpen();
    if (this.pendingAsync) {
      throw new Error("Wal: sync append while async appends are pending");
    }
    const lsn = this.endOffset;
    const payload = serializeRecord({ ...rec, lsn });
    return this.writeFrame(payload);
  }

  private pendingAsync = false;

  /**
   * Concurrency-safe append: callers are serialized on an internal promise
   * chain, so LSN assignment and frame writes are atomic with respect to
   * other async appends.
   */
  append(rec: LogRecord): Promise<number> {
    this.assertOpen();
    this.pendingAsync = true;
    const result = this.tail.then(() => {
      const lsn = this.endOffset;
      const payload = serializeRecord({ ...rec, lsn });
      return this.writeFrame(payload);
    });
    this.tail = result.catch(() => undefined);
    return result.finally(() => {
      this.pendingAsync = false;
    });
  }

  /** Reads the whole log into one buffer (mmap-style view of the file). */
  private loadView(): Buffer {
    const size = this.endOffset;
    const buf = Buffer.allocUnsafe(size === 0 ? 1 : size);
    if (size > 0) fs.readSync(this.fd, buf, 0, size, 0);
    return buf;
  }

  /**
   * Lazy zero-copy scan from `fromLSN` (inclusive) to the end of the log.
   * Record payloads reference the shared file buffer; nothing is copied.
   */
  *scan(fromLSN = 0): Generator<ScannedFrame> {
    this.assertOpen();
    const view = this.loadView();
    let offset = fromLSN;
    while (offset + FRAME_HEADER_SIZE <= view.length) {
      const payloadLen = view.readUInt32LE(offset);
      if (payloadLen === 0 || payloadLen > MAX_FRAME_PAYLOAD) {
        throw new CodecError(`invalid frame length ${payloadLen}`, offset);
      }
      const frameEnd = offset + FRAME_HEADER_SIZE + payloadLen;
      if (frameEnd > view.length) {
        throw new CodecError("truncated frame", offset);
      }
      const crc = view.readUInt32LE(offset + 4);
      const payload = view.subarray(offset + FRAME_HEADER_SIZE, frameEnd);
      if (crc32(payload) !== crc) {
        throw new CodecError("frame CRC mismatch", offset);
      }
      yield { lsn: offset, record: deserializeRecord(payload) };
      offset = frameEnd;
    }
    if (offset !== view.length) {
      throw new CodecError("dangling bytes after last frame", offset);
    }
  }

  /** Reads a single record at the given LSN (O(1) seek). */
  readRecord(lsn: number): LogRecord {
    this.assertOpen();
    const header = Buffer.allocUnsafe(FRAME_HEADER_SIZE);
    if (fs.readSync(this.fd, header, 0, FRAME_HEADER_SIZE, lsn) < FRAME_HEADER_SIZE) {
      throw new CodecError("LSN out of range", lsn);
    }
    const payloadLen = header.readUInt32LE(0);
    if (payloadLen === 0 || payloadLen > MAX_FRAME_PAYLOAD) {
      throw new CodecError(`invalid frame length ${payloadLen}`, lsn);
    }
    const payload = Buffer.allocUnsafe(payloadLen);
    if (fs.readSync(this.fd, payload, 0, payloadLen, lsn + FRAME_HEADER_SIZE) < payloadLen) {
      throw new CodecError("truncated frame", lsn);
    }
    if (crc32(payload) !== header.readUInt32LE(4)) {
      throw new CodecError("frame CRC mismatch", lsn);
    }
    return deserializeRecord(payload);
  }

  private assertOpen(): void {
    if (this.closed) throw new Error("Wal: already closed");
  }

  close(): void {
    if (this.closed) return;
    this.closed = true;
    fs.closeSync(this.fd);
  }
}
