import type { ConfigObject } from './types.js';

const hasOwn = (obj: object, key: string): boolean =>
  Object.prototype.hasOwnProperty.call(obj, key);

/** Structural equality for JSON-like config values. */
export function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) {
    return false;
  }
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((item, index) => deepEqual(item, (b as unknown[])[index]));
  }
  const aObj = a as ConfigObject;
  const bObj = b as ConfigObject;
  const aKeys = Object.keys(aObj);
  const bKeys = Object.keys(bObj);
  if (aKeys.length !== bKeys.length) return false;
  return aKeys.every((key) => hasOwn(bObj, key) && deepEqual(aObj[key], bObj[key]));
}

/** Deep clone for JSON-like config values. */
export function deepClone<T>(value: T): T {
  if (typeof value !== 'object' || value === null) return value;
  if (Array.isArray(value)) return value.map((item) => deepClone(item)) as unknown as T;
  const out: ConfigObject = {};
  for (const [key, item] of Object.entries(value as ConfigObject)) {
    out[key] = deepClone(item);
  }
  return out as unknown as T;
}
