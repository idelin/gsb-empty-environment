import { createDefaultMeasurer } from './measurer.ts';
import type { Measurer } from './measurer.ts';
import {
  PieceTable,
  isHighSurrogate,
  isLowSurrogate,
} from './pieceTable.ts';

/* ------------------------------------------------------------------ */
/* Types                                                               */
/* ------------------------------------------------------------------ */

/**
 * Hit-test boundary policy.
 * - `forward`  : prefer the candidate toward the document start
 *                (smaller UTF-16 offset).
 * - `backward` : prefer the candidate toward the document end
 *                (larger UTF-16 offset).
 * - `nearest`  : snap to the visually closest boundary.
 */
export type HitPolicy = 'forward' | 'backward' | 'nearest';

/**
 * Caret affinity at an ambiguous offset.
 * - `downstream` : caret sits at the START of a fragment
 *                  (belongs to what follows).
 * - `upstream`   : caret sits at the END of a fragment
 *                  (belongs to what precedes).
 */
export type Affinity = 'upstream' | 'downstream';

/** A leaf of the editable fragment tree. Offsets are relative to its line. */
export interface Fragment {
  /** `tab` fragments are the expanded-tab stops; `cluster` is a grapheme. */
  readonly kind: 'cluster' | 'tab';
  /** UTF-16 offset relative to the owning line's start. */
  readonly start: number;
  /** UTF-16 length (tab fragments are length 1). */
  readonly length: number;
  /** Visual column inside the row. */
  readonly col: number;
  /** Columns occupied (0 for a zero-width combining cluster). */
  readonly width: number;
}

export interface RowLayout {
  readonly fragments: readonly Fragment[];
  /** A soft-wrap break follows this row (it has no corresponding text). */
  readonly softBreakAfter: boolean;
}

export interface LineLayout {
  readonly index: number;
  /** Absolute UTF-16 offset of the line start in the document. */
  readonly start: number;
  /** Content length in UTF-16 units, excluding the terminator. */
  readonly contentLength: number;
  /** 0 (last line), 1 (`\n` or lone `\r`) or 2 (`\r\n`). */
  readonly terminatorLength: number;
  readonly rows: readonly RowLayout[];
  readonly height: number;
}

export interface VisualPosition {
  readonly line: number;
  readonly row: number;
  readonly column: number;
  readonly affinity: Affinity;
}

export interface HitTestResult extends VisualPosition {
  /** Absolute UTF-16 offset; never the middle of a surrogate pair. */
  readonly offset: number;
}

export interface LayoutOptions {
  measurer?: Measurer;
  tabWidth?: number;
  /** Maximum columns before soft wrapping. Infinity = no wrap. */
  wrapColumn?: number;
}

interface ResolvedOptions {
  measurer: Measurer;
  tabWidth: number;
  wrapColumn: number;
}

/* ------------------------------------------------------------------ */
/* Line scanning (LF, CRLF and lone CR)                               */
/* ------------------------------------------------------------------ */

function scanLine(text: string, start: number): {
  contentLength: number;
  terminatorLength: number;
} {
  let i = start;
  while (i < text.length) {
    const c = text.charCodeAt(i);
    if (c === 10) return { contentLength: i - start, terminatorLength: 1 };
    if (c === 13) {
      const terminatorLength = text.charCodeAt(i + 1) === 10 ? 2 : 1;
      return { contentLength: i - start, terminatorLength };
    }
    i += 1;
  }
  return { contentLength: i - start, terminatorLength: 0 };
}

function resolveOptions(options: LayoutOptions = {}): ResolvedOptions {
  return {
    measurer: options.measurer ?? createDefaultMeasurer(),
    tabWidth: options.tabWidth ?? 4,
    wrapColumn: options.wrapColumn ?? Infinity,
  };
}

/* ------------------------------------------------------------------ */
/* Fragment-tree construction for one logical line                    */
/* ------------------------------------------------------------------ */

function buildLine(
  index: number,
  start: number,
  content: string,
  terminatorLength: number,
  options: ResolvedOptions,
): LineLayout {
  const rows: RowLayout[] = [];
  let fragments: Fragment[] = [];
  let col = 0;

  const finishRow = (softBreakAfter: boolean): void => {
    rows.push({ fragments, softBreakAfter });
    fragments = [];
    col = 0;
  };

  const wrapIfNeeded = (width: number): void => {
    if (width > 0 && col > 0 && col + width > options.wrapColumn) {
      finishRow(true);
    }
  };

  let i = 0;
  while (i < content.length) {
    if (content.charCodeAt(i) === 9) {
      // Tab: advance to the next tab stop *within this row*.
      let width = options.tabWidth - (col % options.tabWidth);
      wrapIfNeeded(width);
      if (col === 0) width = options.tabWidth;
      fragments.push({ kind: 'tab', start: i, length: 1, col, width });
      col += width;
      i += 1;
    } else {
      let j = i;
      while (j < content.length && content.charCodeAt(j) !== 9) j += 1;
      // The caller-supplied measurer owns cluster boundaries and widths.
      const metrics = options.measurer.measure(content.slice(i, j));
      let rel = i;
      for (const metric of metrics) {
        // Clusters are atomic: a wide cluster moves to the next row
        // rather than straddling the wrap column; zero-width marks wrap
        // together with the preceding cluster.
        wrapIfNeeded(metric.width);
        fragments.push({
          kind: 'cluster',
          start: rel,
          length: metric.length,
          col,
          width: metric.width,
        });
        col += metric.width;
        rel += metric.length;
      }
      i = j;
    }
  }
  finishRow(false);

  return {
    index,
    start,
    contentLength: content.length,
    terminatorLength,
    rows,
    height: rows.length,
  };
}

export function rowWidth(row: RowLayout): number {
  const last = row.fragments[row.fragments.length - 1];
  return last ? last.col + last.width : 0;
}

/* ------------------------------------------------------------------ */
/* Full layout (also the reference implementation for fuzz testing)   */
/* ------------------------------------------------------------------ */

function layoutAllLines(
  text: string,
  options: ResolvedOptions,
): LineLayout[] {
  const lines: LineLayout[] = [];
  let start = 0;
  let index = 0;
  while (start <= text.length) {
    const { contentLength, terminatorLength } = scanLine(text, start);
    lines.push(
      buildLine(index, start, text.slice(start, start + contentLength), terminatorLength, options),
    );
    start += contentLength + terminatorLength;
    index += 1;
    if (terminatorLength === 0) break;
  }
  return lines;
}

export function computeFullLayout(
  text: string,
  options: LayoutOptions = {},
): LayoutSnapshot {
  const resolved = resolveOptions(options);
  return new LayoutSnapshot(layoutAllLines(text, resolved), text, resolved);
}

/* ------------------------------------------------------------------ */
/* Immutable snapshot: bidirectional conversion + hit testing         */
/* ------------------------------------------------------------------ */

function lineIndexAtOffset(lines: readonly LineLayout[], offset: number): number {
  let lo = 0;
  let hi = lines.length - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (lines[mid].start <= offset) lo = mid;
    else hi = mid - 1;
  }
  return lo;
}

function splitsSurrogatePair(text: string, offset: number): boolean {
  return (
    offset > 0 &&
    offset < text.length &&
    isHighSurrogate(text.charCodeAt(offset - 1)) &&
    isLowSurrogate(text.charCodeAt(offset))
  );
}

export class LayoutSnapshot {
  /** Total visual rows across all lines (including soft-wrapped rows). */
  readonly height: number;
  readonly lines: readonly LineLayout[];
  readonly text: string;
  readonly options: ResolvedOptions;

  constructor(
    lines: readonly LineLayout[],
    text: string,
    options: ResolvedOptions,
  ) {
    this.lines = lines;
    this.text = text;
    this.options = options;
    this.height = lines.reduce((sum, line) => sum + line.height, 0);
  }

  /** Absolute visual row of the first row of a logical line. */
  firstVisualRow(lineIndex: number): number {
    let row = 0;
    for (let i = 0; i < lineIndex && i < this.lines.length; i += 1) {
      row += this.lines[i].height;
    }
    return row;
  }

  /**
   * Logical UTF-16 offset -> visual (line, row, column).
   * Offsets inside an atomic cluster snap to that cluster's start;
   * offsets inside a surrogate pair snap to the pair start.
   */
  positionAt(offset: number, affinity: Affinity = 'downstream'): VisualPosition {
    const clamped = Math.max(0, Math.min(offset, this.text.length));
    const safe = splitsSurrogatePair(this.text, clamped) ? clamped - 1 : clamped;
    const lineIndex = lineIndexAtOffset(this.lines, safe);
    const line = this.lines[lineIndex];
    const rel = safe - line.start;

    // On (or past) the terminator: end of the final row.
    if (rel >= line.contentLength) {
      const rowIndex = line.rows.length - 1;
      return {
        line: lineIndex,
        row: rowIndex,
        column: rowWidth(line.rows[rowIndex]),
        affinity: 'upstream',
      };
    }

    for (let r = 0; r < line.rows.length; r += 1) {
      const fragments = line.rows[r].fragments;
      for (let f = 0; f < fragments.length; f += 1) {
        const fragment = fragments[f];
        if (rel < fragment.start) {
          return { line: lineIndex, row: r, column: fragment.col, affinity: 'downstream' };
        }
        if (rel === fragment.start) {
          // Soft-wrap boundary: upstream belongs to the previous row.
          if (affinity === 'upstream' && f === 0 && r > 0) {
            return {
              line: lineIndex,
              row: r - 1,
              column: rowWidth(line.rows[r - 1]),
              affinity: 'upstream',
            };
          }
          return { line: lineIndex, row: r, column: fragment.col, affinity: 'downstream' };
        }
        if (rel < fragment.start + fragment.length) {
          // Inside an atomic cluster (e.g. between base and combining
          // mark): snap to the cluster start, never split it.
          return { line: lineIndex, row: r, column: fragment.col, affinity: 'downstream' };
        }
        const lastFragment = f === fragments.length - 1 && r === line.rows.length - 1;
        if (rel === fragment.start + fragment.length) {
          if (affinity === 'upstream' || lastFragment) {
            return {
              line: lineIndex,
              row: r,
              column: fragment.col + fragment.width,
              affinity: 'upstream',
            };
          }
          // Otherwise the boundary belongs to the following fragment
          // (possibly the first fragment of a soft-wrapped next row).
        }
      }
    }

    const lastRow = line.rows.length - 1;
    return {
      line: lineIndex,
      row: lastRow,
      column: rowWidth(line.rows[lastRow]),
      affinity: 'upstream',
    };
  }

  /**
   * Visual (line, row, column) -> logical offset with an explicit
   * boundary policy. When one column has several logical candidates
   * (wide-char interior, zero-width marks, soft-wrap point) the result
   * carries the chosen candidate and its caret affinity instead of
   * pretending the mapping is a bijection.
   */
  hitTest(
    lineIndexInput: number,
    rowIndexInput: number,
    columnInput: number,
    policy: HitPolicy = 'nearest',
  ): HitTestResult {
    const lineIndex = Math.max(0, Math.min(lineIndexInput, this.lines.length - 1));
    const line = this.lines[lineIndex];
    const rowIndex = Math.max(0, Math.min(rowIndexInput, line.rows.length - 1));
    const row = line.rows[rowIndex];
    const column = Math.max(0, Math.floor(columnInput));
    const fragments = row.fragments;

    const result = (relOffset: number, affinity: Affinity): HitTestResult => {
      let abs = line.start + relOffset;
      // Final safety net for caller-supplied measurers: never hand back
      // an offset in the middle of a surrogate pair.
      if (splitsSurrogatePair(this.text, abs)) abs -= 1;
      return { line: lineIndex, row: rowIndex, column, offset: abs, affinity };
    };

    if (fragments.length === 0) return result(0, 'downstream');

    const width = rowWidth(row);
    if (column >= width) {
      const last = fragments[fragments.length - 1];
      return result(last.start + last.length, 'upstream');
    }

    for (let f = 0; f < fragments.length; f += 1) {
      const fragment = fragments[f];
      if (fragment.width === 0) continue;
      if (column < fragment.col) break;
      if (column >= fragment.col + fragment.width) continue;

      if (column === fragment.col) {
        // Exact left edge: candidates are the end of the fragment chain
        // closing here (including zero-width marks sharing the column)
        // and the start of this fragment.
        if (f === 0) return result(fragment.start, 'downstream');
        if (policy === 'forward') {
          let k = f - 1;
          while (k > 0 && fragments[k - 1].col + fragments[k - 1].width === fragment.col) {
            k -= 1;
          }
          const left = fragments[k];
          return result(left.start + left.length, 'upstream');
        }
        // `backward` and `nearest` put the caret just before the glyph.
        return result(fragment.start, 'downstream');
      }

      // Strictly inside a fragment (e.g. second column of a wide char).
      if (policy === 'forward') return result(fragment.start, 'downstream');
      if (policy === 'backward') return result(fragment.start + fragment.length, 'upstream');
      const leftDist = column - fragment.col;
      const rightDist = fragment.col + fragment.width - column;
      return leftDist < rightDist
        ? result(fragment.start, 'downstream')
        : result(fragment.start + fragment.length, 'upstream');
    }

    const last = fragments[fragments.length - 1];
    return result(last.start + last.length, 'upstream');
  }
}

/* ------------------------------------------------------------------ */
/* Editable engine: piece tree + partial measurement updates          */
/* ------------------------------------------------------------------ */

/**
 * LayoutEngine owns the mutable piece table and cached per-line
 * layouts. An edit only re-measures the logical lines it actually
 * touches; untouched lines keep their fragment rows (and therefore
 * their measurements), even when heights shift the rows below.
 * Old snapshots stay usable for hit testing after later edits.
 */
export class LayoutEngine {
  private buffer: PieceTable;
  private readonly options: ResolvedOptions;
  private lines: LineLayout[] = [];

  constructor(text = '', options: LayoutOptions = {}) {
    this.options = resolveOptions(options);
    this.buffer = new PieceTable(text);
    this.lines = layoutAllLines(text, this.options);
    this.measuredLines = this.lines.map((line) => line.index);
  }

  /** Logical line indexes re-measured by the last edit (or all initially). */
  measuredLines: readonly number[];

  get text(): string {
    return this.buffer.getText();
  }

  get currentLines(): readonly LineLayout[] {
    return this.lines;
  }

  /** Immutable snapshot; safe to retain and hit-test after later edits. */
  snapshot(): LayoutSnapshot {
    return new LayoutSnapshot(this.lines.slice(), this.buffer.getText(), this.options);
  }

  applyEdit(start: number, deleteCount: number, inserted: string): void {
    const edit = this.buffer.applyEdit(start, deleteCount, inserted);
    if (edit.deleteCount === 0 && edit.text.length === 0) {
      this.measuredLines = [];
      return;
    }
    const text = this.buffer.getText();
    const delta = edit.text.length - edit.deleteCount;
    const s = edit.start;
    const e = edit.start + edit.deleteCount;
    const lines = this.lines;

    let startLine = lineIndexAtOffset(lines, s);
    // A `\r` immediately before the edit can merge with a following
    // `\n` (inserted or shifted) into a single CRLF terminator, which
    // changes the previous line's layout too.
    if (s > 0 && text.charCodeAt(s - 1) === 13) {
      startLine = lineIndexAtOffset(lines, s - 1);
    }

    // `followIndex` = first old line that survives the edit intact. A
    // line survives only if the terminator *preceding* it was not
    // consumed by the edit; otherwise it merges into the rebuilt range.
    let followIndex: number;
    if (
      edit.deleteCount === 0 &&
      lines[startLine].start === s &&
      /[\r\n]$/.test(edit.text)
    ) {
      // Pure insertion of whole lines at a line start: the line itself
      // is reusable, only the inserted lines are new.
      followIndex = startLine;
    } else {
      followIndex = startLine + 1;
      while (
        followIndex < lines.length &&
        e > lines[followIndex].start - lines[followIndex - 1].terminatorLength
      ) {
        followIndex += 1;
      }
    }

    const scanStart = lines[startLine].start;
    const followStart = followIndex < lines.length ? lines[followIndex].start + delta : -1;

    // Re-scan and re-measure exactly the lines covered by the edit.
    const rebuilt: LineLayout[] = [];
    let lineStart = scanStart;
    let index = startLine;
    while (true) {
      if (lineStart === followStart) break;
      const { contentLength, terminatorLength } = scanLine(text, lineStart);
      rebuilt.push(
        buildLine(
          index,
          lineStart,
          text.slice(lineStart, lineStart + contentLength),
          terminatorLength,
          this.options,
        ),
      );
      index += 1;
      lineStart += contentLength + terminatorLength;
      if (terminatorLength === 0) break;
    }

    // Surviving lines keep their measured rows (shared by reference);
    // only index and absolute start shift. New objects are created so
    // old snapshots stay valid for hit testing.
    const tail: LineLayout[] = lines.slice(followIndex).map((line, k) => ({
      ...line,
      index: index + k,
      start: line.start + delta,
    }));

    this.lines = [...lines.slice(0, startLine), ...rebuilt, ...tail];
    this.measuredLines = rebuilt.map((line) => line.index);
  }
}
