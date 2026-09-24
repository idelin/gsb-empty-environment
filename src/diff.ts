/**
 * Line-level diff based on Myers' O(ND) algorithm with a fully
 * deterministic tie-breaking strategy:
 *
 *  1. Common prefixes/suffixes are trimmed before the search, so a
 *     small local edit in a huge file stays cheap.
 *  2. Inside the search, when two paths tie, the insertion branch
 *     (k + 1) is preferred exactly when V[k-1] < V[k+1]; this fixed
 *     rule makes the greedy walk deterministic.
 *  3. Afterwards every hunk is slid DOWN (towards end of file) as far
 *     as possible, git-xdiff style. With repeated lines — including
 *     runs of repeated empty lines — the change is therefore always
 *     attributed to the lowest possible position, giving stable,
 *     reproducible blocks for identical inputs.
 *
 * The search trace is stored as compact per-d rows, so memory is
 * O(D^2) in the edit distance D, independent of the file size. If D
 * exceeds MAX_EDIT_DISTANCE (pathological, mostly-different files),
 * the changed middle is reported as a single replace hunk.
 */

export interface Hunk {
  /** Left line range [aStart, aEnd). Empty for pure insertions. */
  aStart: number;
  aEnd: number;
  /** Right line range [bStart, bEnd). Empty for pure deletions. */
  bStart: number;
  bEnd: number;
}

const MAX_EDIT_DISTANCE = 4096;

type Op =
  | { readonly type: 'equal'; readonly a: number; readonly b: number }
  | { readonly type: 'delete'; readonly a: number }
  | { readonly type: 'insert'; readonly b: number };

function toIds(a: readonly string[], b: readonly string[]): [number[], number[]] {
  const map = new Map<string, number>();
  const id = (s: string): number => {
    let v = map.get(s);
    if (v === undefined) {
      v = map.size;
      map.set(s, v);
    }
    return v;
  };
  return [a.map(id), b.map(id)];
}

/**
 * Myers O(ND) greedy search. Trace rows are stored per d (row d has
 * 2d+1 entries, index k+d), so total memory is O(D^2). Returns null
 * when the edit distance exceeds MAX_EDIT_DISTANCE.
 */
function myersOps(a: readonly number[], b: readonly number[]): Op[] | null {
  const n = a.length;
  const m = b.length;
  const max = n + m;
  const trace: Int32Array[] = [];
  // Previous row; for d = 0 the read of v[k+1-1] = v[0] = 0 implements
  // the classic V[1] = 0 initialisation trick.
  let v = new Int32Array(1);
  let foundD = -1;

  for (let d = 0; d <= max; d++) {
    if (d > MAX_EDIT_DISTANCE) return null;
    const row = new Int32Array(2 * d + 1);
    const prevOffset = d - 1;
    for (let k = -d; k <= d; k += 2) {
      let x: number;
      if (k === -d || (k !== d && v[k - 1 + prevOffset] < v[k + 1 + prevOffset])) {
        x = v[k + 1 + prevOffset]; // down: insertion from b
      } else {
        x = v[k - 1 + prevOffset] + 1; // right: deletion from a
      }
      let y = x - k;
      while (x < n && y < m && a[x] === b[y]) {
        x++;
        y++;
      }
      row[k + d] = x;
      if (x >= n && y >= m) {
        foundD = d;
        break;
      }
    }
    trace.push(row);
    v = row;
    if (foundD !== -1) break;
  }

  const ops: Op[] = [];
  let x = n;
  let y = m;
  for (let d = foundD; d > 0; d--) {
    const vPrev = trace[d - 1];
    const prevOffset = d - 1;
    const k = x - y;
    let prevK: number;
    if (k === -d || (k !== d && vPrev[k - 1 + prevOffset] < vPrev[k + 1 + prevOffset])) {
      prevK = k + 1;
    } else {
      prevK = k - 1;
    }
    const prevX = vPrev[prevK + prevOffset];
    const prevY = prevX - prevK;
    while (x > prevX && y > prevY) {
      ops.push({ type: 'equal', a: x - 1, b: y - 1 });
      x--;
      y--;
    }
    if (x > prevX) {
      ops.push({ type: 'delete', a: x - 1 });
      x--;
    } else {
      ops.push({ type: 'insert', b: y - 1 });
      y--;
    }
  }
  while (x > 0 && y > 0) {
    ops.push({ type: 'equal', a: x - 1, b: y - 1 });
    x--;
    y--;
  }
  ops.reverse();
  return ops;
}

function opsToHunks(ops: readonly Op[], pre: number): Hunk[] {
  const hunks: Hunk[] = [];
  let a = pre;
  let b = pre;
  let i = 0;
  while (i < ops.length) {
    if (ops[i].type === 'equal') {
      a++;
      b++;
      i++;
      continue;
    }
    const hunk: Hunk = { aStart: a, aEnd: a, bStart: b, bEnd: b };
    while (i < ops.length && ops[i].type !== 'equal') {
      if (ops[i].type === 'delete') a++;
      else b++;
      i++;
    }
    hunk.aEnd = a;
    hunk.bEnd = b;
    hunks.push(hunk);
  }
  return hunks;
}

/**
 * Slide every hunk down as far as possible (deterministic canonical
 * position) and merge hunks that become adjacent while sliding.
 */
function slideDown(hunks: readonly Hunk[], a: readonly string[], b: readonly string[]): Hunk[] {
  const result: Hunk[] = [];
  let i = 0;
  while (i < hunks.length) {
    const h: Hunk = { ...hunks[i] };
    for (;;) {
      const next = hunks[i + 1];
      if (next && h.aEnd === next.aStart && h.bEnd === next.bStart) {
        h.aEnd = next.aEnd;
        h.bEnd = next.bEnd;
        i++;
        continue;
      }
      const limitA = next ? next.aStart : a.length;
      const limitB = next ? next.bStart : b.length;
      if (h.aEnd >= limitA || h.bEnd >= limitB) break;
      const okA = h.aStart === h.aEnd || a[h.aStart] === a[h.aEnd];
      const okB = h.bStart === h.bEnd || b[h.bStart] === b[h.bEnd];
      if (!okA || !okB) break;
      h.aStart++;
      h.aEnd++;
      h.bStart++;
      h.bEnd++;
    }
    result.push(h);
    i++;
  }
  return result;
}

/**
 * Compute changed hunks between the line contents of `a` and `b`.
 * Hunks are disjoint, ordered, and normalized to their lowest
 * possible position.
 */
export function diffHunks(a: readonly string[], b: readonly string[]): Hunk[] {
  const minLen = Math.min(a.length, b.length);
  let pre = 0;
  while (pre < minLen && a[pre] === b[pre]) pre++;
  let suf = 0;
  while (suf < minLen - pre && a[a.length - 1 - suf] === b[b.length - 1 - suf]) suf++;

  const aMid = a.slice(pre, a.length - suf);
  const bMid = b.slice(pre, b.length - suf);

  let hunks: Hunk[];
  if (aMid.length === 0 && bMid.length === 0) {
    hunks = [];
  } else if (aMid.length === 0) {
    hunks = [{ aStart: pre, aEnd: pre, bStart: pre, bEnd: pre + bMid.length }];
  } else if (bMid.length === 0) {
    hunks = [{ aStart: pre, aEnd: pre + aMid.length, bStart: pre, bEnd: pre }];
  } else {
    const [aIds, bIds] = toIds(aMid, bMid);
    const ops = myersOps(aIds, bIds);
    hunks = ops === null
      ? [{ aStart: pre, aEnd: a.length - suf, bStart: pre, bEnd: b.length - suf }]
      : opsToHunks(ops, pre);
  }
  return slideDown(hunks, a, b);
}
