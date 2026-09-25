/**
 * Policy AST. Policies may only reference explicitly named attributes,
 * finite integer sets, integer comparisons and boolean combinators.
 * No external identity service is consulted anywhere.
 */

export type IntOp = 'eq' | 'neq' | 'lt' | 'lte' | 'gt' | 'gte';

export type Policy =
  | { kind: 'cmp'; attr: string; op: IntOp; value: number }
  | { kind: 'in'; attr: string; set: readonly number[] }
  | { kind: 'and'; children: readonly Policy[] }
  | { kind: 'or'; children: readonly Policy[] }
  | { kind: 'not'; child: Policy };

export const cmp = (attr: string, op: IntOp, value: number): Policy => ({ kind: 'cmp', attr, op, value });
export const eq = (attr: string, value: number): Policy => cmp(attr, 'eq', value);
export const gt = (attr: string, value: number): Policy => cmp(attr, 'gt', value);
export const oneOf = (attr: string, set: readonly number[]): Policy => ({ kind: 'in', attr, set });
export const and = (...children: readonly Policy[]): Policy => ({ kind: 'and', children });
export const or = (...children: readonly Policy[]): Policy => ({ kind: 'or', children });
export const not = (child: Policy): Policy => ({ kind: 'not', child });

export function attributesOf(p: Policy): Set<string> {
  switch (p.kind) {
    case 'cmp':
    case 'in':
      return new Set([p.attr]);
    case 'not':
      return attributesOf(p.child);
    case 'and':
    case 'or': {
      const out = new Set<string>();
      for (const c of p.children) for (const a of attributesOf(c)) out.add(a);
      return out;
    }
  }
}

export function applyCond(p: Extract<Policy, { kind: 'cmp' | 'in' }>, value: number): boolean {
  if (p.kind === 'in') return p.set.includes(value);
  switch (p.op) {
    case 'eq': return value === p.value;
    case 'neq': return value !== p.value;
    case 'lt': return value < p.value;
    case 'lte': return value <= p.value;
    case 'gt': return value > p.value;
    case 'gte': return value >= p.value;
  }
}
