/**
 * Content hashing for map keys, values and tree nodes.
 *
 * Hashes are 32-bit unsigned integers. They power the subtree-skip
 * optimization in the diff engine: two subtrees with equal hashes are
 * *probably* identical. Whether "probably" is good enough is an explicit
 * policy decision, see `HashTrust` in `diff.ts`.
 */

export type HashFn<T> = (value: T) => number;

/** FNV-1a 32-bit. */
export function hashString(str: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < str.length; i++) {
    h ^= str.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return h >>> 0;
}

/**
 * Default hash for primitive keys/values. Structured values require an
 * explicit `hashKey` / `hashValue` so the caller controls the
 * collision-resistance of their summaries.
 */
export function defaultHash(value: unknown): number {
  switch (typeof value) {
    case 'string':
      return hashString(`s:${value as string}`);
    case 'number':
      return hashString(`n:${value as number}`);
    case 'bigint':
      return hashString(`i:${(value as bigint).toString(36)}`);
    case 'boolean':
      return value ? 0x9e3779b1 : 0x85ebca6b;
    case 'undefined':
      return 0x27d4eb2f;
    case 'object':
      if (value === null) return 0x165667b1;
      break;
  }
  throw new Error(
    'pmap-diff: no default hash for this key/value type; ' +
      'pass hashKey/hashValue to PMap.empty()',
  );
}

/** Order-sensitive combine of three 32-bit hashes, with final avalanche. */
export function mix3(a: number, b: number, c: number): number {
  let h = 0x811c9dc5;
  h = Math.imul(h ^ a, 0x01000193);
  h = Math.imul(h ^ b, 0x01000193);
  h = Math.imul(h ^ c, 0x01000193);
  h ^= h >>> 16;
  h = Math.imul(h, 0x7feb352d);
  h ^= h >>> 15;
  return h >>> 0;
}
