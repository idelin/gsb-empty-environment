// Plain point evaluation of conditions — used by the independent exhaustive
// enumeration cross-check and by the demo. Deliberately separate from the
// cube/DNF machinery so the verification is a genuine second implementation.

import type { Cond, Point, Rule } from './types.ts';

export function evalCond(cond: Cond, point: Point): boolean {
  switch (cond.op) {
    case 'const':
      return cond.value;
    case 'in':
      return cond.values.includes(point[cond.attr] as string);
    case 'cmp': {
      const v = point[cond.attr];
      if (typeof v !== 'number') return false;
      switch (cond.cmp) {
        case '<': return v < cond.value;
        case '<=': return v <= cond.value;
        case '==': return v === cond.value;
        case '>=': return v >= cond.value;
        case '>': return v > cond.value;
        case '!=': return v !== cond.value;
      }
      return false;
    }
    case 'and':
      return cond.args.every((a) => evalCond(a, point));
    case 'or':
      return cond.args.some((a) => evalCond(a, point));
    case 'not':
      return !evalCond(cond.arg, point);
  }
}

export function matchingEffects(rules: readonly Rule[], point: Point): Set<string> {
  const effects = new Set<string>();
  for (const rule of rules) {
    if (evalCond(rule.when, point)) effects.add(rule.effect);
  }
  return effects;
}
