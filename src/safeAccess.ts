/**
 * Safe property access layer.
 *
 * Policy:
 * - Only *own, explicit data properties* are visible. Prototype-chain
 *   properties are never read. Accessor properties (getters/setters) are
 *   treated as absent and are NEVER invoked.
 * - `__proto__`, `constructor` and `prototype` are denied unconditionally.
 * - Functions (host objects) are never called and never traversed into.
 * - Values are read exclusively through own data descriptors, so no `[[Get]]`
 *   ever runs: getters and Proxy `get` traps cannot fire. (Descriptor
 *   introspection itself — `getOwnPropertyDescriptor`/`hasOwnProperty` — is
 *   the only reflection used; a Proxy may observe those traps, but no user
 *   code value is ever invoked.)
 */

export const DENIED_KEYS: ReadonlySet<string> = new Set([
  '__proto__',
  'constructor',
  'prototype',
]);

export type OwnResult =
  | { kind: 'value'; value: unknown }
  | { kind: 'absent' }
  | { kind: 'denied' };

/** Plain data containers only: non-null objects that are not functions. */
export function isDataContainer(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

/** Read an own explicit data property without ever triggering user code. */
export function getOwn(container: unknown, key: string): OwnResult {
  if (DENIED_KEYS.has(key)) return { kind: 'denied' };
  if (!isDataContainer(container)) return { kind: 'absent' };
  const desc = Object.getOwnPropertyDescriptor(container, key);
  if (desc === undefined) return { kind: 'absent' };
  if ('get' in desc || 'set' in desc) return { kind: 'absent' }; // accessor: invisible
  return { kind: 'value', value: desc.value };
}

/**
 * Enumerate visible child keys.
 * - Arrays: existing indices only — holes are skipped, so a hole and an
 *   explicit `undefined` element are observably different.
 * - Objects: own enumerable string keys minus denied keys.
 */
export function ownKeys(container: unknown): string[] {
  if (!isDataContainer(container)) return [];
  if (Array.isArray(container)) {
    const keys: string[] = [];
    for (let i = 0; i < container.length; i++) {
      if (Object.prototype.hasOwnProperty.call(container, i)) keys.push(String(i));
    }
    return keys;
  }
  return Object.keys(container).filter((k) => !DENIED_KEYS.has(k));
}
