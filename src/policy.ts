/**
 * Versioned, read-only admission policies.
 *
 * A policy is a boolean expression over *explicit* attributes supplied by the
 * caller at acquire time. The engine never calls out to any identity service;
 * everything it reads comes from the provided attribute record.
 *
 * The condition language is deliberately small:
 *   - finite-set membership ("in")
 *   - integer comparisons ("eq" | "ne" | "lt" | "lte" | "gt" | "gte")
 *   - boolean combinators ("and" | "or" | "not")
 */

export type Scalar = string | number | boolean;

export type Condition =
  | { readonly op: "in"; readonly attr: string; readonly set: readonly Scalar[] }
  | {
      readonly op: "eq" | "ne" | "lt" | "lte" | "gt" | "gte";
      readonly attr: string;
      readonly value: number;
    }
  | { readonly op: "and" | "or"; readonly conditions: readonly Condition[] }
  | { readonly op: "not"; readonly condition: Condition };

export interface Policy {
  /** Monotonically increasing version; permits must cite the current one. */
  readonly version: number;
  readonly condition: Condition;
}

export type Attributes = Readonly<Record<string, Scalar | undefined>>;

function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === "object" && !Object.isFrozen(value)) {
    for (const key of Reflect.ownKeys(value)) {
      deepFreeze((value as Record<PropertyKey, unknown>)[key]);
    }
    Object.freeze(value);
  }
  return value;
}

function validateCondition(condition: Condition, path: string): void {
  switch (condition.op) {
    case "in":
      if (!Array.isArray(condition.set) || condition.set.length === 0) {
        throw new TypeError(`${path}: "in" requires a non-empty finite set`);
      }
      for (const member of condition.set) {
        const t = typeof member;
        if (t !== "string" && t !== "number" && t !== "boolean") {
          throw new TypeError(`${path}: set members must be scalars`);
        }
      }
      return;
    case "eq":
    case "ne":
    case "lt":
    case "lte":
    case "gt":
    case "gte":
      if (!Number.isInteger(condition.value)) {
        throw new TypeError(`${path}: comparison value must be an integer`);
      }
      return;
    case "and":
    case "or":
      if (!Array.isArray(condition.conditions) || condition.conditions.length === 0) {
        throw new TypeError(`${path}: "${condition.op}" requires at least one operand`);
      }
      condition.conditions.forEach((c, i) => validateCondition(c, `${path}.${condition.op}[${i}]`));
      return;
    case "not":
      validateCondition(condition.condition, `${path}.not`);
      return;
    default:
      throw new TypeError(`${path}: unknown op ${JSON.stringify((condition as { op: unknown }).op)}`);
  }
}

/** Validate, deep-freeze, and return a policy. The result is immutable. */
export function definePolicy(policy: Policy): Readonly<Policy> {
  if (!Number.isInteger(policy.version) || policy.version < 1) {
    throw new TypeError("policy.version must be a positive integer");
  }
  validateCondition(policy.condition, "condition");
  return deepFreeze({ version: policy.version, condition: policy.condition });
}

function evaluateCondition(condition: Condition, attrs: Attributes): boolean {
  switch (condition.op) {
    case "in": {
      const actual = attrs[condition.attr];
      return condition.set.some((member) => member === actual);
    }
    case "eq":
    case "ne":
    case "lt":
    case "lte":
    case "gt":
    case "gte": {
      const actual = attrs[condition.attr];
      // Integer conditions only: non-integer (or missing) attributes never match.
      if (typeof actual !== "number" || !Number.isInteger(actual)) return false;
      switch (condition.op) {
        case "eq": return actual === condition.value;
        case "ne": return actual !== condition.value;
        case "lt": return actual < condition.value;
        case "lte": return actual <= condition.value;
        case "gt": return actual > condition.value;
        case "gte": return actual >= condition.value;
      }
      return false;
    }
    case "and":
      return condition.conditions.every((c) => evaluateCondition(c, attrs));
    case "or":
      return condition.conditions.some((c) => evaluateCondition(c, attrs));
    case "not":
      return !evaluateCondition(condition.condition, attrs);
  }
}

/** Evaluate a policy against explicitly supplied attributes. */
export function evaluatePolicy(policy: Readonly<Policy>, attrs: Attributes): boolean {
  return evaluateCondition(policy.condition, attrs);
}
