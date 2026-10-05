import type { LogEvent, Value, VarVersion } from './model.ts';
import { evalExpr, MissingProducerError } from './expr.ts';

export interface ValueMismatch {
  eventId: number;
  recorded: Value;
  replayed: Value;
}

export interface ReplayGap {
  eventId: number;
  missing: VarVersion;
}

export interface AssertionCheck {
  eventId: number;
  recorded: Value;
  replayed: Value | undefined;
  match: boolean;
}

export interface ReplayResult {
  mismatches: ValueMismatch[];
  gaps: ReplayGap[];
  assertion: AssertionCheck | undefined;
}

export function replaySlice(events: LogEvent[], criterionId: number): ReplayResult {
  const env = new Map<string, Value>();
  const mismatches: ValueMismatch[] = [];
  const gaps: ReplayGap[] = [];
  let assertion: AssertionCheck | undefined;

  const sorted = [...events].sort((a, b) => a.id - b.id);
  for (const event of sorted) {
    let replayed: Value | undefined;
    try {
      replayed = evalExpr(event.expr, env);
    } catch (err) {
      if (err instanceof MissingProducerError) {
        gaps.push({ eventId: event.id, missing: err.missing });
        continue;
      }
      throw err;
    }
    if (event.kind === 'write') {
      env.set(`${event.target.name}@${event.target.version}`, replayed);
    }
    if (replayed !== event.value) {
      mismatches.push({ eventId: event.id, recorded: event.value, replayed });
    }
    if (event.kind === 'assert' && event.id === criterionId) {
      assertion = {
        eventId: event.id,
        recorded: event.value,
        replayed,
        match: replayed === event.value,
      };
    }
  }

  return { mismatches, gaps, assertion };
}
