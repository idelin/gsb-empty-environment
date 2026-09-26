// Cross-validation: replay an executor trace and verify, step by step, that the
// checker's independent one-step semantics lands on the identical state hash.

import { step } from './checker.ts';
import { stateHash, type Event, type Spec } from './spec.ts';
import type { TraceEntry } from './executor.ts';

export interface CrossCheckMismatch {
  readonly seq: number;
  readonly event: Event;
  readonly executorHash: string;
  readonly checkerHash: string | null;
  readonly reason?: string;
}

export interface CrossCheckReport {
  readonly ok: boolean;
  readonly steps: number;
  readonly mismatches: readonly CrossCheckMismatch[];
}

export function crossValidate<S>(
  spec: Spec<S>,
  trace: readonly TraceEntry[],
): CrossCheckReport {
  const mismatches: CrossCheckMismatch[] = [];
  let state = spec.initial();
  for (const entry of trace) {
    const result = step(spec, state, entry.event);
    if (!result.ok) {
      mismatches.push({
        seq: entry.seq,
        event: entry.event,
        executorHash: entry.stateHash,
        checkerHash: null,
        reason: result.reason,
      });
      continue;
    }
    const checkerHash = stateHash(result.state);
    if (checkerHash !== entry.stateHash) {
      mismatches.push({
        seq: entry.seq,
        event: entry.event,
        executorHash: entry.stateHash,
        checkerHash,
      });
    }
    state = result.state;
  }
  return { ok: mismatches.length === 0, steps: trace.length, mismatches };
}
