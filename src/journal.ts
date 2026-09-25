/**
 * Append-only JSONL journal.
 *
 * Persists, per run: configuration (budget, rule version), every oracle
 * result (cache), every accepted reduction step (evidence), the best
 * candidate after each step (checkpoint so recovery never loses it), and
 * the final status. `loadJournal` replays a journal so a run can resume
 * with a fresh budget without losing candidates or re-paying oracle calls.
 */
import { appendFileSync, existsSync, readFileSync, mkdirSync } from 'node:fs';
import { dirname } from 'node:path';
import type { Input } from './condition.ts';
import type { OracleVerdict } from './policy.ts';

export interface RunStartEvent {
  type: 'run-start';
  runId: string;
  ruleVersion: string;
  budget: number;
  at: string;
}

export interface OracleEvent {
  type: 'oracle';
  seq: number;
  key: string;
  verdict: OracleVerdict;
}

export interface StepEvent {
  type: 'step';
  seq: number;
  op: string;
  detail: string;
  before: Input;
  after: Input;
  oracleUsed: number;
  budgetLeft: number;
}

export interface BestEvent {
  type: 'best';
  seq: number;
  candidate: Input;
}

export interface FinishEvent {
  type: 'finish';
  seq: number;
  status: 'minimal' | 'local-minimum';
  reason: string;
  candidate: Input;
}

export type JournalEvent = RunStartEvent | OracleEvent | StepEvent | BestEvent | FinishEvent;

export class Journal {
  private seq = 0;
  private readonly path: string;
  constructor(path: string) {
    this.path = path;
    mkdirSync(dirname(path), { recursive: true });
  }

  emit(event: JournalEvent): void {
    appendFileSync(this.path, JSON.stringify(event) + '\n');
    this.seq = Math.max(this.seq, 'seq' in event ? event.seq : 0);
  }

  nextSeq(): number {
    return ++this.seq;
  }

  /** Restore sequence counter when resuming from an existing journal. */
  rewindTo(seq: number): void {
    this.seq = Math.max(this.seq, seq);
  }
}

export interface JournalState {
  ruleVersion?: string;
  oracleCache: Map<string, OracleVerdict>;
  oracleCalls: number;
  best?: Input;
  maxSeq: number;
}

export function loadJournal(path: string): JournalState {
  const state: JournalState = { oracleCache: new Map(), oracleCalls: 0, maxSeq: 0 };
  if (!existsSync(path)) return state;
  for (const line of readFileSync(path, 'utf8').split('\n')) {
    if (!line.trim()) continue;
    const event = JSON.parse(line) as JournalEvent;
    if ('seq' in event) state.maxSeq = Math.max(state.maxSeq, event.seq);
    switch (event.type) {
      case 'run-start':
        state.ruleVersion = event.ruleVersion;
        break;
      case 'oracle':
        state.oracleCache.set(event.key, event.verdict);
        state.oracleCalls++;
        break;
      case 'best':
        state.best = event.candidate; // recovery: latest checkpoint wins, never lost
        break;
      case 'finish':
        state.best = event.candidate;
        break;
    }
  }
  return state;
}

/** Stable canonical key for an input (attribute-order independent). */
export function canonicalKey(input: Input): string {
  return JSON.stringify(sortValue(input));
}

function sortValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(sortValue);
  if (value !== null && typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>)
      .map(([k, v]) => [k, sortValue(v)] as const)
      .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
    return Object.fromEntries(entries);
  }
  return value;
}
