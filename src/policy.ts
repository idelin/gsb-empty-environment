export type AttributeValue = string | number | boolean;
export type Attributes = Readonly<Record<string, AttributeValue>>;

export type Condition =
  | { readonly op: 'in'; readonly attr: string; readonly set: readonly AttributeValue[] }
  | { readonly op: 'eq' | 'lt' | 'lte' | 'gt' | 'gte'; readonly attr: string; readonly value: number }
  | { readonly op: 'and'; readonly all: readonly Condition[] }
  | { readonly op: 'or'; readonly any: readonly Condition[] }
  | { readonly op: 'not'; readonly cond: Condition };

export interface Policy {
  readonly id: string;
  readonly condition: Condition;
}

export class PolicyError extends Error {}

function evalCondition(cond: Condition, attrs: Attributes): boolean {
  switch (cond.op) {
    case 'in': {
      if (!Array.isArray(cond.set)) {
        throw new PolicyError(`'in' condition on "${cond.attr}" requires a finite set`);
      }
      const value = attrs[cond.attr];
      return value !== undefined && cond.set.includes(value);
    }
    case 'eq':
    case 'lt':
    case 'lte':
    case 'gt':
    case 'gte': {
      if (!Number.isInteger(cond.value)) {
        throw new PolicyError(`integer condition on "${cond.attr}" requires an integer literal`);
      }
      const value = attrs[cond.attr];
      if (typeof value !== 'number' || !Number.isInteger(value)) return false;
      switch (cond.op) {
        case 'eq': return value === cond.value;
        case 'lt': return value < cond.value;
        case 'lte': return value <= cond.value;
        case 'gt': return value > cond.value;
        case 'gte': return value >= cond.value;
      }
      break;
    }
    case 'and':
      return cond.all.every((c) => evalCondition(c, attrs));
    case 'or':
      return cond.any.some((c) => evalCondition(c, attrs));
    case 'not':
      return !evalCondition(cond.cond, attrs);
  }
}

export function evaluate(policy: Policy, attrs: Attributes): boolean {
  return evalCondition(policy.condition, attrs);
}
