import { SemanticToken } from './analyzer';

/**
 * One minimal replacement interval: replace `deleteCount` tokens starting at
 * index `start` of the old token list with `tokens`.
 */
export interface TokenPatch {
  start: number;
  deleteCount: number;
  tokens: SemanticToken[];
}

export function tokensEqual(a: SemanticToken, b: SemanticToken): boolean {
  return (
    a.start === b.start && a.length === b.length && a.type === b.type && a.decl === b.decl
  );
}

interface EditOp {
  kind: 'equal' | 'delete' | 'insert';
  aIndex: number;
  bIndex: number;
}

/**
 * Myers O(ND) diff over token sequences; returns a minimal-length edit script.
 */
function editScript(a: SemanticToken[], b: SemanticToken[]): EditOp[] {
  const n = a.length;
  const m = b.length;
  const max = n + m;
  const offset = max;
  const width = 2 * max + 1;
  let v = new Int32Array(width).fill(0);
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
      while (x < n && y < m && tokensEqual(a[x], b[y])) {
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

  // Backtrack through the trace to recover the script.
  const ops: EditOp[] = [];
  let x = n;
  let y = m;
  for (let d = found; d > 0; d--) {
    // trace[d] holds the V row after d-1 edits (it was snapshotted at the
    // start of iteration d, before row d was computed).
    const prev = trace[d];
    const k = x - y;
    let prevK: number;
    if (k === -d || (k !== d && prev[k - 1 + offset] < prev[k + 1 + offset])) {
      prevK = k + 1; // insertion (move down)
    } else {
      prevK = k - 1; // deletion (move right)
    }
    const prevX = prev[prevK + offset];
    const prevY = prevX - prevK;
    while (x > prevX && y > prevY) {
      ops.push({ kind: 'equal', aIndex: x - 1, bIndex: y - 1 });
      x--;
      y--;
    }
    if (x === prevX) {
      ops.push({ kind: 'insert', aIndex: x, bIndex: y - 1 });
      y--;
    } else {
      ops.push({ kind: 'delete', aIndex: x - 1, bIndex: y });
      x--;
    }
  }
  while (x > 0 && y > 0) {
    ops.push({ kind: 'equal', aIndex: x - 1, bIndex: y - 1 });
    x--;
    y--;
  }
  ops.reverse();
  return ops;
}

/**
 * Compute the minimal interval replacement list transforming `oldTokens` into
 * `newTokens`. Adjacent edits are coalesced so each patch is a maximal changed
 * region; equal tokens between patches are untouched.
 */
export function diffTokens(oldTokens: SemanticToken[], newTokens: SemanticToken[]): TokenPatch[] {
  const ops = editScript(oldTokens, newTokens);
  const patches: TokenPatch[] = [];
  let current: TokenPatch | null = null;

  for (const op of ops) {
    if (op.kind === 'equal') {
      current = null;
      continue;
    }
    if (!current) {
      current = { start: op.aIndex, deleteCount: 0, tokens: [] };
      patches.push(current);
    }
    if (op.kind === 'delete') {
      current.deleteCount++;
    } else {
      current.tokens.push(newTokens[op.bIndex]);
    }
  }

  return patches;
}

/**
 * Apply a replacement list to a token array, returning the transformed array.
 * Patches must be sorted by `start` and non-overlapping (as produced by
 * {@link diffTokens}); they are applied right-to-left so indices stay valid.
 */
export function applyPatches(
  tokens: SemanticToken[],
  patches: TokenPatch[],
): SemanticToken[] {
  const result = tokens.slice();
  for (let i = patches.length - 1; i >= 0; i--) {
    const p = patches[i];
    result.splice(p.start, p.deleteCount, ...p.tokens);
  }
  return result;
}
