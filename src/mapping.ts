/**
 * Bidirectional line-number mapping and context folding over diff blocks.
 *
 * Lines inside a change block have no counterpart on the other side and map
 * to `null` (deleted A lines map to null in B, inserted B lines map to null
 * in A) — never to a neighbouring line.
 */

import type { DiffBlock } from "./diff.ts";

export class LineMapping {
  readonly blocks: readonly DiffBlock[];
  readonly aLineCount: number;
  readonly bLineCount: number;

  constructor(blocks: readonly DiffBlock[], aLineCount: number, bLineCount: number) {
    this.blocks = blocks;
    this.aLineCount = aLineCount;
    this.bLineCount = bLineCount;
  }

  /** Maps an A line to its B line, or null when it was deleted/changed. */
  aToB(aLine: number): number | null {
    this.#checkLine(aLine, this.aLineCount, "a");
    const block = this.#findBlock(aLine, "a");
    if (block === null) return null;
    if (block.kind === "change") return null;
    return block.bStart + (aLine - block.aStart);
  }

  /** Maps a B line to its A line, or null when it was inserted/changed. */
  bToA(bLine: number): number | null {
    this.#checkLine(bLine, this.bLineCount, "b");
    const block = this.#findBlock(bLine, "b");
    if (block === null) return null;
    if (block.kind === "change") return null;
    return block.aStart + (bLine - block.bStart);
  }

  #checkLine(line: number, count: number, side: string): void {
    if (!Number.isInteger(line) || line < 0 || line >= count) {
      throw new RangeError(`line ${line} is out of range [0, ${count}) on side ${side}`);
    }
  }

  #findBlock(line: number, side: "a" | "b"): DiffBlock | null {
    // Binary search: first block whose end (exclusive) is past `line`.
    // Block ranges partition [0, lineCount), so that block contains the line.
    let lo = 0;
    let hi = this.blocks.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      const end = side === "a" ? this.blocks[mid].aEnd : this.blocks[mid].bEnd;
      if (end > line) hi = mid;
      else lo = mid + 1;
    }
    return lo < this.blocks.length ? this.blocks[lo] : null;
  }
}

export interface FoldRegion {
  /** First hidden A line (inclusive). */
  readonly aStart: number;
  /** Last hidden A line (exclusive). */
  readonly aEnd: number;
  /** First hidden B line (inclusive). */
  readonly bStart: number;
  /** Last hidden B line (exclusive). */
  readonly bEnd: number;
  /** Number of collapsed lines (same on both sides). */
  readonly hiddenLines: number;
}

/**
 * Collapses the middle of every equal block longer than `2 * context`,
 * keeping `context` lines visible on each side of the surrounding changes.
 * Folding is view-only: line mapping stays valid through folded regions.
 */
export function computeFolds(blocks: readonly DiffBlock[], context: number): FoldRegion[] {
  if (!Number.isInteger(context) || context < 0) {
    throw new RangeError(`context must be a non-negative integer, got ${context}`);
  }
  const folds: FoldRegion[] = [];
  for (const block of blocks) {
    if (block.kind !== "equal") continue;
    const length = block.aEnd - block.aStart;
    const hidden = length - 2 * context;
    if (hidden <= 0) continue;
    folds.push({
      aStart: block.aStart + context,
      aEnd: block.aEnd - context,
      bStart: block.bStart + context,
      bEnd: block.bEnd - context,
      hiddenLines: hidden,
    });
  }
  return folds;
}
