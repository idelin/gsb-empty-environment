import { appendFileSync, existsSync, readFileSync } from 'node:fs';

export interface WalDataRecord {
  t: 'data';
  txn: number | null; // null = auto-committed single op
  cf: string;
  key: string;
  value: string | null; // null = delete
  seq: number;
}

export interface WalCommitRecord {
  t: 'commit';
  txn: number;
}

export type WalRecord = WalDataRecord | WalCommitRecord;

export interface RecoveredOp {
  cf: string;
  key: string;
  value: string | null;
  seq: number;
}

/**
 * Single physical write-ahead log shared by every column family.
 * Multi-CF writes are grouped under a txn id and become visible
 * on recovery only when a commit record is present.
 */
export class Wal {
  private nextTxn = 1;
  private readonly file: string;

  constructor(file: string) {
    this.file = file;
  }

  append(record: WalRecord): void {
    appendFileSync(this.file, JSON.stringify(record) + '\n');
  }

  beginTxn(): number {
    return this.nextTxn++;
  }

  /**
   * Replays the log and returns only committed operations, in log order.
   * Records of a transaction without a commit marker (crash mid-batch)
   * are discarded, keeping all column families atomically aligned.
   */
  recover(): { ops: RecoveredOp[]; maxSeq: number; maxTxn: number } {
    const ops: RecoveredOp[] = [];
    let maxSeq = 0;
    let maxTxn = 0;
    if (!existsSync(this.file)) return { ops, maxSeq, maxTxn };

    const pending = new Map<number, WalDataRecord[]>();
    for (const line of readFileSync(this.file, 'utf8').split('\n')) {
      if (!line.trim()) continue;
      const rec = JSON.parse(line) as WalRecord;
      if (rec.t === 'data') {
        maxSeq = Math.max(maxSeq, rec.seq);
        if (rec.txn === null) {
          ops.push({ cf: rec.cf, key: rec.key, value: rec.value, seq: rec.seq });
        } else {
          maxTxn = Math.max(maxTxn, rec.txn);
          const list = pending.get(rec.txn) ?? [];
          list.push(rec);
          pending.set(rec.txn, list);
        }
      } else {
        maxTxn = Math.max(maxTxn, rec.txn);
        const list = pending.get(rec.txn);
        if (list) {
          for (const d of list) {
            ops.push({ cf: d.cf, key: d.key, value: d.value, seq: d.seq });
          }
          pending.delete(rec.txn);
        }
      }
    }
    // Uncommitted leftovers in `pending` are dropped on purpose.
    return { ops, maxSeq, maxTxn };
  }

  restoreTxnCounter(maxTxn: number): void {
    this.nextTxn = maxTxn + 1;
  }
}
