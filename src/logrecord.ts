/**
 * Versioned binary codec for ARIES log records.
 *
 * Record payload layout (format v1, all integers little-endian):
 *   u8  version          - record format version (forward/backward compat gate)
 *   u8  type             - RecordType
 *   u64 lsn              - log sequence number (= file offset of the frame)
 *   u32 txnId            - 0 for checkpoint records
 *   u64 prevLSN          - previous LSN of the same transaction
 *   ... type-specific body
 *
 * UPDATE body:  u32 pageId, u32 offset, lenBytes beforeImage, lenBytes afterImage
 * CLR body:     UPDATE body + u64 undoNextLSN
 * END_CHECKPOINT body:
 *   u64 beginLSN, u16 txnCount, txnCount * {u32 txnId, u8 status, u64 lastLSN},
 *   u16 pageCount, pageCount * {u32 pageId, u64 recLSN}
 *
 * Compatibility rules:
 *  - version 0 is rejected (CorruptionError);
 *  - version 1 parses the full layout above;
 *  - version > FORMAT_VERSION parses the known v1 prefix and ignores unknown
 *    trailing bytes (forward compatibility), since the frame length prefix
 *    delimits the record;
 *  - unknown record types decode to an UNKNOWN record so scanners can skip
 *    them without failing (backward tolerance for mixed-version logs).
 *
 * Zero-copy: decoded before/after images are subarray views aliasing the
 * source buffer. Callers that need ownership must copy explicitly.
 */

import { BoundedReader, BoundedWriter, CorruptionError } from "./binary.ts";

export const FORMAT_VERSION = 1;

export const RecordType = {
  UNKNOWN: 0,
  UPDATE: 1,
  COMMIT: 2,
  ABORT: 3,
  END: 4,
  CLR: 5,
  BEGIN_CHECKPOINT: 6,
  END_CHECKPOINT: 7,
} as const;

export const TxnStatus = {
  RUNNING: 1,
  COMMITTED: 2,
  ABORTING: 3,
} as const;

export interface CheckpointTxnEntry {
  txnId: number;
  status: number;
  lastLSN: number;
}

export interface CheckpointPageEntry {
  pageId: number;
  recLSN: number;
}

interface RecordBase {
  lsn: number;
  txnId: number;
  prevLSN: number;
}

export interface UpdateRecord extends RecordBase {
  type: typeof RecordType.UPDATE;
  pageId: number;
  offset: number;
  beforeImage: Uint8Array;
  afterImage: Uint8Array;
}

export interface ClrRecord extends RecordBase {
  type: typeof RecordType.CLR;
  pageId: number;
  offset: number;
  beforeImage: Uint8Array;
  afterImage: Uint8Array;
  undoNextLSN: number;
}

export interface TxnRecord extends RecordBase {
  type: typeof RecordType.COMMIT | typeof RecordType.ABORT | typeof RecordType.END;
}

export interface BeginCheckpointRecord extends RecordBase {
  type: typeof RecordType.BEGIN_CHECKPOINT;
}

export interface EndCheckpointRecord extends RecordBase {
  type: typeof RecordType.END_CHECKPOINT;
  beginLSN: number;
  txns: CheckpointTxnEntry[];
  pages: CheckpointPageEntry[];
}

export interface UnknownRecord extends RecordBase {
  type: typeof RecordType.UNKNOWN;
  rawType: number;
}

export type LogRecord =
  | UpdateRecord
  | ClrRecord
  | TxnRecord
  | BeginCheckpointRecord
  | EndCheckpointRecord
  | UnknownRecord;

export function encodeRecord(rec: LogRecord): Buffer {
  const w = new BoundedWriter(64);
  w.writeU8(FORMAT_VERSION);
  w.writeU8(rec.type);
  w.writeU64(rec.lsn);
  w.writeU32(rec.txnId);
  w.writeU64(rec.prevLSN);
  switch (rec.type) {
    case RecordType.UPDATE:
    case RecordType.CLR:
      w.writeU32(rec.pageId);
      w.writeU32(rec.offset);
      w.writeLenBytes(rec.beforeImage);
      w.writeLenBytes(rec.afterImage);
      if (rec.type === RecordType.CLR) w.writeU64(rec.undoNextLSN);
      break;
    case RecordType.COMMIT:
    case RecordType.ABORT:
    case RecordType.END:
    case RecordType.BEGIN_CHECKPOINT:
      break;
    case RecordType.END_CHECKPOINT:
      w.writeU64(rec.beginLSN);
      w.writeU16(rec.txns.length);
      for (const t of rec.txns) {
        w.writeU32(t.txnId);
        w.writeU8(t.status);
        w.writeU64(t.lastLSN);
      }
      w.writeU16(rec.pages.length);
      for (const p of rec.pages) {
        w.writeU32(p.pageId);
        w.writeU64(p.recLSN);
      }
      break;
    default:
      throw new Error(`cannot encode record of type ${(rec as UnknownRecord).rawType ?? rec.type}`);
  }
  return Buffer.from(w.toBuffer());
}

/** Decode a record payload. Image fields are zero-copy views into `payload`. */
export function decodeRecord(payload: Uint8Array): LogRecord {
  const r = new BoundedReader(payload);
  const version = r.readU8();
  if (version < 1) {
    throw new CorruptionError(`unsupported record version ${version}`, 0);
  }
  // version > FORMAT_VERSION: parse the known v1 prefix, ignore trailing bytes.
  const type = r.readU8();
  const lsn = r.readU64Number();
  const txnId = r.readU32();
  const prevLSN = r.readU64Number();
  switch (type) {
    case RecordType.UPDATE:
    case RecordType.CLR: {
      const pageId = r.readU32();
      const offset = r.readU32();
      const beforeImage = r.readLenBytes();
      const afterImage = r.readLenBytes();
      if (type === RecordType.CLR) {
        const undoNextLSN = r.readU64Number();
        return { type, lsn, txnId, prevLSN, pageId, offset, beforeImage, afterImage, undoNextLSN };
      }
      return { type, lsn, txnId, prevLSN, pageId, offset, beforeImage, afterImage };
    }
    case RecordType.COMMIT:
    case RecordType.ABORT:
    case RecordType.END:
      return { type, lsn, txnId, prevLSN };
    case RecordType.BEGIN_CHECKPOINT:
      return { type, lsn, txnId, prevLSN };
    case RecordType.END_CHECKPOINT: {
      const beginLSN = r.readU64Number();
      const txnCount = r.readU16();
      const txns: CheckpointTxnEntry[] = [];
      for (let i = 0; i < txnCount; i++) {
        txns.push({ txnId: r.readU32(), status: r.readU8(), lastLSN: r.readU64Number() });
      }
      const pageCount = r.readU16();
      const pages: CheckpointPageEntry[] = [];
      for (let i = 0; i < pageCount; i++) {
        pages.push({ pageId: r.readU32(), recLSN: r.readU64Number() });
      }
      return { type, lsn, txnId, prevLSN, beginLSN, txns, pages };
    }
    default:
      return { type: RecordType.UNKNOWN, rawType: type, lsn, txnId, prevLSN };
  }
}
