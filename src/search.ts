import type { Ast, MatchVector, SearchMatch, VectorCache } from './types.js';
import { parsePattern } from './parse.js';
import { compareCaps, enumMatchVectors } from './vectors.js';

export function searchPattern(source: string, input: string): SearchMatch | null {
  const parsed = parsePattern(source);
  return searchAst(parsed.ast, parsed.groupCount, input);
}

export function searchAst(ast: Ast, groupCount: number, input: string): SearchMatch | null {
  const cache: VectorCache = new Map();
  for (let start = 0; start <= input.length; start++) {
    const vectors = enumMatchVectors(ast, input, start, groupCount, cache);
    if (vectors.length === 0) continue;
    let best = vectors[0];
    for (const vector of vectors) {
      if (compareMatchVectors(vector, best) < 0) best = vector;
    }
    const groups: Array<string | undefined> = [];
    for (const span of best.caps) {
      if (span === undefined) {
        groups.push(undefined);
      } else {
        groups.push(input.slice(span[0], span[1]));
      }
    }
    return { start, end: best.end, match: input.slice(start, best.end), groups };
  }
  return null;
}

export function compareMatchVectors(a: MatchVector, b: MatchVector): number {
  if (a.end !== b.end) return b.end - a.end;
  return compareCaps(a.caps, b.caps);
}
