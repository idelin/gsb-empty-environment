/**
 * Bidirectional line/position mapping between the original source and the
 * pruned output.
 *
 * Lines are 1-based, columns 0-based. Only whole lines are ever removed, so
 * columns are preserved unchanged for surviving lines. Queries that land on
 * deleted content return `null` (or an empty array for ranges) — never an
 * approximation.
 */

export interface Position {
  line: number;
  column: number;
}

export interface LineRange {
  startLine: number;
  endLine: number;
}

export class LineMapping {
  /** 1-based original line numbers that survived, ascending. */
  private readonly kept: number[];
  private readonly originalLineCount: number;

  constructor(keptOriginalLines: number[], originalLineCount: number) {
    this.kept = [...keptOriginalLines];
    this.originalLineCount = originalLineCount;
  }

  get generatedLineCount(): number {
    return this.kept.length;
  }

  /** Original (1-based) -> generated (1-based), or null when the line was deleted. */
  toGeneratedLine(originalLine: number): number | null {
    let lo = 0;
    let hi = this.kept.length - 1;
    while (lo <= hi) {
      const mid = (lo + hi) >> 1;
      const value = this.kept[mid];
      if (value === originalLine) return mid + 1;
      if (value < originalLine) lo = mid + 1;
      else hi = mid - 1;
    }
    return null;
  }

  /** Generated (1-based) -> original (1-based), or null when out of range. */
  toOriginalLine(generatedLine: number): number | null {
    if (!Number.isInteger(generatedLine) || generatedLine < 1 || generatedLine > this.kept.length) {
      return null;
    }
    return this.kept[generatedLine - 1];
  }

  isDeleted(originalLine: number): boolean {
    return this.toGeneratedLine(originalLine) === null;
  }

  /** Maps an original position; null when the position was deleted. */
  toGeneratedPosition(pos: Position): Position | null {
    const line = this.toGeneratedLine(pos.line);
    return line === null ? null : { line, column: pos.column };
  }

  /** Maps a generated position back; null when out of range. */
  toOriginalPosition(pos: Position): Position | null {
    const line = this.toOriginalLine(pos.line);
    return line === null ? null : { line, column: pos.column };
  }

  /**
   * Maps an original line range to the generated ranges covering its
   * surviving lines. Returns [] when the whole range was deleted.
   */
  toGeneratedRange(startLine: number, endLine: number): LineRange[] {
    const ranges: LineRange[] = [];
    for (let idx = 0; idx < this.kept.length; idx++) {
      const original = this.kept[idx];
      if (original < startLine || original > endLine) continue;
      const generated = idx + 1;
      const last = ranges[ranges.length - 1];
      if (last !== undefined && generated === last.endLine + 1) {
        last.endLine = generated;
      } else {
        ranges.push({ startLine: generated, endLine: generated });
      }
    }
    return ranges;
  }

  /** Deleted line ranges in original coordinates. */
  deletedRanges(): LineRange[] {
    const ranges: LineRange[] = [];
    let previous = 0;
    for (const original of this.kept) {
      if (original > previous + 1) {
        ranges.push({ startLine: previous + 1, endLine: original - 1 });
      }
      previous = original;
    }
    if (previous < this.originalLineCount) {
      ranges.push({ startLine: previous + 1, endLine: this.originalLineCount });
    }
    return ranges;
  }

  toJSON(): Record<string, unknown> {
    return {
      originalLineCount: this.originalLineCount,
      generatedLineCount: this.kept.length,
      keptOriginalLines: [...this.kept],
      deletedRanges: this.deletedRanges(),
    };
  }
}
