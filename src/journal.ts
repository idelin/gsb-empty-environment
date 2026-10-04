import type { PublishReceipt, PublishRequest } from './types.ts';

export interface JournalEntry {
  request: PublishRequest;
  receipt: PublishReceipt;
}

/**
 * Durable record of committed publishes. Written before the caller's promise
 * is resolved, so a recovered queue can replay idempotent results.
 */
export interface Journal {
  recordCommitted(entry: JournalEntry): void | Promise<void>;
  entries(): JournalEntry[] | Promise<JournalEntry[]>;
}

export class MemoryJournal implements Journal {
  private readonly committed: JournalEntry[] = [];

  recordCommitted(entry: JournalEntry): void {
    this.committed.push({ request: { ...entry.request }, receipt: { ...entry.receipt } });
  }

  entries(): JournalEntry[] {
    return this.committed.map((e) => ({ request: { ...e.request }, receipt: { ...e.receipt } }));
  }
}
