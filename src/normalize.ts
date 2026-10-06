/** Normalize a path/prefix: leading slash, collapsed separators, no trailing slash. */
export function normalizePath(input: string): string {
  let s = input.trim();
  if (s === '') return '/';
  if (!s.startsWith('/')) s = '/' + s;
  s = s.replace(/\/+/g, '/');
  if (s.length > 1) s = s.replace(/\/+$/g, '');
  return s;
}

export function segments(path: string): string[] {
  const n = normalizePath(path);
  return n === '/' ? [] : n.slice(1).split('/');
}

export function depth(path: string): number {
  return segments(path).length;
}

/** Segment-aware prefix match: `/a` matches `/a` and `/a/b` but not `/ab`. */
export function matchesPrefix(prefix: string, path: string): boolean {
  const pre = normalizePath(prefix);
  const p = normalizePath(path);
  if (pre === '/') return true;
  return p === pre || p.startsWith(pre + '/');
}
