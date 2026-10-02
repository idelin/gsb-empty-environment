import { createHash, type Hash } from 'node:crypto';
import type { EventRecord } from './simulator.ts';

export interface TraceEntry extends EventRecord {
  state: string;
}

export interface TraceRecorderOptions {
  stateFn?: () => string;
  store?: boolean;
}

export class TraceRecorder {
  readonly entries: TraceEntry[] = [];
  private hash: Hash = createHash('sha256');
  private stateFn: () => string;
  private store: boolean;

  constructor(options: TraceRecorderOptions | (() => string) = {}) {
    if (typeof options === 'function') {
      this.stateFn = options;
      this.store = true;
    } else {
      this.stateFn = options.stateFn ?? (() => '');
      this.store = options.store ?? true;
    }
  }

  observer(): (rec: EventRecord) => void {
    return (rec) => {
      const state = this.stateFn();
      if (this.store) this.entries.push({ ...rec, state });
      this.hash.update(`${rec.step}|${rec.time}|${rec.seq}|${rec.label}|${state}\n`);
    };
  }

  checksum(): string {
    return this.hash.copy().digest('hex');
  }
}

export interface Divergence {
  index: number;
  expected: TraceEntry | undefined;
  actual: TraceEntry | undefined;
  reason: string;
}

function entryEqual(a: TraceEntry, b: TraceEntry): boolean {
  return a.step === b.step && a.time === b.time && a.seq === b.seq && a.label === b.label && a.state === b.state;
}

export function diffTraces(expected: TraceEntry[], actual: TraceEntry[]): Divergence | null {
  const n = Math.min(expected.length, actual.length);
  for (let i = 0; i < n; i++) {
    if (!entryEqual(expected[i], actual[i])) {
      return { index: i, expected: expected[i], actual: actual[i], reason: 'entry mismatch' };
    }
  }
  if (expected.length !== actual.length) {
    return {
      index: n,
      expected: expected[n],
      actual: actual[n],
      reason: expected.length > actual.length ? 'actual trace truncated' : 'actual trace has extra events',
    };
  }
  return null;
}
