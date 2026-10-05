import { appendFileSync, writeFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import type { Script } from './model.ts';

export type ReplayKind = 'initial' | 'candidate' | 'verify' | 'final';

export interface ReplaySummary {
  seq: number;
  timestamp: string;
  kind: ReplayKind;
  scriptHash: string;
  stepCount: number;
  removedStepId: string | null;
  valid: boolean;
  validationErrors: string[];
  outcome: 'failed' | 'passed' | 'not-replayed';
  assertion: string | null;
  failedAtStepId: string | null;
  accepted: boolean;
}

export function hashScript(script: Script): string {
  return createHash('sha256').update(JSON.stringify(script)).digest('hex').slice(0, 16);
}

/**
 * Records one summary per replay (or rejected candidate) as a JSONL stream,
 * appended to disk immediately so no replay is lost if the run aborts.
 */
export class ReplayLog {
  private seq = 0;
  private readonly summaries: ReplaySummary[] = [];
  private readonly filePath?: string;

  constructor(filePath?: string) {
    this.filePath = filePath;
    if (filePath) writeFileSync(filePath, '');
  }

  record(entry: Omit<ReplaySummary, 'seq' | 'timestamp'>): ReplaySummary {
    const summary: ReplaySummary = {
      ...entry,
      seq: this.seq += 1,
      timestamp: new Date().toISOString(),
    };
    this.summaries.push(summary);
    if (this.filePath) appendFileSync(this.filePath, `${JSON.stringify(summary)}\n`);
    return summary;
  }

  get entries(): readonly ReplaySummary[] {
    return this.summaries;
  }
}
