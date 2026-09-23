/** A path is a tuple of segments, e.g. ['users', 'u1', 'name'] for /users/u1/name. */
export type Path = readonly string[];

export function parsePath(source: string): string[] {
  if (source === '/' || source === '') return [];
  const parts = source.split('/');
  if (parts[0] !== '') throw new Error(`path must start with '/': ${source}`);
  const segs = parts.slice(1);
  for (const seg of segs) {
    if (seg === '') throw new Error(`empty segment in path: ${source}`);
  }
  return segs;
}

export function toPath(path: string | Path): string[] {
  return typeof path === 'string' ? parsePath(path) : [...path];
}

export function formatPath(path: Path): string {
  return path.length === 0 ? '/' : '/' + path.join('/');
}

/**
 * Segment-wise prefix test. This is what makes "adjacent string prefixes"
 * NOT ancestors: ['a', 'b'] is not a prefix of ['a', 'bc'] even though
 * the string "/a/b" is a string-prefix of "/a/bc".
 */
export function isPrefixPath(prefix: Path, path: Path): boolean {
  if (prefix.length > path.length) return false;
  for (let i = 0; i < prefix.length; i++) {
    if (prefix[i] !== path[i]) return false;
  }
  return true;
}
