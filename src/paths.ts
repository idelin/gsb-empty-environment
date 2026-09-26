/**
 * Instance navigation with explicit missing-value semantics.
 *
 * A lookup never fabricates values: a missing key/index yields `missing`,
 * never zero, never an empty list, never null.
 */

export type Json = null | boolean | number | string | Json[] | { [k: string]: Json };

export type Lookup =
  | { status: 'present'; value: Json }
  | { status: 'missing' };

/** Split "/items/0/price" into ["items", "0", "price"]. "" splits to []. */
export function parsePath(path: string): string[] {
  if (path === '' || path === '/') return [];
  return path
    .replace(/^\//, '')
    .split('/')
    .map((seg) => seg.replace(/~1/g, '/').replace(/~0/g, '~'));
}

/** Navigate `root` along `path`. Missing anywhere along the way => missing. */
export function getPath(root: Json, path: string): Lookup {
  let cur: Json = root;
  for (const seg of parsePath(path)) {
    if (Array.isArray(cur)) {
      if (!/^\d+$/.test(seg)) return { status: 'missing' };
      const idx = Number(seg);
      if (idx >= cur.length) return { status: 'missing' };
      cur = cur[idx];
    } else if (cur !== null && typeof cur === 'object') {
      if (!Object.prototype.hasOwnProperty.call(cur, seg)) return { status: 'missing' };
      cur = (cur as Record<string, Json>)[seg];
    } else {
      return { status: 'missing' };
    }
  }
  return { status: 'present', value: cur };
}

/** True iff the value exists at all (null counts as present-but-null). */
export function exists(root: Json, path: string): boolean {
  return getPath(root, path).status === 'present';
}

export type TypeName = 'null' | 'boolean' | 'number' | 'string' | 'list' | 'object';

export function typeOf(value: Json): TypeName {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'list';
  return typeof value as TypeName;
}
