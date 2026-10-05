/**
 * Resource path patterns.
 *
 * Supported forms:
 * - "*"        matches every path
 * - "a/b/*"    matches "a/b" itself and anything beneath it
 * - "a/b/c"    matches exactly that path
 */

export function matchPattern(pattern: string, path: string): boolean {
  if (pattern === "*") return true;
  if (pattern.endsWith("/*")) {
    const prefix = pattern.slice(0, -2);
    return path === prefix || path.startsWith(prefix + "/");
  }
  return pattern === path;
}

/**
 * Specificity score: more concrete (non-wildcard) segments win.
 * A trailing wildcard contributes nothing. Used to break ties between
 * rules attached to the same org node / the same user.
 */
export function specificity(pattern: string): number {
  if (pattern === "*") return 0;
  const segments = pattern.split("/");
  if (segments[segments.length - 1] === "*") return segments.length - 1;
  return segments.length;
}
