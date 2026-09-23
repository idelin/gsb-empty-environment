export function deepClone<T>(value: T): T {
  return value === undefined ? value : structuredClone(value);
}

export function deepFreeze<T>(value: T): T {
  if (value !== null && typeof value === 'object') {
    for (const key of Object.keys(value)) {
      deepFreeze((value as Record<string, unknown>)[key]);
    }
    Object.freeze(value);
  }
  return value;
}

export function nowIso(): string {
  return new Date().toISOString();
}
