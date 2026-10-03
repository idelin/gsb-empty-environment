import { LogType } from './types.ts';
import type { LogRecord } from './types.ts';

/** FNV-1a 32-bit hash, used as the log-record integrity checksum. */
export function fnv1a(input: string): number {
  let hash = 0x811c9dc5;
  for (let i = 0; i < input.length; i++) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  return hash >>> 0;
}

/** Canonical serialization of a record (every field except the checksum itself). */
export function canonicalString(rec: LogRecord): string {
  switch (rec.type) {
    case LogType.Update:
      return `U|${rec.lsn}|${rec.txnId}|${rec.prevLSN}|${rec.pageId}|${rec.beforeImage}|${rec.afterImage}`;
    case LogType.Clr:
      return `C|${rec.lsn}|${rec.txnId}|${rec.prevLSN}|${rec.undoNextLSN}|${rec.pageId}|${rec.beforeImage}|${rec.afterImage}`;
    case LogType.Commit:
    case LogType.Abort:
    case LogType.End:
      return `T|${rec.type}|${rec.lsn}|${rec.txnId}|${rec.prevLSN}`;
    case LogType.BeginCheckpoint:
      return `B|${rec.lsn}`;
    case LogType.EndCheckpoint: {
      const tt = rec.txnTable.map((e) => `${e.txnId}:${e.status}:${e.lastLSN}`).join(',');
      const dpt = rec.dirtyPageTable.map((e) => `${e.pageId}:${e.recLSN}`).join(',');
      return `E|${rec.lsn}|${tt}|${dpt}`;
    }
  }
}

export function computeChecksum(rec: LogRecord): number {
  return fnv1a(canonicalString(rec));
}

export function verifyChecksum(rec: LogRecord): boolean {
  return rec.checksum === computeChecksum(rec);
}
