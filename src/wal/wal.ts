import { closeSync, existsSync, openSync, readSync, writeSync, readFileSync } from "node:fs";
import { EngineError, ErrorCodes } from "../errors.ts";
import type { MetricsRegistry } from "../metrics/registry.ts";
import { crc32, decodeRecord, encodeRecord, type LogRecord } from "./record.ts";

const HEADER_SIZE = 8; // [u32 payload length][u32 crc32]

/**
 * Frame-based write-ahead log. Each frame: length + CRC32 + JSON payload.
 * A torn tail (partial frame / checksum mismatch) is detected on read and
 * treated as the crash point: scanning stops there, error metrics recorded.
 */
export class WalWriter {
  private fd: number;
  private nextLSN = 1;
  private closedFlag = false;
  private readonly appendTimer;
  readonly path: string;
  private readonly metrics: MetricsRegistry;

  constructor(path: string, metrics: MetricsRegistry, mode: "w" | "a" = "w", startLSN = 1) {
    this.path = path;
    this.metrics = metrics;
    this.fd = openSync(path, mode);
    this.nextLSN = startLSN;
    this.appendTimer = metrics.timer("wal.append.duration_ms");
  }

  /** Opens an existing WAL for appending recovery records (CLRs, ENDs). */
  static append(path: string, metrics: MetricsRegistry, startLSN: number): WalWriter {
    return new WalWriter(path, metrics, "a", startLSN);
  }

  append(record: Omit<LogRecord, "lsn">): LogRecord {
    if (this.closedFlag) {
      throw new EngineError(ErrorCodes.WAL_IO, "append on closed WAL", { path: this.path });
    }
    const full: LogRecord = { ...record, lsn: this.nextLSN++ };
    const payload = encodeRecord(full);
    const frame = new Uint8Array(HEADER_SIZE + payload.length);
    const view = new DataView(frame.buffer);
    view.setUint32(0, payload.length, true);
    view.setUint32(4, crc32(payload), true);
    frame.set(payload, HEADER_SIZE);
    this.appendTimer.time(() => {
      writeSync(this.fd, frame);
    });
    this.metrics.counter("wal.records_appended", { type: full.type }).inc();
    return full;
  }

  /** fsync-equivalent durability barrier. */
  flush(): void {
    // writeSync is already durable enough for this single-process simulation;
    // the hook exists so commit paths explicitly declare the barrier.
  }

  close(): void {
    if (!this.closedFlag) {
      this.closedFlag = true;
      closeSync(this.fd);
    }
  }

  get closed(): boolean {
    return this.closedFlag;
  }
}

export interface WalScanResult {
  records: LogRecord[];
  /** True when the tail was torn/corrupt (normal after a crash). */
  tornTail: boolean;
}

export function scanWal(path: string, metrics: MetricsRegistry): WalScanResult {
  const records: LogRecord[] = [];
  if (!existsSync(path)) {
    throw new EngineError(ErrorCodes.WAL_IO, "WAL file does not exist", { path });
  }
  const buf = readFileSync(path);
  let offset = 0;
  let tornTail = false;
  while (offset < buf.length) {
    if (offset + HEADER_SIZE > buf.length) {
      tornTail = true;
      break;
    }
    const length = buf.readUInt32LE(offset);
    const expectedCrc = buf.readUInt32LE(offset + 4);
    if (offset + HEADER_SIZE + length > buf.length) {
      tornTail = true;
      break;
    }
    const payload = buf.subarray(offset + HEADER_SIZE, offset + HEADER_SIZE + length);
    if (crc32(payload) !== expectedCrc) {
      metrics.recordError(ErrorCodes.WAL_CHECKSUM_MISMATCH);
      tornTail = true;
      break;
    }
    try {
      records.push(decodeRecord(payload));
    } catch {
      metrics.recordError(ErrorCodes.WAL_TORN_WRITE);
      tornTail = true;
      break;
    }
    offset += HEADER_SIZE + length;
  }
  if (tornTail) metrics.counter("wal.torn_tail").inc();
  return { records, tornTail };
}
