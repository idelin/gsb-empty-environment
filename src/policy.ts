import { InvalidPolicyError } from './errors.ts';

export type AttributeValue = string | number | boolean;
export type AttributeSet = Readonly<Record<string, AttributeValue>>;

export type IntCmp = 'eq' | 'lt' | 'lte' | 'gt' | 'gte';

export type Rule =
  | { readonly kind: 'in'; readonly attr: string; readonly values: readonly AttributeValue[] }
  | { readonly kind: 'int'; readonly attr: string; readonly cmp: IntCmp; readonly value: number }
  | { readonly kind: 'and'; readonly rules: readonly Rule[] }
  | { readonly kind: 'or'; readonly rules: readonly Rule[] }
  | { readonly kind: 'not'; readonly rule: Rule };

export interface Policy {
  readonly version: number;
  readonly name: string;
  readonly rule: Rule;
}

const MAX_DEPTH = 16;
const MAX_SET_SIZE = 1024;
const INT_CMPS: readonly IntCmp[] = ['eq', 'lt', 'lte', 'gt', 'gte'];

function isAttributeValue(value: unknown): value is AttributeValue {
  const t = typeof value;
  if (t === 'string' || t === 'boolean') return true;
  if (t === 'number') return Number.isFinite(value as number);
  return false;
}

function fail(path: string, reason: string): never {
  throw new InvalidPolicyError(`${path}: ${reason}`);
}

export function validateRule(rule: unknown, path = 'rule', depth = 0): asserts rule is Rule {
  if (depth > MAX_DEPTH) fail(path, `nesting deeper than ${MAX_DEPTH}`);
  if (rule === null || typeof rule !== 'object') fail(path, 'must be an object');
  const kind = (rule as { kind?: unknown }).kind;
  switch (kind) {
    case 'in': {
      const r = rule as { attr?: unknown; values?: unknown };
      if (typeof r.attr !== 'string' || r.attr.length === 0) fail(path, 'attr must be a non-empty string');
      if (!Array.isArray(r.values)) fail(path, 'values must be an array (finite set)');
      if (r.values.length === 0) fail(path, 'values must not be empty');
      if (r.values.length > MAX_SET_SIZE) fail(path, `values larger than ${MAX_SET_SIZE} elements`);
      r.values.forEach((v, i) => {
        if (!isAttributeValue(v)) fail(`${path}.values[${i}]`, 'must be a string, boolean or finite number');
      });
      return;
    }
    case 'int': {
      const r = rule as { attr?: unknown; cmp?: unknown; value?: unknown };
      if (typeof r.attr !== 'string' || r.attr.length === 0) fail(path, 'attr must be a non-empty string');
      if (!INT_CMPS.includes(r.cmp as IntCmp)) fail(path, `cmp must be one of ${INT_CMPS.join(', ')}`);
      if (typeof r.value !== 'number' || !Number.isSafeInteger(r.value)) {
        fail(path, 'value must be a safe integer');
      }
      return;
    }
    case 'and':
    case 'or': {
      const r = rule as { rules?: unknown };
      if (!Array.isArray(r.rules) || r.rules.length === 0) fail(path, 'rules must be a non-empty array');
      r.rules.forEach((sub, i) => validateRule(sub, `${path}.rules[${i}]`, depth + 1));
      return;
    }
    case 'not': {
      const r = rule as { rule?: unknown };
      validateRule(r.rule, `${path}.rule`, depth + 1);
      return;
    }
    default:
      fail(path, `unknown kind ${JSON.stringify(kind)}; allowed: in, int, and, or, not`);
  }
}

function compareInt(cmp: IntCmp, actual: number, expected: number): boolean {
  switch (cmp) {
    case 'eq': return actual === expected;
    case 'lt': return actual < expected;
    case 'lte': return actual <= expected;
    case 'gt': return actual > expected;
    case 'gte': return actual >= expected;
  }
}

export function evaluateRule(rule: Rule, attrs: AttributeSet): boolean {
  switch (rule.kind) {
    case 'in': {
      if (!Object.hasOwn(attrs, rule.attr)) return false;
      return rule.values.includes(attrs[rule.attr]);
    }
    case 'int': {
      const actual = attrs[rule.attr];
      if (typeof actual !== 'number' || !Number.isSafeInteger(actual)) return false;
      return compareInt(rule.cmp, actual, rule.value);
    }
    case 'and':
      return rule.rules.every((sub) => evaluateRule(sub, attrs));
    case 'or':
      return rule.rules.some((sub) => evaluateRule(sub, attrs));
    case 'not':
      return !evaluateRule(rule.rule, attrs);
  }
}

function deepFreeze<T>(value: T, seen = new Set<object>()): T {
  if (value !== null && typeof value === 'object' && !seen.has(value)) {
    seen.add(value as object);
    for (const key of Object.keys(value)) {
      deepFreeze((value as Record<string, unknown>)[key], seen);
    }
    Object.freeze(value);
  }
  return value;
}

export function createPolicy(version: number, name: string, rule: Rule): Policy {
  if (!Number.isSafeInteger(version) || version < 1) {
    throw new InvalidPolicyError('version must be a positive integer');
  }
  if (typeof name !== 'string' || name.length === 0) {
    throw new InvalidPolicyError('name must be a non-empty string');
  }
  validateRule(rule);
  return deepFreeze<Policy>({ version, name, rule });
}

export function freezeAttributes(attrs: Readonly<Record<string, unknown>>): AttributeSet {
  const out: Record<string, AttributeValue> = {};
  for (const [key, value] of Object.entries(attrs)) {
    if (!isAttributeValue(value)) {
      throw new InvalidPolicyError(
        `attribute "${key}" must be a string, boolean or finite number`,
      );
    }
    out[key] = value;
  }
  return Object.freeze(out);
}
