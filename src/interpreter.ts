import { StateStore, type JsonValue, type Patch } from './state.ts';
import type { LogRecord } from './log.ts';

export interface InterpretResult {
  state: StateStore;
  digest: string;
  /** effectIds confirmed by an ack record in the log. */
  confirmedEffects: string[];
}

/**
 * Independent full-log interpreter: a deliberately separate, minimal code
 * path that replays a verified log from scratch. Used as the ground-truth
 * cross-check for the incremental recovery engine.
 *
 * Rules mirrored from the spec:
 *  - only complete (committed) transactions mutate state;
 *  - duplicate opIds are applied at most once;
 *  - side effects are never executed here — replay only observes acks.
 */
export function interpretFullLog(records: LogRecord[]): InterpretResult {
  const state = new StateStore();
  const seen = new Set<string>();
  const confirmed = new Set<string>();
  let txId: string | null = null;
  let buffer: Patch[] = [];

  for (const rec of records) {
    switch (rec.type) {
      case 'op': {
        if (seen.has(rec.opId)) break; // duplicate opId: never replayed twice
        seen.add(rec.opId);
        const patch = rec.payload.patch as Patch;
        if (txId === null) state.apply(patch);
        else buffer.push(patch);
        break;
      }
      case 'tx_begin':
        txId = rec.payload.txId as string;
        buffer = [];
        break;
      case 'tx_commit':
        for (const p of buffer) state.apply(p);
        txId = null;
        buffer = [];
        break;
      case 'tx_abort':
        txId = null;
        buffer = [];
        break;
      case 'effect_ack':
        confirmed.add(rec.payload.effectId as string);
        break;
      default:
        break; // branch/switch/effect/checkpoint carry no committed state
    }
  }
  // An uncommitted transaction at end of log is simply discarded.

  return { state, digest: state.digest(), confirmedEffects: [...confirmed].sort() };
}
