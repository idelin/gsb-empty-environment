// Generated from src/equal.ts (regenerate with `npm run build`).
const hasOwn = (obj, key) => Object.prototype.hasOwnProperty.call(obj, key);

/** Structural equality for JSON-like config values. */
export function deepEqual(a, b) {
  if (a === b) return true;
  if (typeof a !== 'object' || typeof b !== 'object' || a === null || b === null) {
    return false;
  }
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((item, index) => deepEqual(item, b[index]));
  }
  const aKeys = Object.keys(a);
  const bKeys = Object.keys(b);
  if (aKeys.length !== bKeys.length) return false;
  return aKeys.every((key) => hasOwn(b, key) && deepEqual(a[key], b[key]));
}

/** Deep clone for JSON-like config values. */
export function deepClone(value) {
  if (typeof value !== 'object' || value === null) return value;
  if (Array.isArray(value)) return value.map((item) => deepClone(item));
  const out = {};
  for (const [key, item] of Object.entries(value)) {
    out[key] = deepClone(item);
  }
  return out;
}
