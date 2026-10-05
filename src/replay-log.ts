import * as crypto from "crypto";
import * as fs from "fs";
import { ReplayStatus, ReplaySummary, Step } from "./types";

export type ReplayAction = "initial" | "ddmin" | "greedy" | "verify";

export interface ReplayLogEntry {
  seq: number;
  ts: string;
  action: ReplayAction;
  scriptLength: number;
  scriptHash: string;
  removedIndices?: number[];
  status: ReplayStatus;
  summary: ReplaySummary;
}

export function hashSteps(steps: Step[]): string {
  return crypto.createHash("sha1").update(JSON.stringify(steps)).digest("hex").slice(0, 12);
}

/**
 * Records a summary of every replay. Entries are kept in memory and, when
 * a file path is given, appended as JSONL so the full reduction trace is
 * preserved (including candidates that turned out invalid).
 */
export class ReplayLog {
  readonly entries: ReplayLogEntry[] = [];

  constructor(private readonly filePath?: string) {
    if (this.filePath) fs.writeFileSync(this.filePath, "");
  }

  record(entry: Omit<ReplayLogEntry, "seq" | "ts">): ReplayLogEntry {
    const full: ReplayLogEntry = {
      seq: this.entries.length + 1,
      ts: new Date().toISOString(),
      ...entry,
    };
    this.entries.push(full);
    if (this.filePath) fs.appendFileSync(this.filePath, JSON.stringify(full) + "\n");
    return full;
  }

  get count(): number {
    return this.entries.length;
  }
}
