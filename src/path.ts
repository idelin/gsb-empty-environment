/** A concrete path is a list of segments. Array indices are canonical numeric strings. */
export type Path = string[];

/**
 * Parses '/a/b/0' -> ['a','b','0']. Leading '/' optional. '' or '/' -> [] (root).
 * Concrete paths may not contain the reserved wildcard segments '*' or '**'.
 */
export function parsePath(p: string | readonly string[]): Path {
  if (Array.isArray(p)) return [...p];
  let s = p;
  if (s.startsWith('/')) s = s.slice(1);
  if (s === '') return [];
  const segs = s.split('/');
  for (const seg of segs) {
    if (seg === '') throw new Error(`empty segment in path: ${p}`);
    if (seg === '*' || seg === '**') throw new Error(`wildcard segment not allowed in concrete path: ${p}`);
  }
  return segs;
}

export function formatPath(p: readonly string[]): string {
  return '/' + p.join('/');
}

/**
 * Segment-wise prefix test. Note: ancestry is defined on segments, so ['ab']
 * is NOT an ancestor of ['abc'] even though 'ab' is a string prefix of 'abc'.
 */
export function isPrefixPath(a: readonly string[], b: readonly string[]): boolean {
  if (a.length > b.length) return false;
  for (let i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
  return true;
}
