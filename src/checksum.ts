import { createHash, type Hash } from 'node:crypto';
import type { ChangeEvent } from './types.ts';

/** Canonical serialization of one row, used for checksums and wire format. */
export function serializeRow(row: ChangeEvent): string {
  return JSON.stringify([row.lsn, row.op, row.table, row.key, row.payload]);
}

export function deserializeRow(line: string): ChangeEvent {
  const [lsn, op, table, key, payload] = JSON.parse(line) as [
    number,
    ChangeEvent['op'],
    string,
    string,
    string,
  ];
  return { lsn, op, table, key, payload };
}

export function hashChunk(rows: ChangeEvent[]): string {
  const hash = createHash('sha256');
  for (const row of rows) {
    hash.update(serializeRow(row));
    hash.update('\n');
  }
  return hash.digest('hex');
}

/** Incremental transaction-level checksum: chains per-chunk checksums. */
export class TxnChecksum {
  private hash: Hash = createHash('sha256');

  addChunk(chunkChecksum: string): void {
    this.hash.update(chunkChecksum);
    this.hash.update('\n');
  }

  digest(): string {
    // copy() so the instance stays usable if needed
    return this.hash.copy().digest('hex');
  }
}
