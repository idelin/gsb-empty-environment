export function deepFreeze<T>(value: T, seen: WeakSet<object> = new WeakSet()): T {
  if (value === null || typeof value !== 'object') return value;
  const obj = value as unknown as object;
  if (seen.has(obj) || Object.isFrozen(obj)) return value;
  seen.add(obj);
  for (const key of Reflect.ownKeys(obj)) {
    deepFreeze((obj as Record<PropertyKey, unknown>)[key], seen);
  }
  Object.freeze(obj);
  return value;
}
