/**
 * Line-level diff based on the Myers O(ND) algorithm.
 *
 * Determinism / tie-breaking: the greedy walk and the backtrack both resolve
 * ties with a single fixed rule — when a deletion and an insertion are
 * equally good, the deletion is taken first. Combined with common
 * prefix/suffix trimming this means repeated lines (including runs of
 * identical blank lines) always align their earliest occurrences, and the
 * same input pair always yields the exact same blocks.
 */

export type BlockKind = "equal" | "change";

export interface DiffBlock {
  readonly kind: BlockKind;
  /** Start line in A (0-based, inclusive). */
  readonly aStart: number;
  /** End line in A (exclusive). */
  readonly aEnd: number;
  /** Start line in B (0-based, inclusive). */
  readonly bStart: number;
  /** End line in B (exclusive). */
  readonly bEnd: number;
}

type OpType = "eq" | "del" | "ins";

interface Op {
  readonly type: OpType;
}

function myersOps(
  a: readonly string[],
  aLo: number,
  aHi: number,
  b: readonly string[],
  bLo: number,
  bHi: number,
): Op[] {
  const n = aHi - aLo;
  const m = bHi - bLo;
  const ops: Op[] = [];
  if (n === 0 && m === 0) return ops;
  if (n === 0) {
    for (let i = 0; i < m; i++) ops.push({ type: "ins" });
    return ops;
  }
  if (m === 0) {
    for (let i = 0; i < n; i++) ops.push({ type: "del" });
    return ops;
  }

  const max = n + m;
  const offset = max;
  const width = 2 * max + 1;
  let v = new Array<number>(width).fill(0);
  // trace[d] = V after processing distance d (needed by the backtrack).
  const trace: number[][] = [];
  let distance = -1;

  outer: for (let d = 0; d <= max; d++) {
    for (let k = -d; k <= d; k += 2) {
      // Tie-break: on equality prefer the deletion (right move).
      let x: number;
      if (k === -d || (k !== d && v[k - 1 + offset] < v[k + 1 + offset])) {
        x = v[k + 1 + offset]; // down move: insertion from B
      } else {
        x = v[k - 1 + offset] + 1; // right move: deletion from A
      }
      let y = x - k;
      while (x < n && y < m && a[aLo + x] === b[bLo + y]) {
        x++;
        y++;
      }
      v[k + offset] = x;
      if (x >= n && y >= m) {
        distance = d;
        trace.push(v.slice());
        break outer;
      }
    }
    if (distance === -1) trace.push(v.slice());
  }

  // Backtrack from (n, m) to (0, 0).
  const rev: Op[] = [];
  let x = n;
  let y = m;
  for (let d = distance; d > 0; d--) {
    const vPrev = trace[d - 1];
    const k = x - y;
    // Must mirror the forward tie-break exactly.
    const prevK =
      k === -d || (k !== d && vPrev[k - 1 + offset] < vPrev[k + 1 + offset])
        ? k + 1
        : k - 1;
    const prevX = vPrev[prevK + offset];
    const prevY = prevX - prevK;
    while (x > prevX && y > prevY) {
      rev.push({ type: "eq" });
      x--;
      y--;
    }
    if (prevK === k + 1) {
      rev.push({ type: "ins" });
      y--;
    } else {
      rev.push({ type: "del" });
      x--;
    }
  }
  while (x > 0 && y > 0) {
    rev.push({ type: "eq" });
    x--;
    y--;
  }
  rev.reverse();
  return rev;
}

/**
 * Computes the block partition of two line arrays. Equal blocks satisfy
 * `aEnd - aStart === bEnd - bStart`; change blocks cover deleted A lines
 * and/or inserted B lines. Identical inputs always produce identical output.
 */
export function diffLines(a: readonly string[], b: readonly string[]): DiffBlock[] {
  const n = a.length;
  const m = b.length;

  // Trim common prefix/suffix so local edits only re-diff the middle.
  let p = 0;
  while (p < n && p < m && a[p] === b[p]) p++;
  let aEnd = n;
  let bEnd = m;
  while (aEnd > p && bEnd > p && a[aEnd - 1] === b[bEnd - 1]) {
    aEnd--;
    bEnd--;
  }

  const blocks: DiffBlock[] = [];
  if (p > 0) {
    blocks.push({ kind: "equal", aStart: 0, aEnd: p, bStart: 0, bEnd: p });
  }

  const ops = myersOps(a, p, aEnd, b, p, bEnd);
  let aPos = p;
  let bPos = p;
  let i = 0;
  while (i < ops.length) {
    if (ops[i].type === "eq") {
      const aS = aPos;
      const bS = bPos;
      while (i < ops.length && ops[i].type === "eq") {
        i++;
        aPos++;
        bPos++;
      }
      blocks.push({ kind: "equal", aStart: aS, aEnd: aPos, bStart: bS, bEnd: bPos });
    } else {
      const aS = aPos;
      const bS = bPos;
      while (i < ops.length && ops[i].type !== "eq") {
        if (ops[i].type === "del") aPos++;
        else bPos++;
        i++;
      }
      blocks.push({ kind: "change", aStart: aS, aEnd: aPos, bStart: bS, bEnd: bPos });
    }
  }

  if (aEnd < n) {
    blocks.push({ kind: "equal", aStart: aEnd, aEnd: n, bStart: bEnd, bEnd: m });
  }
  return blocks;
}

/** Extracts the content of each line for diffing (terminators ignored). */
export function lineTexts(lines: readonly { text: string }[]): string[] {
  return lines.map((l) => l.text);
}
