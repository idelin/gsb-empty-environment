/** A pattern segment: a literal, '*' (exactly one segment) or '**' (zero or more segments). */
export type PatSeg = string;
export type Pattern = PatSeg[];

/**
 * Subscription semantics: a pattern matches a change path when it matches a
 * PREFIX of that path — i.e. every subscription is a subtree subscription, so
 * subscribing to a parent path yields notifications for changes to children.
 */
export function parsePattern(p: string | readonly string[]): Pattern {
  if (Array.isArray(p)) return [...p];
  let s = p;
  if (s.startsWith('/')) s = s.slice(1);
  if (s === '') return [];
  const segs = s.split('/');
  for (const seg of segs) {
    if (seg === '') throw new Error(`empty segment in pattern: ${p}`);
  }
  return segs;
}
