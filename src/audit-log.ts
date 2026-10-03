import fs from 'node:fs';
import { sha256Hex } from './crypto.ts';

export interface AuditEntry {
  seq: number;
  timestamp: number;
  action: string;
  details: Record<string, unknown>;
  prevHash: string;
  hash: string;
}

const GENESIS = '0'.repeat(64);

function entryHash(entry: Omit<AuditEntry, 'hash'>): string {
  return sha256Hex(JSON.stringify({
    seq: entry.seq,
    timestamp: entry.timestamp,
    action: entry.action,
    details: entry.details,
    prevHash: entry.prevHash,
  }));
}

export class AuditLog {
  private entries: AuditEntry[] = [];
  private readonly filePath: string | null;

  constructor(filePath?: string) {
    this.filePath = filePath ?? null;
  }

  append(action: string, details: Record<string, unknown>, timestamp = Date.now()): AuditEntry {
    const prevHash = this.entries.length > 0 ? this.entries[this.entries.length - 1].hash : GENESIS;
    const base = { seq: this.entries.length, timestamp, action, details, prevHash };
    const entry: AuditEntry = { ...base, hash: entryHash(base) };
    this.entries.push(entry);
    if (this.filePath) {
      fs.appendFileSync(this.filePath, JSON.stringify(entry) + '\n');
    }
    return entry;
  }

  list(): readonly AuditEntry[] {
    return this.entries;
  }

  verify(): boolean {
    let prevHash = GENESIS;
    for (let i = 0; i < this.entries.length; i++) {
      const entry = this.entries[i];
      if (entry.seq !== i || entry.prevHash !== prevHash) return false;
      const { hash, ...rest } = entry;
      if (entryHash(rest) !== hash) return false;
      prevHash = entry.hash;
    }
    return true;
  }
}
