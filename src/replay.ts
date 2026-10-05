import { applyOp } from './ops.js';
import { varKey } from './slicer.js';
import type { ReplayReport, ReplayVerdict, SliceResult, Value } from './types.js';

export function replaySlice(slice: SliceResult): ReplayReport {
  const env: Map<string, Value> = new Map();
  const unknownVars: Set<string> = new Set();
  const unknownEvents: number[] = [];
  const gapVars: Set<string> = new Set();
  for (const gap of slice.gaps) {
    if (gap.variable !== null) {
      gapVars.add(varKey(gap.variable));
    }
  }

  let expected: Value = null;
  let actual: Value | null = null;
  let assertionUnknown = false;

  for (const ev of slice.events) {
    const args: Value[] = [];
    let unknown = false;
    for (const read of ev.reads) {
      const key = varKey(read);
      if (gapVars.has(key) || unknownVars.has(key)) {
        unknown = true;
        break;
      }
      const v = env.get(key);
      if (v === undefined) {
        unknown = true;
        break;
      }
      args.push(v);
    }

    if (!unknown) {
      let computed: Value = ev.value;
      if (ev.kind === 'const') {
        computed = ev.value;
      } else if (ev.op !== null) {
        try {
          computed = applyOp(ev.op, args);
        } catch {
          unknown = true;
        }
      } else {
        unknown = true;
      }
      if (!unknown) {
        if (ev.writes !== null) {
          env.set(varKey(ev.writes), computed);
        }
        if (ev.kind === 'assert' && ev.id === slice.criterion) {
          actual = computed;
          expected = ev.value;
        }
      }
    }

    if (unknown) {
      unknownEvents.push(ev.id);
      if (ev.writes !== null) {
        unknownVars.add(varKey(ev.writes));
      }
      if (ev.kind === 'assert' && ev.id === slice.criterion) {
        assertionUnknown = true;
        expected = ev.value;
      }
    }
  }

  let verdict: ReplayVerdict = 'indeterminate';
  if (assertionUnknown) {
    verdict = 'indeterminate';
  } else if (actual === expected) {
    verdict = 'match';
  } else {
    verdict = 'mismatch';
  }
  return { verdict, assertionId: slice.criterion, expected, actual, unknownEvents };
}
