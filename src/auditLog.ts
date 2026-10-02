import type { AuditRecord } from './types.ts';

export type AuditSink = (record: AuditRecord) => void;

export class AuditLog {
  private records: AuditRecord[] = [];
  private seq = 0;
  private sinks: AuditSink[] = [];

  addSink(sink: AuditSink): void {
    this.sinks.push(sink);
  }

  append(record: Omit<AuditRecord, 'seq'>): AuditRecord {
    const full: AuditRecord = { ...record, seq: this.seq };
    this.seq += 1;
    this.records.push(full);
    for (const sink of this.sinks) {
      sink(full);
    }
    return full;
  }

  all(): AuditRecord[] {
    return [...this.records];
  }

  byVersion(version: string): AuditRecord[] {
    return this.records.filter((r) => r.ruleVersion === version);
  }
}
