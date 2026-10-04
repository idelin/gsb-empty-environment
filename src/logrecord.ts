/**
 * ARIES log record model and versioned binary serialization.
 *
 * Payload layout (inside a WAL frame):
 *   u8      formatVersion   (current = 2)
 *   u8      recordType
 *   varint  lsn             (byte offset of the frame in the WAL file)
 *   varint  txnId           (0 for checkpoint records)
 *   varint  prevLSN         (0 = none)
 *   ... type-specific fields ...
 *
 * Versioning / compatibility:
 *  - v1: UPDATE records carry no `offset` field (implicit 0).
 *  - v2: adds `offset` to UPDATE/CLR.
 *  Readers accept any version <= SUPPORTED_VERSION (backward compat) and
 *  ignore trailing bytes of newer payloads (forward compat), since frames
 *  are length-delimited.
 */

import { Reader, Writer, CodecError } from "./codec";

export const FORMAT_VERSION = 2;
export const SUPPORTED_MAX_VERSION = 3; // tolerate one future minor version

export enum RecordType {
  Begin = 1,
  Update = 2,
  Commit = 3,
  Abort = 4,
  End = 5,
  Clr = 6,
  Checkpoint = 7,
}

export type TxnStatus = "running" | "committed" | "aborting";

export interface TxnEntry {
  txnId: number;
  status: TxnStatus;
  lastLSN: number;
}

interface RecordBase {
  lsn: number;
  txnId: number;
  prevLSN: number;
}

export interface BeginRecord extends RecordBase {
  type: RecordType.Begin;
}

export interface UpdateRecord extends RecordBase {
  type: RecordType.Update;
  pageId: number;
  offset: number;
  /** Zero-copy views into the WAL frame buffer. */
  before: Buffer;
  after: Buffer;
}

export interface ClrRecord extends RecordBase {
  type: RecordType.Clr;
  pageId: number;
  offset: number;
  before: Buffer;
  after: Buffer;
  undoNextLSN: number;
}

export interface CommitRecord extends RecordBase {
  type: RecordType.Commit;
}

export interface AbortRecord extends RecordBase {
  type: RecordType.Abort;
}

export interface EndRecord extends RecordBase {
  type: RecordType.End;
}

export interface CheckpointRecord extends RecordBase {
  type: RecordType.Checkpoint;
  txns: TxnEntry[];
  dirtyPages: Array<[pageId: number, recLSN: number]>;
}

export type LogRecord =
  | BeginRecord
  | UpdateRecord
  | CommitRecord
  | AbortRecord
  | EndRecord
  | ClrRecord
  | CheckpointRecord;

const STATUS_TO_CODE: Record<TxnStatus, number> = {
  running: 1,
  committed: 2,
  aborting: 3,
};
const CODE_TO_STATUS: TxnStatus[] = ["running", "running", "committed", "aborting"];

// ------------------------------------------------------------ serialize --

export function serializeRecord(rec: LogRecord): Buffer {
  const w = new Writer(64);
  w.writeU8(FORMAT_VERSION);
  w.writeU8(rec.type);
  w.writeVarint(rec.lsn);
  w.writeVarint(rec.txnId);
  w.writeVarint(rec.prevLSN);
  switch (rec.type) {
    case RecordType.Update:
    case RecordType.Clr: {
      w.writeVarint(rec.pageId);
      w.writeVarint(rec.offset); // v2 field
      w.writeVarint(rec.before.length);
      w.writeBytes(rec.before);
      w.writeVarint(rec.after.length);
      w.writeBytes(rec.after);
      if (rec.type === RecordType.Clr) w.writeVarint(rec.undoNextLSN);
      break;
    }
    case RecordType.Checkpoint: {
      w.writeVarint(rec.txns.length);
      for (const t of rec.txns) {
        w.writeVarint(t.txnId);
        w.writeU8(STATUS_TO_CODE[t.status]);
        w.writeVarint(t.lastLSN);
      }
      w.writeVarint(rec.dirtyPages.length);
      for (const [pageId, recLSN] of rec.dirtyPages) {
        w.writeVarint(pageId);
        w.writeVarint(recLSN);
      }
      break;
    }
    default:
      break; // begin/commit/abort/end carry no extra fields
  }
  return w.toBuffer();
}

// ---------------------------------------------------------- deserialize --

/**
 * Parses one record payload. Zero-copy: byte-slice fields are views into
 * `payload`. Trailing bytes beyond the known layout are ignored so newer
 * writers remain readable (forward compatibility).
 */
export function deserializeRecord(payload: Buffer): LogRecord {
  const r = new Reader(payload);
  const version = r.readU8();
  if (version < 1 || version > SUPPORTED_MAX_VERSION) {
    throw new CodecError(`unsupported record format version ${version}`, 0);
  }
  const type = r.readU8() as RecordType;
  const lsn = r.readVarint();
  const txnId = r.readVarint();
  const prevLSN = r.readVarint();
  const base = { lsn, txnId, prevLSN };

  switch (type) {
    case RecordType.Begin:
      return { type, ...base };
    case RecordType.Commit:
      return { type, ...base };
    case RecordType.Abort:
      return { type, ...base };
    case RecordType.End:
      return { type, ...base };
    case RecordType.Update:
    case RecordType.Clr: {
      const pageId = r.readVarint();
      // v1 records have no offset field; default to 0 (backward compat).
      const offset = version >= 2 ? r.readVarint() : 0;
      const beforeLen = r.readVarint();
      const before = r.readBytes(beforeLen);
      const afterLen = r.readVarint();
      const after = r.readBytes(afterLen);
      if (type === RecordType.Update) {
        return { type, ...base, pageId, offset, before, after };
      }
      const undoNextLSN = r.readVarint();
      return { type, ...base, pageId, offset, before, after, undoNextLSN };
    }
    case RecordType.Checkpoint: {
      const txnCount = r.readVarint();
      const txns: TxnEntry[] = [];
      for (let i = 0; i < txnCount; i++) {
        const id = r.readVarint();
        const statusCode = r.readU8();
        const status = CODE_TO_STATUS[statusCode];
        if (status === undefined || statusCode === 0) {
          throw new CodecError(`invalid txn status code ${statusCode}`, r.offset);
        }
        txns.push({ txnId: id, status, lastLSN: r.readVarint() });
      }
      const pageCount = r.readVarint();
      const dirtyPages: Array<[number, number]> = [];
      for (let i = 0; i < pageCount; i++) {
        dirtyPages.push([r.readVarint(), r.readVarint()]);
      }
      return { type, ...base, txns, dirtyPages };
    }
    default:
      throw new CodecError(`unknown record type ${type}`, 1);
  }
}

export function recordTypeName(rec: LogRecord): string {
  return RecordType[rec.type];
}
