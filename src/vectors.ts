import type { Ast, MatchVector, Span, VectorCache } from './types.js';

const NO_CACHE: VectorCache | undefined = undefined;

export function enumMatchVectors(
  ast: Ast,
  input: string,
  pos: number,
  groupCount: number,
  cache: VectorCache | undefined = NO_CACHE,
): MatchVector[] {
  if (cache !== undefined) {
    const byPos = cache.get(ast);
    const hit = byPos === undefined ? undefined : byPos.get(pos);
    if (hit !== undefined) return hit;
  }
  const result = enumUncached(ast, input, pos, groupCount, cache);
  if (cache !== undefined) {
    let byPos = cache.get(ast);
    if (byPos === undefined) {
      byPos = new Map();
      cache.set(ast, byPos);
    }
    byPos.set(pos, result);
  }
  return result;
}

export function compareCaps(
  a: ReadonlyArray<Span | undefined>,
  b: ReadonlyArray<Span | undefined>,
): number {
  const count = Math.max(a.length, b.length);
  for (let i = 0; i < count; i++) {
    const rankA = spanRank(a[i]);
    const rankB = spanRank(b[i]);
    if (rankA !== rankB) return rankB - rankA;
  }
  return 0;
}

function enumUncached(
  ast: Ast,
  input: string,
  pos: number,
  groupCount: number,
  cache: VectorCache | undefined,
): MatchVector[] {
  switch (ast.kind) {
    case 'lit': {
      if (pos < input.length && input[pos] === ast.ch) {
        return [{ end: pos + 1, caps: emptyCaps(groupCount) }];
      }
      return [];
    }
    case 'concat': {
      let vectors: MatchVector[] = [{ end: pos, caps: emptyCaps(groupCount) }];
      for (const part of ast.parts) {
        const next: MatchVector[] = [];
        for (const vector of vectors) {
          for (const step of enumMatchVectors(part, input, vector.end, groupCount, cache)) {
            next.push({ end: step.end, caps: mergeCaps(vector.caps, step.caps, groupCount) });
          }
        }
        vectors = pruneBest(next);
        if (vectors.length === 0) break;
      }
      return vectors;
    }
    case 'alt': {
      const all: MatchVector[] = [];
      for (const option of ast.options) {
        for (const vector of enumMatchVectors(option, input, pos, groupCount, cache)) {
          all.push(vector);
        }
      }
      return pruneBest(all);
    }
    case 'star': {
      return enumStarVectors(ast.inner, input, pos, groupCount, cache);
    }
    case 'group': {
      const out: MatchVector[] = [];
      for (const vector of enumMatchVectors(ast.inner, input, pos, groupCount, cache)) {
        const caps = vector.caps.slice();
        caps[ast.index - 1] = [pos, vector.end];
        out.push({ end: vector.end, caps });
      }
      return pruneBest(out);
    }
  }
}

function enumStarVectors(
  inner: Ast,
  input: string,
  pos: number,
  groupCount: number,
  cache: VectorCache | undefined,
): MatchVector[] {
  const best: Array<MatchVector | undefined> = new Array(input.length + 1).fill(undefined);
  best[pos] = { end: pos, caps: emptyCaps(groupCount) };
  const results: Array<MatchVector | undefined> = new Array(input.length + 1).fill(undefined);
  const relaxResult = (vector: MatchVector): void => {
    const current = results[vector.end];
    if (current === undefined || compareCaps(vector.caps, current.caps) < 0) {
      results[vector.end] = vector;
    }
  };
  for (let end = pos; end <= input.length; end++) {
    const state = best[end];
    if (state === undefined) continue;
    relaxResult(state);
    for (const step of enumMatchVectors(inner, input, end, groupCount, cache)) {
      const merged: MatchVector = { end: step.end, caps: mergeCaps(state.caps, step.caps, groupCount) };
      if (step.end === end) {
        relaxResult(merged);
      } else {
        const current = best[step.end];
        if (current === undefined || compareCaps(merged.caps, current.caps) < 0) {
          best[step.end] = merged;
        }
      }
    }
  }
  const out: MatchVector[] = [];
  for (const vector of results) {
    if (vector !== undefined) out.push(vector);
  }
  return out;
}

function pruneBest(vectors: MatchVector[]): MatchVector[] {
  const bestByEnd: Map<number, MatchVector> = new Map();
  for (const vector of vectors) {
    const current = bestByEnd.get(vector.end);
    if (current === undefined || compareCaps(vector.caps, current.caps) < 0) {
      bestByEnd.set(vector.end, vector);
    }
  }
  return [...bestByEnd.values()];
}

function emptyCaps(groupCount: number): Array<Span | undefined> {
  return new Array(groupCount).fill(undefined);
}

function mergeCaps(
  base: ReadonlyArray<Span | undefined>,
  overlay: ReadonlyArray<Span | undefined>,
  groupCount: number,
): Array<Span | undefined> {
  const out: Array<Span | undefined> = new Array(groupCount);
  for (let i = 0; i < groupCount; i++) {
    const fromOverlay = overlay[i];
    out[i] = fromOverlay !== undefined ? fromOverlay : base[i];
  }
  return out;
}

function spanRank(span: Span | undefined): number {
  if (span === undefined) return 0;
  return span[1] - span[0] + 1;
}
