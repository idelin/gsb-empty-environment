import type { Json } from './types.ts';

/** Stable serialization with sorted object keys; doubles as deep equality. */
export function canonical(value: unknown): string {
  if (value === undefined) return 'undefined';
  if (value === null || typeof value !== 'object') return JSON.stringify(value) ?? 'undefined';
  if (Array.isArray(value)) return '[' + value.map(canonical).join(',') + ']';
  const obj = value as Record<string, unknown>;
  const body = Object.keys(obj)
    .sort()
    .map((k) => JSON.stringify(k) + ':' + canonical(obj[k]))
    .join(',');
  return '{' + body + '}';
}

export function deepEqual(a: unknown, b: unknown): boolean {
  return canonical(a) === canonical(b);
}

export function typeOf(instance: Json): string {
  if (instance === null) return 'null';
  if (Array.isArray(instance)) return 'array';
  return typeof instance; // 'boolean' | 'number' | 'string' | 'object'
}

function matchesType(instance: Json, expected: string): boolean {
  if (expected === 'integer') {
    return typeof instance === 'number' && Number.isInteger(instance);
  }
  return typeOf(instance) === expected;
}

/**
 * Atomic (non-recursive) keyword checks. Each returns a failure message or
 * null when the keyword does not apply / is satisfied. Applicator keywords
 * (properties, items, oneOf, ...) are handled by the validator itself.
 */
export type KeywordCheck = (expected: unknown, instance: Json) => string | null;

export const keywordChecks: Record<string, KeywordCheck> = {
  type(expected, instance) {
    const list = Array.isArray(expected) ? expected : [expected];
    if (list.some((t) => typeof t === 'string' && matchesType(instance, t))) return null;
    return `expected type ${list.join(' | ')}, got ${typeOf(instance)}`;
  },

  enum(expected, instance) {
    const list = Array.isArray(expected) ? expected : [];
    if (list.some((v) => deepEqual(v, instance))) return null;
    return `value not in enum ${canonical(list)}`;
  },

  const(expected, instance) {
    if (deepEqual(expected, instance)) return null;
    return `expected const ${canonical(expected)}`;
  },

  minLength(expected, instance) {
    if (typeof instance !== 'string' || typeof expected !== 'number') return null;
    return instance.length >= expected ? null : `length ${instance.length} < minLength ${expected}`;
  },

  maxLength(expected, instance) {
    if (typeof instance !== 'string' || typeof expected !== 'number') return null;
    return instance.length <= expected ? null : `length ${instance.length} > maxLength ${expected}`;
  },

  pattern(expected, instance) {
    if (typeof instance !== 'string' || typeof expected !== 'string') return null;
    return new RegExp(expected).test(instance) ? null : `does not match pattern ${JSON.stringify(expected)}`;
  },

  minimum(expected, instance) {
    if (typeof instance !== 'number' || typeof expected !== 'number') return null;
    return instance >= expected ? null : `${instance} < minimum ${expected}`;
  },

  maximum(expected, instance) {
    if (typeof instance !== 'number' || typeof expected !== 'number') return null;
    return instance <= expected ? null : `${instance} > maximum ${expected}`;
  },

  exclusiveMinimum(expected, instance) {
    if (typeof instance !== 'number' || typeof expected !== 'number') return null;
    return instance > expected ? null : `${instance} <= exclusiveMinimum ${expected}`;
  },

  exclusiveMaximum(expected, instance) {
    if (typeof instance !== 'number' || typeof expected !== 'number') return null;
    return instance < expected ? null : `${instance} >= exclusiveMaximum ${expected}`;
  },

  multipleOf(expected, instance) {
    if (typeof instance !== 'number' || typeof expected !== 'number' || expected === 0) return null;
    return instance % expected === 0 ? null : `${instance} is not a multiple of ${expected}`;
  },

  minItems(expected, instance) {
    if (!Array.isArray(instance) || typeof expected !== 'number') return null;
    return instance.length >= expected ? null : `items ${instance.length} < minItems ${expected}`;
  },

  maxItems(expected, instance) {
    if (!Array.isArray(instance) || typeof expected !== 'number') return null;
    return instance.length <= expected ? null : `items ${instance.length} > maxItems ${expected}`;
  },

  required(expected, instance) {
    if (instance === null || typeof instance !== 'object' || Array.isArray(instance)) return null;
    if (!Array.isArray(expected)) return null;
    const missing = expected.filter(
      (k): k is string => typeof k === 'string' && !(k in (instance as Record<string, Json>)),
    );
    return missing.length === 0 ? null : `missing required ${missing.map((m) => JSON.stringify(m)).join(', ')}`;
  },
};

/** Keywords this implementation promises to enforce. Everything else is ignored. */
export const APPLICATOR_KEYWORDS = ['properties', 'items', 'additionalProperties', 'oneOf', 'anyOf'] as const;
