import type { Tri } from './types.ts';

/** Kleene (strong) three-valued logic over {true, false, unknown}. */

export function triNot(v: Tri): Tri {
  if (v === 'true') return 'false';
  if (v === 'false') return 'true';
  return 'unknown';
}

export function triAnd(values: readonly Tri[]): Tri {
  let sawUnknown = false;
  for (const v of values) {
    if (v === 'false') return 'false';
    if (v === 'unknown') sawUnknown = true;
  }
  return sawUnknown ? 'unknown' : 'true';
}

export function triOr(values: readonly Tri[]): Tri {
  let sawUnknown = false;
  for (const v of values) {
    if (v === 'true') return 'true';
    if (v === 'unknown') sawUnknown = true;
  }
  return sawUnknown ? 'unknown' : 'false';
}
