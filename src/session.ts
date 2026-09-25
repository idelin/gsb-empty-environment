/**
 * DiffSession keeps the line-diff blocks between two long texts up to date
 * while either side is edited locally.
 */

import { diffLines, lineTexts, type DiffBlock } from "./diff.ts";
import { LineMapping, computeFolds, type FoldRegion } from "./mapping.ts";
import { assertSafeOffset, splitLines, type LineInfo } from "./text.ts";

export type Side = "a" | "b";

export interface Edit {
  /** UTF-16 offset where the edit starts (inclusive). Must not split a surrogate pair. */
  readonly start: number;
  /** UTF-16 offset where the replaced range ends (exclusive). Must not split a surrogate pair. */
  readonly end: number;
  /** Replacement text (may contain LF and/or CRLF line endings). */
  readonly text: string;
}

export class DiffSession {
  #textA: string;
  #textB: string;
  #linesA: LineInfo[];
  #linesB: LineInfo[];
  #blocks: DiffBlock[];
  #mapping: LineMapping;

  constructor(textA: string, textB: string) {
    this.#textA = textA;
    this.#textB = textB;
    this.#linesA = splitLines(textA);
    this.#linesB = splitLines(textB);
    this.#blocks = [];
    this.#mapping = new LineMapping([], 0, 0);
    this.#recompute();
  }

  getText(side: Side): string {
    return side === "a" ? this.#textA : this.#textB;
  }

  getLines(side: Side): readonly LineInfo[] {
    return side === "a" ? this.#linesA : this.#linesB;
  }

  /** All blocks (equal + change), partitioning both documents. */
  getBlocks(): readonly DiffBlock[] {
    return this.#blocks;
  }

  /** Only the differing regions; empty when both texts are identical. */
  getChangeBlocks(): readonly DiffBlock[] {
    return this.#blocks.filter((b) => b.kind === "change");
  }

  /**
   * Applies a local edit to one side and refreshes blocks, mapping and
   * folds. Offsets are UTF-16 code-unit offsets into the current text of
   * that side; an offset that would split a surrogate pair is rejected.
   */
  applyEdit(side: Side, edit: Edit): void {
    const current = side === "a" ? this.#textA : this.#textB;
    assertSafeOffset(current, edit.start);
    assertSafeOffset(current, edit.end);
    if (edit.start > edit.end) {
      throw new RangeError(`edit start ${edit.start} is after end ${edit.end}`);
    }
    const next = current.slice(0, edit.start) + edit.text + current.slice(edit.end);
    if (side === "a") {
      this.#textA = next;
      this.#linesA = splitLines(next);
    } else {
      this.#textB = next;
      this.#linesB = splitLines(next);
    }
    this.#recompute();
  }

  /** Maps an A line to its B line, or null when it has no counterpart. */
  aToB(aLine: number): number | null {
    return this.#mapping.aToB(aLine);
  }

  /** Maps a B line to its A line, or null when it has no counterpart. */
  bToA(bLine: number): number | null {
    return this.#mapping.bToA(bLine);
  }

  /** Foldable unchanged regions, keeping `context` lines around changes. */
  folds(context: number): FoldRegion[] {
    return computeFolds(this.#blocks, context);
  }

  #recompute(): void {
    this.#blocks = diffLines(lineTexts(this.#linesA), lineTexts(this.#linesB));
    this.#mapping = new LineMapping(this.#blocks, this.#linesA.length, this.#linesB.length);
  }
}
