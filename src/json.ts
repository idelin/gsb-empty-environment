export type JsonValue = string | number | boolean | null | JsonValue[] | { [key: string]: JsonValue };

export function isPlainObject(value: JsonValue): value is { [key: string]: JsonValue } {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

export function deepEqual(a: JsonValue, b: JsonValue): boolean {
  if (a === b) return true;
  if (typeof a !== typeof b || a === null || b === null) return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((item, i) => deepEqual(item, b[i] as JsonValue));
  }
  if (typeof a === 'object') {
    const aObj = a as { [key: string]: JsonValue };
    const bObj = b as { [key: string]: JsonValue };
    const aKeys = Object.keys(aObj);
    const bKeys = Object.keys(bObj);
    if (aKeys.length !== bKeys.length) return false;
    return aKeys.every((k) => Object.prototype.hasOwnProperty.call(bObj, k) && deepEqual(aObj[k] as JsonValue, bObj[k] as JsonValue));
  }
  return false;
}

export function deepClone<T extends JsonValue>(value: T): T {
  return structuredClone(value);
}
