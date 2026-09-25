import type { AttributeBag } from './types.ts';

export type Condition =
  | { op: 'in'; attr: string; values: readonly (string | number | boolean)[] }
  | { op: 'eq' | 'lt' | 'lte' | 'gt' | 'gte'; attr: string; value: number }
  | { op: 'and' | 'or'; args: readonly Condition[] }
  | { op: 'not'; arg: Condition };

export class MissingAttributeError extends Error {
  readonly attr: string;

  constructor(attr: string) {
    super(`missing attribute: ${attr}`);
    this.name = 'MissingAttributeError';
    this.attr = attr;
  }
}

export function evaluateCondition(cond: Condition, attrs: AttributeBag): boolean {
  switch (cond.op) {
    case 'in': {
      const v = readAttr(attrs, cond.attr);
      return cond.values.includes(v);
    }
    case 'eq':
    case 'lt':
    case 'lte':
    case 'gt':
    case 'gte': {
      const v = readAttr(attrs, cond.attr);
      if (typeof v !== 'number' || !Number.isInteger(v)) {
        throw new MissingAttributeError(cond.attr);
      }
      switch (cond.op) {
        case 'eq': return v === cond.value;
        case 'lt': return v < cond.value;
        case 'lte': return v <= cond.value;
        case 'gt': return v > cond.value;
        case 'gte': return v >= cond.value;
      }
      break;
    }
    case 'and':
      return cond.args.every((c) => evaluateCondition(c, attrs));
    case 'or':
      return cond.args.some((c) => evaluateCondition(c, attrs));
    case 'not':
      return !evaluateCondition(cond.arg, attrs);
  }
}

function readAttr(attrs: AttributeBag, attr: string) {
  if (!(attr in attrs)) throw new MissingAttributeError(attr);
  return attrs[attr];
}
