/**
 * Monospaced terminal-style layout over an editable `PieceTree`.
 *
 * - Tabs expand to the next tab stop (`tabWidth`).
 * - Wide clusters occupy two columns, zero-width clusters none.
 * - Soft wrap breaks lines at `wrapColumn` between grapheme clusters;
 *   clusters (incl. combining sequences) are never split.
 * - Logical offsets are UTF-16 code-unit offsets; surrogate pairs are
 *   never split.
 * - Where one visual position maps to several logical offsets (inside a
 *   wide cluster, at a soft-wrap boundary) the API returns one candidate
 *   plus a directional affinity instead of faking a bijection.
 * - Layout is measured lazily per logical line and cached; edits
 *   invalidate only the affected lines, so a height change in one line
 *   never re-measures unrelated lines.
 */

import { GraphemeMeasurer } from './measurer.ts';
import type { Measurer } from './measurer.ts';
import { PieceTree, isMidSurrogatePair } from './pieceTree.ts';
import type { EditResult } from './pieceTree.ts';

/** Visual position: logical line, soft-wrap row within the line, column. */
export interface VisualPosition {
  line: number;
  row: number;
  column: number;
}

/**
 * Hit policy for clicks landing strictly inside a wide cluster:
 * - `'start'` (the specified "forward" policy): snap to the cluster start;
 * - `'end'`: snap to the cluster end.
 */
export type HitPolicy = 'start' | 'end';

/** Direction preference used to disambiguate wrap-boundary positions. */
export type DirectionPreference = 'forward' | 'backward';

export interface HitResult {
  /** UTF-16 document offset (never inside a surrogate pair). */
  offset: number;
  /**
   * Caret affinity at the returned offset: `'forward'` binds the caret to
   * the cluster after the offset (leading edge), `'backward'` to the text
   * before it (trailing edge / soft-wrap row end).
   */
  affinity: DirectionPreference;
}

/** A laid-out cluster; offsets are relative to the logical line start. */
export interface LaidCluster {
  offset: number;
  length: number;
  column: number;
  width: number;
}

/** One visual row produced by soft-wrapping a logical line. */
export interface RowLayout {
  startOffset: number;
  endOffset: number;
  width: number;
  clusters: LaidCluster[];
}

export interface LineLayout {
  rows: RowLayout[];
}

/** A visual row with absolute (document) UTF-16 offsets. */
export interface AbsoluteRow {
  line: number;
  row: number;
  startOffset: number;
  endOffset: number;
  width: number;
  clusters: LaidCluster[];
}

export interface LayoutOptions {
  text?: string;
  measurer?: Measurer;
  tabWidth?: number;
  /** Soft-wrap column; omit or <= 0 to disable wrapping. */
  wrapColumn?: number;
}

function clamp(value: number, min: number, max: number): number {
  return Math.max(min, Math.min(value, max));
}

export class LayoutEngine {
  readonly tree: PieceTree;
  private readonly measurer: Measurer;
  private readonly tabWidth: number;
  private readonly wrapColumn: number;
  /** Per-line layout cache; entries hold line-relative offsets so pure
   *  vertical shifts after an edit never invalidate unrelated lines. */
  private cache: (LineLayout | undefined)[];

  constructor(options: LayoutOptions = {}) {
    this.tree = new PieceTree(options.text ?? '');
    this.measurer = options.measurer ?? new GraphemeMeasurer();
    this.tabWidth = options.tabWidth ?? 4;
    this.wrapColumn =
      options.wrapColumn && options.wrapColumn > 0
        ? options.wrapColumn
        : Number.POSITIVE_INFINITY;
    this.cache = new Array(this.tree.lineCount).fill(undefined);
  }

  get lineCount(): number {
    return this.tree.lineCount;
  }

  getText(): string {
    return this.tree.getText();
  }

  // ---------------------------------------------------------------- edits

  insert(offset: number, text: string): void {
    this.applyEdit(this.tree.insert(offset, text));
  }

  delete(offset: number, length: number): void {
    this.applyEdit(this.tree.delete(offset, length));
  }

  private applyEdit(result: EditResult): void {
    if (result.oldLineCount === 0 && result.newLineCount === 0) {
      return;
    }
    this.cache.splice(
      result.startLine,
      result.oldLineCount,
      ...new Array<undefined>(result.newLineCount).fill(undefined),
    );
  }

  // ------------------------------------------------------------- measuring

  /** Number of visual rows (height) of a logical line. */
  lineHeight(line: number): number {
    return this.layoutLine(line).rows.length;
  }

  /** Line-relative layout of one logical line (cached, lazily measured). */
  layoutLine(line: number): LineLayout {
    const cached = this.cache[line];
    if (cached !== undefined) {
      return cached;
    }
    const content = this.tree.lineContent(line);
    const clusters = this.measurer.measure(content);
    const rows: RowLayout[] = [];
    let current: LaidCluster[] = [];
    let column = 0;
    let rowStart = 0;
    const pushRow = (endOffset: number): void => {
      rows.push({ startOffset: rowStart, endOffset, width: column, clusters: current });
    };
    for (const c of clusters) {
      const isTab = content.charCodeAt(c.offset) === 0x09;
      let width = isTab ? this.tabWidth - (column % this.tabWidth) : c.width;
      if (width > 0 && column > 0 && column + width > this.wrapColumn) {
        // Soft-wrap before this cluster; the cluster itself stays intact.
        pushRow(c.offset);
        rowStart = c.offset;
        current = [];
        column = 0;
        if (isTab) {
          width = this.tabWidth;
        }
      }
      current.push({ offset: c.offset, length: c.length, column, width });
      column += width;
    }
    pushRow(content.length);
    const layout: LineLayout = { rows };
    this.cache[line] = layout;
    return layout;
  }

  /** All visual rows of the document with absolute UTF-16 offsets. */
  allRows(): AbsoluteRow[] {
    const out: AbsoluteRow[] = [];
    for (let line = 0; line < this.lineCount; line++) {
      const base = this.tree.lineStart(line);
      const layout = this.layoutLine(line);
      layout.rows.forEach((row, r) => {
        out.push({
          line,
          row: r,
          startOffset: base + row.startOffset,
          endOffset: base + row.endOffset,
          width: row.width,
          clusters: row.clusters.map((c) => ({
            offset: base + c.offset,
            length: c.length,
            column: c.column,
            width: c.width,
          })),
        });
      });
    }
    return out;
  }

  // ----------------------------------------------------------- conversions

  /**
   * Logical UTF-16 offset -> visual position. Mid-surrogate offsets snap
   * to the pair start. At a soft-wrap boundary the offset has two visual
   * candidates; `preference` picks the row (`forward` = next row start,
   * `backward` = previous row end).
   */
  logicalToVisual(
    offset: number,
    preference: DirectionPreference = 'forward',
  ): VisualPosition {
    const text = this.tree.getText();
    let off = clamp(offset, 0, text.length);
    if (isMidSurrogatePair(text, off)) {
      off -= 1;
    }
    const line = this.tree.lineAt(off);
    const layout = this.layoutLine(line);
    const rel = off - this.tree.lineStart(line);
    const rows = layout.rows;
    for (let r = 0; r < rows.length; r++) {
      const row = rows[r];
      const isLast = r === rows.length - 1;
      if (rel <= row.endOffset || isLast) {
        if (!isLast && rel === row.endOffset) {
          return preference === 'forward'
            ? { line, row: r + 1, column: 0 }
            : { line, row: r, column: row.width };
        }
        for (const c of row.clusters) {
          if (rel <= c.offset) {
            return { line, row: r, column: c.column };
          }
          if (rel < c.offset + c.length) {
            // Mid-cluster offset: snap to the cluster start column.
            return { line, row: r, column: c.column };
          }
        }
        return { line, row: r, column: row.width };
      }
    }
    const last = rows[rows.length - 1];
    return { line, row: rows.length - 1, column: last.width };
  }

  /**
   * Visual position -> logical UTF-16 offset. When several logical
   * candidates exist (click inside a wide cluster, past the row end, at
   * a wrap boundary) one candidate is returned together with a
   * directional affinity — no fake bijection.
   */
  visualToLogical(position: VisualPosition, policy: HitPolicy = 'start'): HitResult {
    const line = clamp(position.line, 0, this.lineCount - 1);
    const layout = this.layoutLine(line);
    const row = layout.rows[clamp(position.row, 0, layout.rows.length - 1)];
    const x = Math.max(0, position.column);
    const lineStart = this.tree.lineStart(line);
    for (const c of row.clusters) {
      if (x <= c.column) {
        return { offset: lineStart + c.offset, affinity: 'forward' };
      }
      if (x < c.column + c.width) {
        // Strictly inside a wide cluster: two logical candidates.
        return policy === 'start'
          ? { offset: lineStart + c.offset, affinity: 'forward' }
          : { offset: lineStart + c.offset + c.length, affinity: 'backward' };
      }
    }
    // At / past the row end (also the wrap boundary for non-last rows).
    return { offset: lineStart + row.endOffset, affinity: 'backward' };
  }

  // -------------------------------------------------------------- snapshot

  /**
   * Immutable snapshot of the current layout. After later edits the
   * snapshot still answers hit tests against the old layout.
   */
  snapshot(): LayoutSnapshot {
    return new LayoutSnapshot(this.tree.getText(), {
      measurer: this.measurer,
      tabWidth: this.tabWidth,
      wrapColumn: this.wrapColumn,
    });
  }
}

/** Read-only view of a past layout state (old-layout hit testing). */
export class LayoutSnapshot {
  private readonly engine: LayoutEngine;

  constructor(text: string, options: Omit<LayoutOptions, 'text'> = {}) {
    this.engine = new LayoutEngine({ ...options, text });
  }

  get text(): string {
    return this.engine.getText();
  }

  get lineCount(): number {
    return this.engine.lineCount;
  }

  lineHeight(line: number): number {
    return this.engine.lineHeight(line);
  }

  allRows(): AbsoluteRow[] {
    return this.engine.allRows();
  }

  logicalToVisual(offset: number, preference: DirectionPreference = 'forward'): VisualPosition {
    return this.engine.logicalToVisual(offset, preference);
  }

  visualToLogical(position: VisualPosition, policy: HitPolicy = 'start'): HitResult {
    return this.engine.visualToLogical(position, policy);
  }
}
