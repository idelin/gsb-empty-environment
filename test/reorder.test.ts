import { test } from "node:test";
import assert from "node:assert/strict";
import { runs, visualOrder } from "../src/reorder.ts";

/**
 * Independent per-fragment reference implementation.
 *
 * Directly mirrors the nesting structure (UBA embedding-tree placement):
 * the fragment's level-L pieces are listed in logical order when L is even
 * and reversed when L is odd; each maximal sub-fragment at a higher level is
 * arranged recursively before that reversal. The effective level of a
 * sub-fragment entered from level L is treated as L+1.
 *
 * Written independently from the iterative L2 implementation used in
 * production, so agreement across exhaustive inputs is meaningful.
 */
type Segment = { kind: "base" | "sub"; indices: number[] };

function referenceVisualOrder(levels: number[]): number[] {
  const place = (indices: number[], loLevel: number): number[] => {
    const segments: Segment[] = [];
    let i = 0;
    while (i < indices.length) {
      const start = i;
      const insideSub = levels[indices[i]!]! > loLevel;
      i++;
      while (i < indices.length && (levels[indices[i]!]! > loLevel) === insideSub) i++;
      segments.push({ kind: insideSub ? "sub" : "base", indices: indices.slice(start, i) });
    }
    const emitted = segments.map((segment) => {
      if (segment.kind === "sub") return place(segment.indices, loLevel + 1);
      // an odd base run mirrors itself
      return loLevel % 2 === 1 ? [...segment.indices].reverse() : segment.indices;
    });
    if (loLevel % 2 === 1) emitted.reverse();
    return emitted.flat();
  };
  const indices = levels.map((_, i) => i);
  return place(indices, 0);
}

test("single-level sequences: RTL run reverses, LTR stays", () => {
  assert.deepEqual(visualOrder([0, 0, 0]), [0, 1, 2]);
  assert.deepEqual(visualOrder([1, 1, 1]), [2, 1, 0]);
});

test("classic L2 example: LTR with embedded RTL fragment", () => {
  // car THE DOOR is ... => car RO EHT ...
  assert.deepEqual(visualOrder([0, 0, 0, 1, 1, 1, 1, 1, 1, 0]), [0, 1, 2, 8, 7, 6, 5, 4, 3, 9]);
});

test("nested levels: RTL paragraph with LTR embedding", () => {
  assert.deepEqual(visualOrder([1, 1, 2, 2, 2, 1, 1]), [6, 5, 2, 3, 4, 1, 0]);
});

test("runs() groups adjacent equal levels", () => {
  const grouped = runs([0, 0, 1, 1, 0], [..."abcde"]);
  assert.deepEqual(
    grouped.map((r) => ({ level: r.level, items: r.items.join("") })),
    [
      { level: 0, items: "ab" },
      { level: 1, items: "cd" },
      { level: 0, items: "e" },
    ],
  );
});

test("reorder is a permutation (no loss/duplication)", () => {
  const levels = [0, 0, 0, 1, 1, 1, 0, 0, 0];
  const order = visualOrder(levels);
  assert.deepEqual([...order].sort((a, b) => a - b), levels.map((_, i) => i));
});

/**
 * Generate VALID UBA level walks: the paragraph level is 0 (LTR) or 1 (RTL),
 * and a fragment may only step to level-1, stay, or step one level deeper
 * (explicit embedding never skips a level). Sequences like 2,3,2 without a
 * surrounding level-1 fragment are not producible by embedding rules.
 */
function validLevelSequences(maxLen: number, maxLevel: number): number[][] {
  const out: number[][] = [[]];
  const grow = (seq: number[], level: number): void => {
    if (seq.length === maxLen) return;
    const candidates = [level, level + 1, level - 1].filter(
      (l) => l >= 0 && l <= maxLevel,
    );
    for (const next of new Set(candidates)) {
      const child = [...seq, next];
      out.push(child);
      grow(child, next);
    }
  };
  grow([0], 0);
  grow([1], 1);
  return out;
}

test("exhaustive valid level walks (depth<=4, len<=8): L2 agrees with embedding-tree reference", () => {
  const sequences = validLevelSequences(8, 4);
  for (const levels of sequences) {
    assert.deepEqual(visualOrder(levels), referenceVisualOrder(levels), `levels=${levels}`);
  }
  assert.ok(sequences.length > 3000, `only ${sequences.length} sequences`);
});

test("round-trip invariants also hold on arbitrary (even invalid) sequences", () => {
  // The production L2 algorithm stays a well-defined permutation for any input.
  const sequences: number[][] = [
    [0, 1, 2, 1, 0, 1],
    [1, 2, 3, 2, 1, 0],
    [2, 3, 2, 1, 0],
    [3, 3, 0, 2],
  ];
  for (const levels of sequences) {
    const order = visualOrder(levels);
    assert.equal(new Set(order).size, levels.length);
    assert.deepEqual([...order].sort((a, b) => a - b), levels.map((_, i) => i));
  }
});

test("round trip: visual -> logical positions recovers every offset once", () => {
  const sequences: number[][] = [
    [0, 0, 0, 1, 1, 1, 0, 0, 0],
    [1, 1, 2, 2, 2, 1, 1],
    [0, 1, 2, 1, 0, 1],
    [1, 2, 3, 2, 1, 0],
  ];
  for (const levels of sequences) {
    const order = visualOrder(levels);
    const back = new Array<number>(levels.length);
    order.forEach((logicalIndex, visualIndex) => {
      back[logicalIndex] = visualIndex;
    });
    assert.deepEqual([...back].sort((a, b) => a - b), levels.map((_, i) => i));
    // visual position map is bijective
    assert.equal(new Set(order).size, levels.length);
  }
});
