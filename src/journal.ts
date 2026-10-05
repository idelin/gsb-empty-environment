// Append-only JSONL journal with fsync durability and crash-injection hooks.
// This is the "single table" of record: intents and resolutions for every
// transaction, plus decided records (map/quota/capacity/tick/reassign).

import * as fs from "node:fs";
import type { JournalRecord } from "./types.ts";

export class CrashError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "CrashError";
  }
}

export class Journal {
  readonly path: string;
  private fd: number;
  private failNextType: JournalRecord["type"] | null = null;
  private dead = false;

  constructor(path: string) {
    this.path = path;
    this.fd = fs.openSync(path, "a+");
  }

  // Test hook: the next append of this record type simulates a process crash
  // (the record is NOT written and the journal becomes unusable).
  failNext(type: JournalRecord["type"]): void {
    this.failNextType = type;
  }

  clearFault(): void {
    this.failNextType = null;
  }

  append(rec: JournalRecord): void {
    if (this.dead) throw new CrashError("journal is dead (process crashed)");
    if (this.failNextType === rec.type) {
      this.failNextType = null;
      this.dead = true;
      throw new CrashError(`injected crash before writing ${rec.type}`);
    }
    fs.writeSync(this.fd, JSON.stringify(rec) + "\n");
    fs.fsyncSync(this.fd);
  }

  close(): void {
    try {
      fs.closeSync(this.fd);
    } catch {
      // already closed
    }
  }

  static read(path: string): JournalRecord[] {
    if (!fs.existsSync(path)) return [];
    const out: JournalRecord[] = [];
    for (const line of fs.readFileSync(path, "utf8").split("\n")) {
      const trimmed = line.trim();
      if (trimmed.length > 0) out.push(JSON.parse(trimmed) as JournalRecord);
    }
    return out;
  }
}
