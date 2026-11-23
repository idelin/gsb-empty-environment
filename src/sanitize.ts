import { UnsafeArgumentError } from './errors.js';

const MAX_DEPTH = 100;

export const FORBIDDEN_KEYS: ReadonlySet<string> = new Set([
  '__proto__',
  'constructor',
  'prototype',
]);

function isPlainContainer(value: object): value is unknown[] | Record<string, unknown> {
  if (Array.isArray(value)) {
    return true;
  }
  let prototype: object | null;
  try {
    prototype = Object.getPrototypeOf(value);
  } catch (error) {
    const reason = error instanceof Error ? error.message : String(error);
    fail(`proxy trap failed while reading the prototype: ${reason}`);
  }
  return prototype === Object.prototype || prototype === null;
}

function fail(message: string): never {
  throw new UnsafeArgumentError(message);
}

/**
 * Recursively inspects a value before any structured clone so that attacker
 * controlled accessors never run and prototype pollution keys never cross the
 * trust boundary. Only JSON-like data (plain objects, arrays, primitives) is
 * accepted.
 */
export function scanValue(value: unknown, depth = 0, seen: Set<object> = new Set()): void {
  if (value === null || typeof value !== 'object') {
    if (typeof value === 'function' || typeof value === 'symbol') {
      fail(`values of type ${typeof value} cannot cross the bridge`);
    }
    return;
  }

  if (depth > MAX_DEPTH) {
    fail(`argument nesting exceeds the maximum depth of ${MAX_DEPTH}`);
  }
  if (!isPlainContainer(value)) {
    const name = (value as { constructor?: { name?: string } }).constructor?.name ?? 'unknown';
    fail(`only plain objects and arrays can cross the bridge, rejected ${name}`);
  }
  if (seen.has(value)) {
    fail('circular references cannot cross the bridge');
  }
  seen.add(value);

  const container = value as Record<PropertyKey, unknown>;
  let keys: PropertyKey[];
  try {
    keys = Reflect.ownKeys(container);
  } catch (error) {
    const reason = error instanceof Error ? error.message : String(error);
    fail(`proxy trap failed while enumerating keys: ${reason}`);
  }

  for (const key of keys) {
    if (typeof key === 'symbol') {
      fail('symbol keys cannot cross the bridge');
    }
    if (FORBIDDEN_KEYS.has(key)) {
      fail(`forbidden prototype pollution key cannot cross the bridge: ${key}`);
    }

    let descriptor: PropertyDescriptor | undefined;
    try {
      descriptor = Reflect.getOwnPropertyDescriptor(container, key);
    } catch (error) {
      const reason = error instanceof Error ? error.message : String(error);
      fail(`proxy trap failed while inspecting ${String(key)}: ${reason}`);
    }
    if (descriptor === undefined) {
      continue;
    }
    if ('get' in descriptor || 'set' in descriptor) {
      fail(`accessor properties (getters/setters) cannot cross the bridge at ${String(key)}`);
    }

    scanValue(descriptor.value, depth + 1, seen);
  }
}

/**
 * Snapshot the raw, potentially exotic (Proxy/getter) argument into an
 * attacker-free plain value. The pre-clone scan rejects functions and getters
 * without invoking them; the post-clone scan guarantees the handler only ever
 * touches a verified, immutable-for-the-handler snapshot.
 */
export function safeClone<T>(raw: T): T {
  scanValue(raw);

  let snapshot: T;
  try {
    snapshot = structuredClone(raw);
  } catch (error) {
    if (error instanceof UnsafeArgumentError) {
      throw error;
    }
    const reason = error instanceof Error ? error.message : String(error);
    fail(`argument could not be structurally cloned: ${reason}`);
  }

  scanValue(snapshot);
  return snapshot;
}
