import { PathError } from './errors.ts';
import { FORBIDDEN_KEYS } from './parser.ts';

/**
 * Safe data-object access layer.
 *
 * Policy:
 * - Only "explicit data" is traversable: plain objects (Object.prototype or
 *   null prototype) and real arrays. Anything else -- class instances,
 *   functions, Dates, Maps, host objects -- is rejected.
 * - Properties are read exclusively through Object.getOwnPropertyDescriptor,
 *   so prototype-chain properties are always invisible and accessor getters
 *   are never invoked (an accessor property is treated as absent).
 * - __proto__ / constructor / prototype are denied even as own properties.
 * - No [[Get]], [[Set]] or [[Call]] is ever performed on user data.
 */

export type ReadResult = { found: true; value: unknown } | { found: false };

export function isPlainObject(value: unknown): value is Record<string, unknown> {
  if (typeof value !== 'object' || value === null) return false;
  if (Array.isArray(value)) return false;
  const proto = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}

export function isTraversable(value: unknown): boolean {
  return isPlainObject(value) || Array.isArray(value);
}

/**
 * Assert that a value is allowed inside a document: a scalar, a plain object
 * or an array. Throws HOST_OBJECT otherwise. Never inspects the value beyond
 * its type tag and prototype identity.
 */
export function assertDataValue(value: unknown, userPath: string): void {
  if (value === null || value === undefined) return;
  const t = typeof value;
  if (t === 'string' || t === 'boolean') return;
  if (t === 'number') {
    if (!Number.isFinite(value as number)) {
      throw new PathError('TYPE_ERROR', userPath, 'non-finite numbers are not data');
    }
    return;
  }
  if (isPlainObject(value) || Array.isArray(value)) return;
  throw new PathError(
    'HOST_OBJECT',
    userPath,
    'only plain data objects and arrays are allowed',
  );
}

/**
 * Deeply validate a value for storage: every reachable node must be explicit
 * data, and the graph must be acyclic. Iterative to avoid stack overflow.
 */
export function assertDataTree(root: unknown, userPath: string): void {
  const seen = new Set<object>();
  const stack: unknown[] = [root];
  while (stack.length > 0) {
    const value = stack.pop();
    assertDataValue(value, userPath);
    if (isPlainObject(value) || Array.isArray(value)) {
      const obj = value as object;
      if (seen.has(obj)) {
        throw new PathError('TYPE_ERROR', userPath, 'cyclic structures are not data');
      }
      seen.add(obj);
      for (const key of Object.keys(obj)) {
        if (FORBIDDEN_KEYS.has(key)) continue; // unreadable anyway; skip
        const desc = Object.getOwnPropertyDescriptor(obj, key);
        if (desc && 'value' in desc) stack.push(desc.value);
      }
    }
  }
}

/**
 * Read an own data property. Prototype properties are invisible; accessor
 * properties are treated as absent (their getter is never called); forbidden
 * keys raise a policy error.
 */
export function readOwn(holder: unknown, key: string, userPath: string): ReadResult {
  if (FORBIDDEN_KEYS.has(key)) {
    throw new PathError(
      'FORBIDDEN_PROPERTY',
      userPath,
      `access to property ${JSON.stringify(key)} is denied`,
    );
  }
  if (!isPlainObject(holder) && !Array.isArray(holder)) return { found: false };
  const desc = Object.getOwnPropertyDescriptor(holder, key);
  if (!desc || !('value' in desc)) return { found: false };
  return { found: true, value: desc.value };
}

/** Read an array element by index. Holes report { found: false }. */
export function readIndex(holder: unknown, index: number, userPath: string): ReadResult {
  if (!Array.isArray(holder)) return { found: false };
  if (index < 0 || index >= holder.length) return { found: false };
  return readOwn(holder, String(index), userPath);
}

/**
 * Enumerate readable entries of a traversable value: own enumerable data
 * properties only, forbidden keys skipped, accessors skipped, array holes
 * skipped. Returns [key, value] pairs; keys of arrays are index strings.
 */
export function ownEntries(holder: unknown): Array<[string, unknown]> {
  const out: Array<[string, unknown]> = [];
  if (!isPlainObject(holder) && !Array.isArray(holder)) return out;
  for (const key of Object.keys(holder)) {
    if (FORBIDDEN_KEYS.has(key)) continue;
    const desc = Object.getOwnPropertyDescriptor(holder, key);
    if (!desc || !('value' in desc)) continue;
    out.push([key, desc.value]);
  }
  return out;
}
