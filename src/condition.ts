/**
 * Condition DSL shared by schema constraints and policy rules.
 *
 * Deliberately restricted (per requirements):
 *  - reads only explicitly present attributes of the candidate input;
 *  - atoms are integer comparisons, finite-set membership and boolean flags;
 *  - combinators are plain boolean and/or/not;
 *  - no external identity / I/O of any kind.
 *
 * Semantics for a missing attribute: the atom evaluates to `false`.
 * This keeps policy evaluation total and deterministic over partial inputs.
 */

export type CmpOp = '<' | '<=' | '>' | '>=' | '==' | '!=';

export type Condition =
  | { op: 'cmp'; attr: string; cmp: CmpOp; value: number }
  | { op: 'in'; attr: string; value: string }
  | { op: 'flag'; attr: string; is: boolean }
  | { op: 'and'; all: Condition[] }
  | { op: 'or'; any: Condition[] }
  | { op: 'not'; cond: Condition };

export type Input = Record<string, unknown>;

function cmp(a: number, op: CmpOp, b: number): boolean {
  switch (op) {
    case '<': return a < b;
    case '<=': return a <= b;
    case '>': return a > b;
    case '>=': return a >= b;
    case '==': return a === b;
    case '!=': return a !== b;
  }
}

export function evalCondition(cond: Condition, input: Input): boolean {
  switch (cond.op) {
    case 'cmp': {
      const v = input[cond.attr];
      return typeof v === 'number' && cmp(v, cond.cmp, cond.value);
    }
    case 'in': {
      const v = input[cond.attr];
      if (Array.isArray(v)) return v.includes(cond.value);
      return v === cond.value;
    }
    case 'flag': {
      const v = input[cond.attr];
      return typeof v === 'boolean' && v === cond.is;
    }
    case 'and':
      return cond.all.every((c) => evalCondition(c, input));
    case 'or':
      return cond.any.some((c) => evalCondition(c, input));
    case 'not':
      return !evalCondition(cond.cond, input);
  }
}

/** Attributes referenced by a condition (used for dependency checks). */
export function conditionAttrs(cond: Condition): string[] {
  switch (cond.op) {
    case 'cmp':
    case 'in':
    case 'flag':
      return [cond.attr];
    case 'and':
      return cond.all.flatMap(conditionAttrs);
    case 'or':
      return cond.any.flatMap(conditionAttrs);
    case 'not':
      return conditionAttrs(cond.cond);
  }
}
