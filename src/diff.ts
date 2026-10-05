/**
 * Minimal interval replacement lists between two token sequences.
 *
 * Tokens are compared by their full contents (UTF-16 range, type, binding).
 * A Myers O(ND) diff finds a minimal edit script; the gaps between maximal
 * kept blocks are emitted as replacement intervals over the old token list.
 */

import type { SemanticToken } from './analyzer.ts';

export interface TokenPatch {
  /** Index into the old token list, inclusive. */
  start: number;
  /** Index into the old token list, exclusive. */
  end: number;
  /** Tokens replacing the old interval [start, end). */
  tokens: SemanticToken[];
}

export function tokenKey(token: SemanticToken): string {
  return `${token.start}:${token.end}:${token.type}:${token.binding}`;
}

type Op = 'keep' | 'delete' | 'insert';

/** Myers O(ND) diff; returns a minimal edit script turning a into b. */
function myersOps(a: readonly string[], b: readonly string[]): Op[] {
  const n = a.length;
  const m = b.length;
  const max = n + m;
  if (max === 0) return [];

  const offset = max;
  const width = 2 * max + 1;
  const v = new Int32Array(width);
  const trace: Int32Array[] = [];
  let found = -1;

  for (let d = 0; d <= max; d++) {
    trace.push(v.slice());
    for (let k = -d; k <= d; k += 2) {
      let x: number;
      if (k === -d || (k !== d && v[k - 1 + offset] < v[k + 1 + offset])) {
        x = v[k + 1 + offset];
      } else {
        x = v[k - 1 + offset] + 1;
      }
      let y = x - k;
      while (x < n && y < m && a[x] === b[y]) {
        x++;
        y++;
      }
      v[k + offset] = x;
      if (x >= n && y >= m) {
        found = d;
        break;
      }
    }
    if (found >= 0) break;
  }

  const ops: Op[] = [];
  let x = n;
  let y = m;
  for (let d = found; d >= 0; d--) {
    const vv = trace[d];
    const k = x - y;
    const prevK =
      k === -d || (k !== d && vv[k - 1 + offset] < vv[k + 1 + offset]) ? k + 1 : k - 1;
    const prevX = vv[prevK + offset];
    const prevY = prevX - prevK;
    while (x > prevX && y > prevY) {
      ops.push('keep');
      x--;
      y--;
    }
    if (d > 0) {
      if (x === prevX) {
        ops.push('insert');
        y--;
      } else {
        ops.push('delete');
        x--;
      }
    }
    x = prevX;
    y = prevY;
  }
  ops.reverse();
  return ops;
}

/**
 * Compute the minimal list of interval replacements transforming
 * `oldTokens` into `newTokens`. Intervals are sorted, non-overlapping, and
 * reference indexes into `oldTokens`.
 */
export function computePatches(
  oldTokens: readonly SemanticToken[],
  newTokens: readonly SemanticToken[],
): TokenPatch[] {
  const ops = myersOps(oldTokens.map(tokenKey), newTokens.map(tokenKey));
  const patches: TokenPatch[] = [];
  let oldIndex = 0;
  let newIndex = 0;
  let pending: TokenPatch | null = null;

  const flush = (): void => {
    if (pending !== null) {
      patches.push(pending);
      pending = null;
    }
  };

  for (const op of ops) {
    if (op === 'keep') {
      flush();
      oldIndex++;
      newIndex++;
    } else if (op === 'delete') {
      if (pending === null) pending = { start: oldIndex, end: oldIndex, tokens: [] };
      pending.end = oldIndex + 1;
      oldIndex++;
    } else {
      if (pending === null) pending = { start: oldIndex, end: oldIndex, tokens: [] };
      pending.tokens.push(newTokens[newIndex]);
      newIndex++;
    }
  }
  flush();
  return patches;
}

/**
 * Apply a replacement list to a token array, producing the next version.
 * Patches must be sorted by `start` and non-overlapping.
 */
export function applyPatches(
  base: readonly SemanticToken[],
  patches: readonly TokenPatch[],
): SemanticToken[] {
  for (let i = 0; i < patches.length; i++) {
    const patch = patches[i];
    if (
      !Number.isInteger(patch.start) ||
      !Number.isInteger(patch.end) ||
      patch.start < 0 ||
      patch.start > patch.end ||
      patch.end > base.length
    ) {
      throw new Error(`invalid patch interval [${patch.start}, ${patch.end}) at index ${i}`);
    }
    if (i > 0 && patch.start < patches[i - 1].end) {
      throw new Error(`patches overlap or are unsorted at index ${i}`);
    }
  }
  const result = base.slice();
  for (let i = patches.length - 1; i >= 0; i--) {
    const patch = patches[i];
    result.splice(patch.start, patch.end - patch.start, ...patch.tokens);
  }
  return result;
}
