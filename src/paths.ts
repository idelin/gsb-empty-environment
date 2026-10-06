/**
 * Normalizes a path prefix: ensures a leading slash, collapses duplicate
 * slashes and strips trailing slashes (except the root "/").
 * Returns null for empty / non-string input.
 */
export function normalizePrefix(prefix: string): string | null {
  if (typeof prefix !== 'string' || prefix.length === 0) return null;
  let p = prefix;
  if (!p.startsWith('/')) p = '/' + p;
  p = p.replace(/\/{2,}/g, '/');
  if (p.length > 1) p = p.replace(/\/+$/g, '');
  return p.length === 0 ? '/' : p;
}

/**
 * Segment-aware prefix match: "/a/b" matches "/a/b" and "/a/b/c"
 * but not "/a/bc". The root "/" matches every path.
 */
export function prefixMatches(prefix: string, path: string): boolean {
  if (prefix === '/') return true;
  return path === prefix || path.startsWith(prefix + '/');
}

/** Specificity of a prefix: number of path segments. Root "/" has depth 0. */
export function prefixDepth(prefix: string): number {
  if (prefix === '/') return 0;
  return prefix.split('/').length - 1;
}
