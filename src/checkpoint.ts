import { appendFileSync, existsSync, readFileSync, writeFileSync } from 'node:fs';
import type { JsonObject } from './json.ts';

/**
 * - `emitted`: record transformed and published (isolated mode).
 * - `failed`:  record rejected (isolated mode); counted once, never retried
 *              by the same store, so recovery cannot double-count it.
 * - `staged`:  record transformed inside an atomic batch, awaiting commit.
 */
export type RecordStatus = 'emitted' | 'failed' | 'staged';

export interface CheckpointCounts {
  emitted: number;
  failed: number;
  staged: number;
}

export interface CheckpointStore {
  /** Status previously recorded for this id, or undefined if never seen. */
  get(id: string): RecordStatus | undefined;
  /**
   * Record a status. First write wins: re-marking an existing id is a no-op
   * and returns false, which is what makes restarts idempotent.
   */
  mark(id: string, status: RecordStatus, output?: JsonObject): boolean;
  /** Output stored alongside a `staged` mark (atomic mode resume). */
  getOutput(id: string): JsonObject | undefined;
  /** Whether the atomic batch was already committed (published). */
  isCommitted(): boolean;
  /** Permanently flag the atomic batch as published. */
  commit(): void;
  /** Live counts derived from the log; safe to read after a reload. */
  counts(): CheckpointCounts;
  flush(): void;
}

export class MemoryCheckpointStore implements CheckpointStore {
  protected records = new Map<string, { status: RecordStatus; output?: JsonObject }>();
  protected committed = false;

  get(id: string): RecordStatus | undefined {
    return this.records.get(id)?.status;
  }

  mark(id: string, status: RecordStatus, output?: JsonObject): boolean {
    if (this.records.has(id)) return false;
    this.records.set(id, output === undefined ? { status } : { status, output });
    return true;
  }

  getOutput(id: string): JsonObject | undefined {
    return this.records.get(id)?.output;
  }

  isCommitted(): boolean {
    return this.committed;
  }

  commit(): void {
    this.committed = true;
  }

  counts(): CheckpointCounts {
    let emitted = 0;
    let failed = 0;
    let staged = 0;
    for (const { status } of this.records.values()) {
      if (status === 'emitted') emitted += 1;
      else if (status === 'failed') failed += 1;
      else staged += 1;
    }
    return { emitted, failed, staged };
  }

  flush(): void {}
}

type LogLine =
  | { type: 'record'; id: string; status: RecordStatus; output?: JsonObject }
  | { type: 'commit' };

/**
 * Append-only JSONL checkpoint. Every mark/commit is flushed to disk before
 * the runner moves on, so a crash never loses progress and a restart replays
 * the log instead of re-emitting or re-counting records.
 */
export class FileCheckpointStore extends MemoryCheckpointStore {
  readonly filePath: string;

  constructor(filePath: string) {
    super();
    this.filePath = filePath;
    if (existsSync(filePath)) {
      const lines = readFileSync(filePath, 'utf8').split('\n');
      for (const line of lines) {
        if (!line.trim()) continue;
        const entry = JSON.parse(line) as LogLine;
        if (entry.type === 'commit') this.committed = true;
        else if (!this.records.has(entry.id)) {
          this.records.set(
            entry.id,
            entry.output === undefined
              ? { status: entry.status }
              : { status: entry.status, output: entry.output },
          );
        }
      }
    } else {
      writeFileSync(filePath, '', 'utf8');
    }
  }

  private append(entry: LogLine): void {
    appendFileSync(this.filePath, `${JSON.stringify(entry)}\n`, 'utf8');
  }

  override mark(id: string, status: RecordStatus, output?: JsonObject): boolean {
    if (!super.mark(id, status, output)) return false;
    const entry: LogLine =
      output === undefined
        ? { type: 'record', id, status }
        : { type: 'record', id, status, output };
    this.append(entry);
    return true;
  }

  override commit(): void {
    if (this.committed) return;
    super.commit();
    this.append({ type: 'commit' });
  }
}
