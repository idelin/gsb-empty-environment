/**
 * ARIES WAL record model.
 *
 * Every record carries a monotonically increasing LSN and a prevLSN pointer
 * that chains all records of the same transaction (the "transaction chain").
 * CLR records additionally carry undoNextLSN, which points to the next record
 * that still has to be undone for that transaction.
 */

export type LSN = number;
export type TxnId = string;
export type PageId = string;
export type Value = string | number | null;

export type TxnStatus = 'running' | 'committed' | 'aborted';

export interface TxnTableSnapshotEntry {
  txnId: TxnId;
  status: TxnStatus;
  lastLSN: LSN;
}

export interface DirtyPageSnapshotEntry {
  pageId: PageId;
  recLSN: LSN;
}

interface RecordBase {
  lsn: LSN;
  prevLSN: LSN | null;
  txnId: TxnId | null;
}

export interface BeginRecord extends RecordBase {
  kind: 'BEGIN';
  txnId: TxnId;
}

export interface UpdateRecord extends RecordBase {
  kind: 'UPDATE';
  txnId: TxnId;
  pageId: PageId;
  key: string;
  before: Value;
  after: Value;
}

export interface ClrRecord extends RecordBase {
  kind: 'CLR';
  txnId: TxnId;
  pageId: PageId;
  key: string;
  /** value being replaced by the compensation (the update's after-image) */
  before: Value;
  /** value restored by the compensation (the update's before-image) */
  after: Value;
  undoNextLSN: LSN | null;
}

export interface CommitRecord extends RecordBase {
  kind: 'COMMIT';
  txnId: TxnId;
}

export interface AbortRecord extends RecordBase {
  kind: 'ABORT';
  txnId: TxnId;
}

export interface EndRecord extends RecordBase {
  kind: 'END';
  txnId: TxnId;
}

export interface CheckpointRecord extends RecordBase {
  kind: 'CHECKPOINT';
  txnId: null;
  txnTable: TxnTableSnapshotEntry[];
  dirtyPageTable: DirtyPageSnapshotEntry[];
}

export type LogRecord =
  | BeginRecord
  | UpdateRecord
  | ClrRecord
  | CommitRecord
  | AbortRecord
  | EndRecord
  | CheckpointRecord;

type StripLsn<T> = Omit<T, 'lsn' | 'prevLSN'>;

/** A record as supplied by callers; LSN and prevLSN are assigned by the WAL. */
export type LogRecordDraft =
  | StripLsn<BeginRecord>
  | StripLsn<UpdateRecord>
  | StripLsn<ClrRecord>
  | StripLsn<CommitRecord>
  | StripLsn<AbortRecord>
  | StripLsn<EndRecord>
  | StripLsn<CheckpointRecord>;

const KINDS = new Set(['BEGIN', 'UPDATE', 'CLR', 'COMMIT', 'ABORT', 'END', 'CHECKPOINT']);

export function serializeRecord(record: LogRecord): string {
  return JSON.stringify(record);
}

export function parseRecord(line: string): LogRecord {
  const raw: unknown = JSON.parse(line);
  if (typeof raw !== 'object' || raw === null) {
    throw new Error(`malformed log record: not an object`);
  }
  const r = raw as Record<string, unknown>;
  if (typeof r.kind !== 'string' || !KINDS.has(r.kind)) {
    throw new Error(`malformed log record: unknown kind ${String(r.kind)}`);
  }
  if (typeof r.lsn !== 'number' || !Number.isInteger(r.lsn) || r.lsn < 1) {
    throw new Error(`malformed log record: invalid lsn ${String(r.lsn)}`);
  }
  if (r.prevLSN !== null && typeof r.prevLSN !== 'number') {
    throw new Error(`malformed log record: invalid prevLSN`);
  }
  if (r.kind === 'CHECKPOINT') {
    if (!Array.isArray(r.txnTable) || !Array.isArray(r.dirtyPageTable)) {
      throw new Error(`malformed checkpoint record: missing tables`);
    }
  } else if (typeof r.txnId !== 'string' || r.txnId.length === 0) {
    throw new Error(`malformed log record: invalid txnId`);
  }
  return raw as LogRecord;
}

export function formatRecord(r: LogRecord): string {
  const prev = r.prevLSN === null ? '-' : String(r.prevLSN);
  switch (r.kind) {
    case 'UPDATE':
      return `LSN=${r.lsn} UPDATE  txn=${r.txnId} prev=${prev} ${r.pageId}.${r.key}: ${fmt(r.before)} -> ${fmt(r.after)}`;
    case 'CLR':
      return `LSN=${r.lsn} CLR     txn=${r.txnId} prev=${prev} ${r.pageId}.${r.key}: ${fmt(r.before)} -> ${fmt(r.after)} undoNext=${r.undoNextLSN ?? '-'}`;
    case 'CHECKPOINT':
      return `LSN=${r.lsn} CHECKPOINT txns=[${r.txnTable.map((e) => `${e.txnId}:${e.status}@${e.lastLSN}`).join(', ')}] dirty=[${r.dirtyPageTable.map((e) => `${e.pageId}@${e.recLSN}`).join(', ')}]`;
    default:
      return `LSN=${r.lsn} ${r.kind.padEnd(7)} txn=${r.txnId} prev=${prev}`;
  }
}

function fmt(v: Value | undefined): string {
  return v === undefined ? '<absent>' : JSON.stringify(v);
}
