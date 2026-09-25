import type { Attributes, PolicyExpr } from './types.ts';

export class PolicyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'PolicyError';
  }
}

function hasAttr(attrs: Attributes, attr: string): boolean {
  return Object.prototype.hasOwnProperty.call(attrs, attr);
}

function memberOf(value: unknown, set: readonly (number | string)[]): boolean {
  if (typeof value === 'string' || typeof value === 'number') {
    return set.includes(value);
  }
  if (Array.isArray(value)) {
    return value.some((item) => set.includes(item));
  }
  return false;
}

/**
 * Evaluates a policy against explicit attributes only.
 *
 * Missing attributes make every condition false (closed world). Integer
 * conditions require both the attribute and the policy value to be integers;
 * a non-integer policy value is a modelling error and throws `PolicyError`.
 */
export function evaluatePolicy(expr: PolicyExpr, attrs: Attributes): boolean {
  switch (expr.op) {
    case 'has':
      return hasAttr(attrs, expr.attr) && Boolean(attrs[expr.attr]);
    case 'eq':
      return hasAttr(attrs, expr.attr) && attrs[expr.attr] === expr.value;
    case 'neq':
      return hasAttr(attrs, expr.attr) && attrs[expr.attr] !== expr.value;
    case 'lt':
    case 'lte':
    case 'gt':
    case 'gte': {
      if (!Number.isInteger(expr.value)) {
        throw new PolicyError(`policy "${expr.op}" requires an integer value, got ${expr.value}`);
      }
      const value = attrs[expr.attr];
      if (typeof value !== 'number' || !Number.isInteger(value)) return false;
      switch (expr.op) {
        case 'lt': return value < expr.value;
        case 'lte': return value <= expr.value;
        case 'gt': return value > expr.value;
        case 'gte': return value >= expr.value;
      }
      break;
    }
    case 'in':
      return hasAttr(attrs, expr.attr) && memberOf(attrs[expr.attr], expr.set);
    case 'notIn':
      return hasAttr(attrs, expr.attr) && !memberOf(attrs[expr.attr], expr.set);
    case 'and':
      return expr.args.every((arg) => evaluatePolicy(arg, attrs));
    case 'or':
      return expr.args.some((arg) => evaluatePolicy(arg, attrs));
    case 'not':
      return !evaluatePolicy(expr.arg, attrs);
  }
}
