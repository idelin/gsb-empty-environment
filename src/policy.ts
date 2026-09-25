/**
 * Pure policy engine. Rules read only explicit, read-only properties and are
 * limited to finite-set membership, integer comparisons and boolean algebra.
 * No external identity service is consulted: evaluation is a pure function of
 * the property bag passed in.
 */

export type IntOp = 'eq' | 'ne' | 'lt' | 'le' | 'gt' | 'ge';

export type Condition =
  | { readonly op: 'in'; readonly prop: string; readonly set: readonly (string | number | boolean)[] }
  | { readonly op: IntOp; readonly prop: string; readonly value: number }
  | { readonly op: 'and' | 'or'; readonly conditions: readonly Condition[] }
  | { readonly op: 'not'; readonly condition: Condition };

export type PolicyErrorCode =
  | 'INVALID_CONDITION'
  | 'UNKNOWN_PROPERTY'
  | 'RULE_NOT_FOUND'
  | 'DUPLICATE_RULE'
  | 'INVALID_PROPS';

export class PolicyError extends Error {
  readonly code: PolicyErrorCode;
  constructor(code: PolicyErrorCode, message: string) {
    super(message);
    this.name = 'PolicyError';
    this.code = code;
  }
}

export interface Rule {
  readonly name: string;
  readonly when: Condition;
  readonly effect: 'allow' | 'deny';
}

const INT_OPS = new Set(['eq', 'ne', 'lt', 'le', 'gt', 'ge']);

export function validateCondition(cond: Condition, path = 'condition'): void {
  switch (cond.op) {
    case 'in': {
      if (!Array.isArray(cond.set)) {
        throw new PolicyError('INVALID_CONDITION', `${path}: 'in' requires a finite array set`);
      }
      for (const item of cond.set) {
        const t = typeof item;
        if (!((t === 'number' && Number.isFinite(item as number)) || t === 'string' || t === 'boolean')) {
          throw new PolicyError('INVALID_CONDITION', `${path}: set members must be finite strings/numbers/booleans`);
        }
      }
      return;
    }
    case 'eq':
    case 'ne':
    case 'lt':
    case 'le':
    case 'gt':
    case 'ge': {
      if (!Number.isInteger(cond.value)) {
        throw new PolicyError('INVALID_CONDITION', `${path}: '${cond.op}' requires an integer operand`);
      }
      return;
    }
    case 'and':
    case 'or': {
      if (!Array.isArray(cond.conditions) || cond.conditions.length === 0) {
        throw new PolicyError('INVALID_CONDITION', `${path}: '${cond.op}' requires a non-empty condition list`);
      }
      cond.conditions.forEach((c, i) => validateCondition(c, `${path}.${cond.op}[${i}]`));
      return;
    }
    case 'not': {
      validateCondition(cond.condition, `${path}.not`);
      return;
    }
    default:
      throw new PolicyError('INVALID_CONDITION', `${path}: unknown operator`);
  }
}

function readProp(props: Readonly<Record<string, unknown>>, prop: string): unknown {
  if (!Object.prototype.hasOwnProperty.call(props, prop)) {
    throw new PolicyError('UNKNOWN_PROPERTY', `property '${prop}' is not declared in the evaluation context`);
  }
  return props[prop];
}

function evalCondition(cond: Condition, props: Readonly<Record<string, unknown>>): boolean {
  switch (cond.op) {
    case 'in':
      return cond.set.includes(readProp(props, cond.prop) as string | number | boolean);
    case 'eq':
    case 'ne':
    case 'lt':
    case 'le':
    case 'gt':
    case 'ge': {
      const actual = readProp(props, cond.prop);
      if (typeof actual !== 'number' || !Number.isInteger(actual)) {
        throw new PolicyError('INVALID_CONDITION', `property '${cond.prop}' must be an integer`);
      }
      switch (cond.op) {
        case 'eq': return actual === cond.value;
        case 'ne': return actual !== cond.value;
        case 'lt': return actual < cond.value;
        case 'le': return actual <= cond.value;
        case 'gt': return actual > cond.value;
        case 'ge': return actual >= cond.value;
      }
      return false;
    }
    case 'and':
      return cond.conditions.every((c) => evalCondition(c, props));
    case 'or':
      return cond.conditions.some((c) => evalCondition(c, props));
    case 'not':
      return !evalCondition(cond.condition, props);
  }
}

export class PolicyEngine {
  /** Rules are keyed per module: 'module:rule'. Same-named rules in different
   *  modules never override each other. */
  private readonly rules = new Map<string, Rule>();

  register(moduleName: string, rule: Rule): void {
    validateCondition(rule.when);
    const key = `${moduleName}:${rule.name}`;
    if (this.rules.has(key)) {
      throw new PolicyError('DUPLICATE_RULE', `rule '${key}' is already registered`);
    }
    this.rules.set(key, rule);
  }

  has(moduleName: string, ruleName: string): boolean {
    return this.rules.has(`${moduleName}:${ruleName}`);
  }

  /** Default-deny: a rule whose condition does not hold evaluates to 'deny'. */
  evaluate(
    moduleName: string,
    ruleName: string,
    props: Readonly<Record<string, unknown>>,
  ): 'allow' | 'deny' {
    const key = `${moduleName}:${ruleName}`;
    const rule = this.rules.get(key);
    if (!rule) throw new PolicyError('RULE_NOT_FOUND', `rule '${key}' is not registered`);
    if (props === null || typeof props !== 'object' || Array.isArray(props)) {
      throw new PolicyError('INVALID_PROPS', 'evaluation context must be a plain property bag');
    }
    const frozen: Readonly<Record<string, unknown>> = Object.freeze({ ...props });
    return evalCondition(rule.when, frozen) ? rule.effect : 'deny';
  }
}
