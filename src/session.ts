import { splitLines, type Line } from './lines.ts';
import { diffHunks } from './diff.ts';
import { clampOffset, snapOffsetBackward, snapOffsetForward } from './utf16.ts';

export type Side = 'left' | 'right';

export type BlockKind = 'insert' | 'delete' | 'replace';

export interface LineRange {
  /** 0-based first line, inclusive. */
  start: number;
  /** 0-based last line, exclusive. */
  end: number;
}

export interface DiffBlock {
  readonly kind: BlockKind;
  readonly left: LineRange;
  readonly right: LineRange;
}

export type ViewItem =
  | { readonly type: 'change'; readonly block: DiffBlock }
  | { readonly type: 'equal'; readonly left: LineRange; readonly right: LineRange }
  | {
      readonly type: 'fold';
      readonly left: LineRange;
      readonly right: LineRange;
      /** Number of collapsed equal lines (same on both sides). */
      readonly lineCount: number;
    };

export interface Position {
  readonly line: number;
  /** Column in UTF-16 code units within the line content. */
  readonly col: number;
}

export interface AppliedEdit {
  /** Adjusted UTF-16 offsets actually replaced (surrogate-safe). */
  readonly start: number;
  readonly end: number;
}

function sideIndex(side: Side): 0 | 1 {
  return side === 'left' ? 0 : 1;
}

function otherIndex(side: Side): 0 | 1 {
  return side === 'left' ? 1 : 0;
}

/**
 * Maintains line diff blocks between two texts. Either side can be
 * edited locally through `applyEdit`; blocks, folding and mappings
 * are recomputed from the new state, so reverting an edit makes the
 * blocks disappear again.
 */
export class LineDiffSession {
  private texts: [string, string];
  private lineTables: [Line[], Line[]];
  private blocks: DiffBlock[];

  constructor(left: string, right: string) {
    this.texts = [left, right];
    this.lineTables = [[], []];
    this.blocks = [];
    this.recompute();
  }

  private recompute(): void {
    this.lineTables = [splitLines(this.texts[0]), splitLines(this.texts[1])];
    const a = this.lineTables[0].map((l) => l.text);
    const b = this.lineTables[1].map((l) => l.text);
    this.blocks = diffHunks(a, b).map((h) => ({
      kind: h.aStart === h.aEnd ? 'insert' : h.bStart === h.bEnd ? 'delete' : 'replace',
      left: { start: h.aStart, end: h.aEnd },
      right: { start: h.bStart, end: h.bEnd },
    }));
  }

  getText(side: Side): string {
    return this.texts[sideIndex(side)];
  }

  getLineCount(side: Side): number {
    return this.lineTables[sideIndex(side)].length;
  }

  getLine(side: Side, line: number): Line {
    const lines = this.lineTables[sideIndex(side)];
    if (!Number.isInteger(line) || line < 0 || line >= lines.length) {
      throw new RangeError(`line ${line} out of range 0..${lines.length - 1}`);
    }
    return lines[line];
  }

  /** Changed blocks only, ordered by position. Equal regions are implicit. */
  getBlocks(): DiffBlock[] {
    return this.blocks.map((b) => ({
      kind: b.kind,
      left: { ...b.left },
      right: { ...b.right },
    }));
  }

  /**
   * Replace the UTF-16 range [start, end) on one side with `text`.
   * Offsets are clamped into the text, swapped if inverted, and
   * expanded outwards so a surrogate pair is never split. Returns
   * the adjusted range that was actually replaced.
   */
  applyEdit(side: Side, start: number, end: number, text: string): AppliedEdit {
    const idx = sideIndex(side);
    const current = this.texts[idx];
    let s = clampOffset(current, start);
    let e = clampOffset(current, end);
    if (e < s) {
      const t = s;
      s = e;
      e = t;
    }
    s = snapOffsetBackward(current, s);
    e = snapOffsetForward(current, e);
    this.texts[idx] = current.slice(0, s) + text + current.slice(e);
    this.recompute();
    return { start: s, end: e };
  }

  /** Replace the whole text of one side. */
  setText(side: Side, text: string): void {
    this.texts[sideIndex(side)] = text;
    this.recompute();
  }

  /**
   * Map a 0-based line number to the other side. Lines inside equal
   * regions map one-to-one (shifted by earlier blocks). Lines inside
   * a changed region — deleted lines, inserted lines, replaced lines —
   * have no counterpart and map to `null` (never to a neighbour).
   */
  mapLine(from: Side, line: number): number | null {
    const fromIdx = sideIndex(from);
    const lines = this.lineTables[fromIdx];
    if (!Number.isInteger(line) || line < 0 || line >= lines.length) {
      throw new RangeError(`line ${line} out of range 0..${lines.length - 1}`);
    }
    const fromKey = from === 'left' ? 'left' : 'right';
    const toKey = from === 'left' ? 'right' : 'left';
    let delta = 0;
    for (const block of this.blocks) {
      const s = block[fromKey];
      const t = block[toKey];
      if (line < s.start) return line + delta;
      if (line < s.end) return null;
      delta += t.end - t.start - (s.end - s.start);
    }
    return line + delta;
  }

  /**
   * Folded view of both sides: changed blocks interleaved with
   * visible equal context lines (`contextLines` around each change)
   * and `fold` items collapsing the remaining equal lines. Folding
   * is view-only; `mapLine` keeps working across folds.
   */
  getView(contextLines = 3): ViewItem[] {
    const context = Math.max(0, Math.trunc(contextLines));
    const items: ViewItem[] = [];
    const totalL = this.lineTables[0].length;
    const totalR = this.lineTables[1].length;
    let cursorL = 0;
    let cursorR = 0;

    const pushEqual = (lStart: number, lEnd: number, rStart: number, rEnd: number): void => {
      if (lEnd > lStart) items.push({ type: 'equal', left: { start: lStart, end: lEnd }, right: { start: rStart, end: rEnd } });
    };
    const pushFold = (lStart: number, lEnd: number, rStart: number, rEnd: number): void => {
      if (lEnd > lStart) items.push({ type: 'fold', left: { start: lStart, end: lEnd }, right: { start: rStart, end: rEnd }, lineCount: lEnd - lStart });
    };

    for (let bi = 0; bi <= this.blocks.length; bi++) {
      const next = this.blocks[bi];
      const gapEndL = next ? next.left.start : totalL;
      const gapEndR = next ? next.right.start : totalR;
      const gap = gapEndL - cursorL;
      const lead = bi > 0 ? context : 0;
      const trail = next ? context : 0;
      if (gap > 0) {
        if (lead + trail >= gap) {
          pushEqual(cursorL, gapEndL, cursorR, gapEndR);
        } else {
          pushEqual(cursorL, cursorL + lead, cursorR, cursorR + lead);
          pushFold(cursorL + lead, gapEndL - trail, cursorR + lead, gapEndR - trail);
          pushEqual(gapEndL - trail, gapEndL, gapEndR - trail, gapEndR);
        }
      }
      if (next) {
        items.push({ type: 'change', block: { kind: next.kind, left: { ...next.left }, right: { ...next.right } } });
        cursorL = next.left.end;
        cursorR = next.right.end;
      }
    }
    return items;
  }

  /**
   * UTF-16 offset of a line/column position. The column is clamped
   * to the line content and snapped backwards if it would split a
   * surrogate pair.
   */
  lineColToOffset(side: Side, line: number, col: number): number {
    const l = this.getLine(side, line);
    const c = snapOffsetBackward(l.text, clampOffset(l.text, col));
    return l.start + c;
  }

  /**
   * Line/column position of a UTF-16 offset. The offset is clamped
   * into the text and snapped backwards if it would split a
   * surrogate pair; offsets inside a line terminator resolve to the
   * end of that line's content.
   */
  offsetToLineCol(side: Side, offset: number): Position {
    const idx = sideIndex(side);
    const text = this.texts[idx];
    const lines = this.lineTables[idx];
    if (lines.length === 0) return { line: 0, col: 0 };
    const o = snapOffsetBackward(text, clampOffset(text, offset));
    let lo = 0;
    let hi = lines.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (lines[mid].start <= o) lo = mid;
      else hi = mid - 1;
    }
    const line = lines[lo];
    return { line: lo, col: Math.min(o - line.start, line.text.length) };
  }
}
