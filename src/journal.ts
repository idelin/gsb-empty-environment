import { readFileSync, writeFileSync, unlinkSync, existsSync, renameSync } from "node:fs";
import type { JsonValue } from "./json.ts";

/**
 * Checkpoint journal: records the hash of the ORIGINAL input, the ordered
 * names of completed steps, and the latest intermediate state. Persisted
 * atomically after every step so a crashed migration can resume.
 */
export interface Journal {
  readonly from: string;
  readonly to: string;
  /** sha256 of the canonicalized original input document. */
  readonly inputHash: string;
  /** Names of steps already applied, in execution order. */
  readonly completedSteps: string[];
  /** Document state after the last completed step. */
  readonly state: JsonValue;
}

export interface JournalStore {
  load(): Journal | null;
  save(journal: Journal): void;
  clear(): void;
}

/** File-backed store (atomic via write+rename). */
export class FileJournalStore implements JournalStore {
  private readonly file: string;

  constructor(file: string) {
    this.file = file;
  }

  load(): Journal | null {
    if (!existsSync(this.file)) return null;
    return JSON.parse(readFileSync(this.file, "utf8")) as Journal;
  }

  save(journal: Journal): void {
    const tmp = `${this.file}.tmp`;
    writeFileSync(tmp, JSON.stringify(journal, null, 2));
    renameSync(tmp, this.file);
  }

  clear(): void {
    if (existsSync(this.file)) unlinkSync(this.file);
  }
}

/** In-memory store, handy for tests. */
export class MemoryJournalStore implements JournalStore {
  private journal: Journal | null = null;

  load(): Journal | null {
    return this.journal ? (JSON.parse(JSON.stringify(this.journal)) as Journal) : null;
  }

  save(journal: Journal): void {
    this.journal = JSON.parse(JSON.stringify(journal)) as Journal;
  }

  clear(): void {
    this.journal = null;
  }
}
