import { parsePath } from './path.ts';
import type { Path } from './path.ts';

/**
 * Subscription pattern segment:
 *  - lit:  literal segment, must equal the path segment
 *  - star: '*', matches exactly one arbitrary segment
 *  - deep: '**' (only allowed as the last segment), matches any remaining depth
 *
 * Matching uses ancestor semantics: a pattern matches a change path when the
 * pattern matches a *prefix* of the path, so a subscriber on /a/b also hears
 * about changes under /a/b/c.
 */
export type PatternSeg =
  | { kind: 'lit'; value: string }
  | { kind: 'star' }
  | { kind: 'deep' };

export type Pattern = readonly PatternSeg[];

export function parsePattern(source: string): Pattern {
  const segs = parsePath(source);
  const out: PatternSeg[] = [];
  for (let i = 0; i < segs.length; i++) {
    const seg = segs[i];
    if (seg === '*') {
      out.push({ kind: 'star' });
    } else if (seg === '**') {
      if (i !== segs.length - 1) throw new Error(`'**' must be the last segment: ${source}`);
      out.push({ kind: 'deep' });
    } else {
      out.push({ kind: 'lit', value: seg });
    }
  }
  return out;
}

/** Independent, per-subscription reference matcher (no automaton involved). */
export function patternMatchesPath(pattern: Pattern, path: Path): boolean {
  let i = 0;
  for (const seg of pattern) {
    if (seg.kind === 'deep') return true;
    if (i >= path.length) return false;
    if (seg.kind === 'lit' && seg.value !== path[i]) return false;
    i++;
  }
  return true;
}
