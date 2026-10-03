/**
 * 预写日志 (WAL) 与崩溃恢复。
 *
 * 记录布局 (小端):
 *   magic u32 | type u8 | txId u32 | payloadLen u32 | payload | crc32 u32
 * crc 覆盖 type..payload。恢复时顺序扫描:
 *   - 校验和损坏 / 撕裂尾部 -> 截断到最后一条完好记录 (自愈)
 *   - 无 COMMIT 的事务 -> 回滚丢弃
 *   - CHECKPOINT 之前的记录视为已固化, 无需重放
 */

import { crc32 } from './crc32.ts';
import { ChecksumMismatchError } from './errors.ts';
import type { BlockDevice } from './device.ts';

export const RecordType = {
  Begin: 1,
  Op: 2,
  Commit: 3,
  Checkpoint: 4,
} as const;
export type RecordType = (typeof RecordType)[keyof typeof RecordType];

const MAGIC = 0x4a524e4c; // 'JRNL'
const HEADER_LEN = 4 + 1 + 4 + 4;
const CRC_LEN = 4;

export interface JournalRecord {
  type: RecordType;
  txId: number;
  payload: Uint8Array;
  /** 记录在设备上的起始偏移。 */
  offset: number;
  /** 记录总长度 (含头与 crc)。 */
  length: number;
}

export function encodeRecord(type: RecordType, txId: number, payload: Uint8Array): Uint8Array {
  const out = new Uint8Array(HEADER_LEN + payload.length + CRC_LEN);
  const view = new DataView(out.buffer);
  view.setUint32(0, MAGIC, true);
  view.setUint8(4, type);
  view.setUint32(5, txId, true);
  view.setUint32(9, payload.length, true);
  out.set(payload, HEADER_LEN);
  const crc = crc32(out.subarray(4, HEADER_LEN + payload.length));
  view.setUint32(HEADER_LEN + payload.length, crc, true);
  return out;
}

export function recordLength(payloadLength: number): number {
  return HEADER_LEN + payloadLength + CRC_LEN;
}

export interface RecoveryResult {
  /** 已提交事务 (txId 升序, 仅含 checkpoint 之后的)。 */
  committed: { txId: number; ops: Uint8Array[] }[];
  /** 被回滚的未提交事务数。 */
  rolledBack: number;
  /** 截断掉的撕裂/损坏字节数。 */
  truncatedBytes: number;
  /** 恢复后日志的逻辑写入位置。 */
  tailOffset: number;
  /** 最后一个 checkpoint 的偏移 (-1 表示无)。 */
  checkpointOffset: number;
  /** 最后一个 checkpoint 携带的状态快照 (无则 null)。 */
  checkpointPayload: Uint8Array | null;
}

/** 顺序扫描设备上的日志区, 返回全部完好记录与扫描终止位置。 */
/** 从 offset 起统计非零垃圾字节的范围 (全零区域视为干净空闲空间)。 */
function garbageExtent(device: BlockDevice, offset: number, usedBytes: number): number {
  const rest = device.read(offset, usedBytes - offset);
  let lastNonZero = -1;
  for (let i = rest.length - 1; i >= 0; i--) {
    if (rest[i] !== 0) {
      lastNonZero = i;
      break;
    }
  }
  return lastNonZero + 1;
}

export function scanJournal(
  device: BlockDevice,
  usedBytes: number,
): { records: JournalRecord[]; goodTail: number; truncatedBytes: number } {
  const records: JournalRecord[] = [];
  let offset = 0;
  let truncated = 0;

  while (offset + HEADER_LEN + CRC_LEN <= usedBytes) {
    const header = device.read(offset, HEADER_LEN);
    const view = new DataView(header.buffer, header.byteOffset, HEADER_LEN);
    if (view.getUint32(0, true) !== MAGIC) {
      truncated = garbageExtent(device, offset, usedBytes);
      break;
    }
    const type = view.getUint8(4) as RecordType;
    const txId = view.getUint32(5, true);
    const payloadLen = view.getUint32(9, true);
    const total = recordLength(payloadLen);
    if (offset + total > usedBytes) {
      truncated = garbageExtent(device, offset, usedBytes); // 撕裂写尾部
      break;
    }
    const body = device.read(offset + 4, total - 4);
    const storedCrc = new DataView(body.buffer, body.byteOffset + body.length - 4, 4).getUint32(0, true);
    if (crc32(body.subarray(0, body.length - 4)) !== storedCrc) {
      truncated = garbageExtent(device, offset, usedBytes); // 损坏记录及其后全部丢弃
      break;
    }
    records.push({
      type,
      txId,
      payload: body.subarray(9, 9 + payloadLen),
      offset,
      length: total,
    });
    offset += total;
  }
  return { records, goodTail: offset, truncatedBytes: truncated };
}

/** 崩溃恢复: 截断撕裂尾部, 区分已提交/待回滚事务。 */
export function recoverJournal(device: BlockDevice, usedBytes: number): RecoveryResult {
  const { records, goodTail, truncatedBytes } = scanJournal(device, usedBytes);

  let checkpointIdx = -1;
  for (let i = records.length - 1; i >= 0; i--) {
    if (records[i].type === RecordType.Checkpoint) {
      checkpointIdx = i;
      break;
    }
  }

  const txOps = new Map<number, Uint8Array[]>();
  const committedOrder: number[] = [];
  let rolledBack = 0;

  for (const rec of records.slice(checkpointIdx + 1)) {
    if (rec.type === RecordType.Begin) {
      txOps.set(rec.txId, []);
    } else if (rec.type === RecordType.Op) {
      txOps.get(rec.txId)?.push(rec.payload);
    } else if (rec.type === RecordType.Commit) {
      if (txOps.has(rec.txId)) committedOrder.push(rec.txId);
    }
  }
  for (const [txId] of txOps) {
    if (!committedOrder.includes(txId)) rolledBack++;
  }

  return {
    committed: committedOrder.map((txId) => ({ txId, ops: txOps.get(txId)! })),
    rolledBack,
    truncatedBytes,
    tailOffset: goodTail,
    checkpointOffset: checkpointIdx >= 0 ? records[checkpointIdx].offset : -1,
    checkpointPayload: checkpointIdx >= 0 ? records[checkpointIdx].payload : null,
  };
}

/** 日志写入器: 追加记录, 维护逻辑尾偏移。 */
export class JournalWriter {
  private tail: number;
  private readonly device: BlockDevice;
  constructor(device: BlockDevice, startOffset = 0) {
    this.device = device;
    this.tail = startOffset;
  }

  get offset(): number {
    return this.tail;
  }

  append(type: RecordType, txId: number, payload: Uint8Array): void {
    const rec = encodeRecord(type, txId, payload);
    this.device.write(this.tail, rec);
    this.tail += rec.length;
  }

  /** 自愈: 截断到指定偏移 (覆盖撕裂尾部)。 */
  truncateTo(offset: number): void {
    if (offset > this.tail) {
      throw new ChecksumMismatchError('truncate 目标超过当前尾部');
    }
    this.tail = offset;
  }
}
