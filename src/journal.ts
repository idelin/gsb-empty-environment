import { crc32 } from './crc32.ts';
import { ProtoError } from './errors.ts';
import type { ByteSink } from './fault.ts';

/**
 * 预写日志（WAL）记录格式：
 *   [payloadLen u32 LE][crc32 u32 LE][payload...]
 * crc 覆盖 payloadLen 与 payload。
 * 崩溃恢复：顺序扫描，遇到校验失败/撕裂尾部即截断，实现自愈。
 */

export const RECORD_HEADER_SIZE = 8;

export function encodeRecord(payload: Uint8Array): Uint8Array {
  const rec = new Uint8Array(RECORD_HEADER_SIZE + payload.length);
  const view = new DataView(rec.buffer);
  view.setUint32(0, payload.length, true);
  rec.set(payload, RECORD_HEADER_SIZE);
  const bodyCrc = crc32(payload, crc32(rec.subarray(0, 4)));
  view.setUint32(4, bodyCrc, true);
  return rec;
}

export interface ScanResult {
  payloads: Uint8Array[];
  /** 最后一个完好记录的结束偏移（即安全截断点）。 */
  goodLength: number;
  /** 是否发现并修复了损坏尾部。 */
  repaired: boolean;
}

/** 顺序扫描字节流，返回完好记录与截断点。 */
export function scanRecords(data: Uint8Array): ScanResult {
  const payloads: Uint8Array[] = [];
  let offset = 0;
  let repaired = false;
  while (offset < data.length) {
    if (offset + RECORD_HEADER_SIZE > data.length) {
      repaired = true; // 撕裂的头部
      break;
    }
    const view = new DataView(data.buffer, data.byteOffset + offset, RECORD_HEADER_SIZE);
    const len = view.getUint32(0, true);
    const storedCrc = view.getUint32(4, true);
    if (len > data.length - offset - RECORD_HEADER_SIZE) {
      repaired = true; // 撕裂的载荷
      break;
    }
    const payload = data.subarray(offset + RECORD_HEADER_SIZE, offset + RECORD_HEADER_SIZE + len);
    const actual = crc32(payload, crc32(data.subarray(offset, offset + 4)));
    if (actual !== storedCrc) {
      repaired = true; // 校验失败：半写记录
      break;
    }
    payloads.push(payload.slice());
    offset += RECORD_HEADER_SIZE + len;
  }
  return { payloads, goodLength: offset, repaired };
}

/** 基于 ByteSink 的日志：append 原子化（单条记录一次写入），recover 自愈截断。 */
export class Journal {
  private readonly sink: ByteSink;

  constructor(sink: ByteSink) {
    this.sink = sink;
  }

  append(payload: Uint8Array): void {
    this.sink.append(encodeRecord(payload));
  }

  /** 崩溃恢复：重放完好记录，并把文件截断到最后完好位置（自愈）。 */
  recover(): Uint8Array[] {
    const { payloads, goodLength, repaired } = scanRecords(this.sink.read());
    if (repaired) this.sink.truncate(goodLength);
    return payloads;
  }

  get size(): number {
    return this.sink.size;
  }
}

/** 校验扫描结果供断言用。 */
export function assertIntact(data: Uint8Array): void {
  const { repaired } = scanRecords(data);
  if (repaired) throw new ProtoError('CHECKSUM_MISMATCH', '日志存在损坏记录');
}
