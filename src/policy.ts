import type { Attributes, Condition } from './types.ts';

export class PolicyError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'PolicyError';
  }
}

/**
 * Structural validation, done once when a policy is attached to a token:
 * integer conditions must carry real integers, sets must be finite and
 * non-empty, boolean nodes must have children.
 */
export function validateCondition(condition: Condition): void {
  switch (condition.op) {
    case 'in':
      if (!Array.isArray(condition.set) || condition.set.length === 0) {
        throw new PolicyError(`"in" on "${condition.attr}" requires a non-empty finite set`);
      }
      for (const item of condition.set) {
        if (typeof item !== 'string' && !Number.isInteger(item)) {
          throw new PolicyError(`"in" set members must be strings or integers, got ${String(item)}`);
        }
      }
      return;
    case 'eq':
    case 'lt':
    case 'lte':
    case 'gt':
    case 'gte':
      if (!Number.isInteger(condition.value)) {
        throw new PolicyError(`"${condition.op}" on "${condition.attr}" requires an integer, got ${condition.value}`);
      }
      return;
    case 'and':
    case 'or':
      if (condition.conditions.length === 0) {
        throw new PolicyError(`"${condition.op}" requires at least one sub-condition`);
      }
      condition.conditions.forEach(validateCondition);
      return;
    case 'not':
      validateCondition(condition.condition);
      return;
  }
}

export interface EvalOutcome {
  readonly ok: boolean;
  /** Names of the explicit attributes that were read, deduplicated, sorted. */
  readonly attributesRead: readonly string[];
}

/**
 * Pure evaluation against the explicit attribute bag supplied by the caller.
 * A missing or wrongly-typed attribute makes the atomic condition false;
 * it never triggers a lookup anywhere else.
 */
export function evaluate(condition: Condition, attributes: Attributes): EvalOutcome {
  const read = new Set<string>();
  const ok = walk(condition, attributes, read);
  return { ok, attributesRead: [...read].sort() };
}

function walk(condition: Condition, attributes: Attributes, read: Set<string>): boolean {
  switch (condition.op) {
    case 'in': {
      read.add(condition.attr);
      const value = attributes[condition.attr];
      return value !== undefined && condition.set.some((item) => item === value);
    }
    case 'eq':
    case 'lt':
    case 'lte':
    case 'gt':
    case 'gte': {
      read.add(condition.attr);
      const value = attributes[condition.attr];
      if (typeof value !== 'number' || !Number.isInteger(value)) {
        return false;
      }
      switch (condition.op) {
        case 'eq': return value === condition.value;
        case 'lt': return value < condition.value;
        case 'lte': return value <= condition.value;
        case 'gt': return value > condition.value;
        case 'gte': return value >= condition.value;
      }
      return false;
    }
    case 'and':
      return condition.conditions.every((sub) => walk(sub, attributes, read));
    case 'or':
      return condition.conditions.some((sub) => walk(sub, attributes, read));
    case 'not':
      return !walk(condition.condition, attributes, read);
  }
}
