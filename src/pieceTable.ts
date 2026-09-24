/**
 * Editable text buffer (piece table over an original + an append buffer).
 * All public offsets are UTF-16 code-unit offsets. Edits are clamped so
 * they never split a surrogate pair.
 */

const HIGH_MIN = 0xd800;
const HIGH_MAX = 0xdbff;
const LOW_MIN = 0xdc00;
const LOW_MAX = 0xdfff;

export function isHighSurrogate(code: number): boolean {
  return code >= HIGH_MIN && code <= HIGH_MAX;
}

export function isLowSurrogate(code: number): boolean {
  return code >= LOW_MIN && code <= LOW_MAX;
}

interface Piece {
  buffer: 0 | 1; // 0 = original, 1 = added
  start: number;
  length: number;
}

export interface AppliedEdit {
  /** Actual (surrogate-clamped) edit that was applied. */
  start: number;
  deleteCount: number;
  text: string;
}

export class PieceTable {
  private original: string;
  private added = '';
  private pieces: Piece[] = [];
  private len = 0;

  constructor(text = '') {
    this.original = text;
    if (text.length > 0) {
      this.pieces.push({ buffer: 0, start: 0, length: text.length });
      this.len = text.length;
    }
  }

  get length(): number {
    return this.len;
  }

  getText(): string {
    let out = '';
    for (const p of this.pieces) {
      out += (p.buffer === 0 ? this.original : this.added).substr(p.start, p.length);
    }
    return out;
  }

  charCodeAt(index: number): number {
    let base = 0;
    for (const p of this.pieces) {
      if (index < base + p.length) {
        return (p.buffer === 0 ? this.original : this.added).charCodeAt(p.start + (index - base));
      }
      base += p.length;
    }
    return NaN;
  }

  /**
   * Clamp to [0, length] and never land between a high and a low
   * surrogate: such an offset moves to the pair's start.
   */
  clampOffset(offset: number): number {
    offset = Math.max(0, Math.min(offset, this.len));
    if (offset > 0 && offset < this.len) {
      if (isHighSurrogate(this.charCodeAt(offset - 1)) && isLowSurrogate(this.charCodeAt(offset))) {
        return offset - 1;
      }
    }
    return offset;
  }

  applyEdit(start: number, deleteCount: number, text: string): AppliedEdit {
    start = this.clampOffset(start);
    const end = this.clampOffset(start + Math.max(0, deleteCount));
    deleteCount = end - start;
    if (deleteCount > 0) this.deleteRange(start, end);
    if (text.length > 0) this.insertAt(start, text);
    return { start, deleteCount, text };
  }

  private locate(offset: number): { index: number; inner: number } {
    // Position `offset` belongs to piece `index` at inner offset `inner`.
    // An offset exactly at a piece boundary resolves to the NEXT piece
    // (inner 0) so inserts don't grow the left neighbour.
    let base = 0;
    for (let i = 0; i < this.pieces.length; i++) {
      const p = this.pieces[i];
      if (offset < base + p.length || (offset === base + p.length && i === this.pieces.length - 1)) {
        return { index: i, inner: offset - base };
      }
      base += p.length;
    }
    return { index: this.pieces.length, inner: 0 };
  }

  private insertAt(offset: number, text: string): void {
    const piece: Piece = { buffer: 1, start: this.added.length, length: text.length };
    this.added += text;
    if (this.pieces.length === 0) {
      this.pieces.push(piece);
    } else {
      const { index, inner } = this.locate(offset);
      if (inner === 0) {
        this.pieces.splice(index, 0, piece);
      } else if (inner === this.pieces[index]?.length && index === this.pieces.length - 1) {
        this.pieces.push(piece);
      } else {
        const p = this.pieces[index];
        if (inner === p.length) {
          this.pieces.splice(index + 1, 0, piece);
        } else {
          const right: Piece = { buffer: p.buffer, start: p.start + inner, length: p.length - inner };
          p.length = inner;
          this.pieces.splice(index + 1, 0, piece, right);
        }
      }
    }
    this.len += text.length;
  }

  private deleteRange(start: number, end: number): void {
    if (start >= end) return;
    const from = this.locate(start);
    const to = this.locate(end);
    if (from.index === to.index) {
      const p = this.pieces[from.index];
      if (from.inner === 0 && to.inner === p.length) {
        this.pieces.splice(from.index, 1);
      } else if (from.inner === 0) {
        p.start += to.inner;
        p.length -= to.inner;
      } else if (to.inner === p.length) {
        p.length = from.inner;
      } else {
        const right: Piece = { buffer: p.buffer, start: p.start + to.inner, length: p.length - to.inner };
        p.length = from.inner;
        this.pieces.splice(from.index + 1, 0, right);
      }
    } else {
      const first = this.pieces[from.index];
      const last = this.pieces[to.index];
      const keepHead: Piece[] = from.inner > 0 ? [{ buffer: first.buffer, start: first.start, length: from.inner }] : [];
      // When `end` lands exactly on a piece boundary, that piece is untouched.
      const removeUpTo = to.inner > 0 ? to.index + 1 : to.index;
      const keepTail: Piece[] = to.inner > 0 ? [{ buffer: last.buffer, start: last.start + to.inner, length: last.length - to.inner }] : [];
      this.pieces.splice(from.index, removeUpTo - from.index, ...keepHead, ...keepTail);
    }
    this.len -= end - start;
  }
}
