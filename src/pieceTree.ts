/**
 * Editable piece tree (piece table with an incremental line index).
 *
 * All public offsets are UTF-16 code-unit offsets into the document.
 * Edits that would split a surrogate pair are rejected.
 * Lines are terminated by LF or CRLF; the terminator is not part of
 * the line content.
 */

export interface EditResult {
  /** First affected logical line (post-edit line numbering). */
  readonly startLine: number;
  /** Number of affected lines before the edit. */
  readonly oldLineCount: number;
  /** Number of affected lines after the edit. */
  readonly newLineCount: number;
}

interface Piece {
  /** 0 = original buffer, 1 = append buffer. */
  buffer: 0 | 1;
  start: number;
  length: number;
}

export function isHighSurrogate(code: number): boolean {
  return code >= 0xd800 && code <= 0xdbff;
}

export function isLowSurrogate(code: number): boolean {
  return code >= 0xdc00 && code <= 0xdfff;
}

/** True when `offset` falls between a high and a low surrogate. */
export function isMidSurrogatePair(text: string, offset: number): boolean {
  return (
    offset > 0 &&
    offset < text.length &&
    isHighSurrogate(text.charCodeAt(offset - 1)) &&
    isLowSurrogate(text.charCodeAt(offset))
  );
}

function countChar(text: string, ch: string): number {
  let count = 0;
  let idx = text.indexOf(ch);
  while (idx !== -1) {
    count += 1;
    idx = text.indexOf(ch, idx + 1);
  }
  return count;
}

export class PieceTree {
  private readonly original: string;
  private added = '';
  private pieces: Piece[] = [];
  private lineStarts: number[] = [0];
  private textLength = 0;
  private cachedText: string | null = null;

  constructor(text = '') {
    this.original = text;
    if (text.length > 0) {
      this.pieces.push({ buffer: 0, start: 0, length: text.length });
    }
    this.textLength = text.length;
    this.rebuildLineStartsFrom(0);
  }

  get length(): number {
    return this.textLength;
  }

  get lineCount(): number {
    return this.lineStarts.length;
  }

  getText(): string {
    if (this.cachedText === null) {
      this.cachedText = this.pieces
        .map((p) => (p.buffer === 0 ? this.original : this.added).substring(p.start, p.start + p.length))
        .join('');
    }
    return this.cachedText;
  }

  /** Document offset where `line` starts (line terminators excluded from content). */
  lineStart(line: number): number {
    return this.lineStarts[line];
  }

  /** Logical line containing `offset` (0-based). */
  lineAt(offset: number): number {
    let lo = 0;
    let hi = this.lineStarts.length - 1;
    let ans = 0;
    while (lo <= hi) {
      const mid = (lo + hi) >> 1;
      if (this.lineStarts[mid] <= offset) {
        ans = mid;
        lo = mid + 1;
      } else {
        hi = mid - 1;
      }
    }
    return ans;
  }

  /** Line content without its LF / CRLF terminator. */
  lineContent(line: number): string {
    const text = this.getText();
    const start = this.lineStarts[line];
    let end = this.textLength;
    if (line + 1 < this.lineStarts.length) {
      const next = this.lineStarts[line + 1];
      end = next - 1; // '\n'
      if (end > start && text.charCodeAt(end - 1) === 0x0d) {
        end -= 1; // '\r\n'
      }
    }
    return text.slice(start, end);
  }

  insert(offset: number, text: string): EditResult {
    this.assertValidBoundary(offset);
    if (text.length === 0) {
      return { startLine: 0, oldLineCount: 0, newLineCount: 0 };
    }
    if (
      isLowSurrogate(text.charCodeAt(0)) ||
      isHighSurrogate(text.charCodeAt(text.length - 1))
    ) {
      throw new Error('inserted text would split a surrogate pair');
    }
    const startLine = this.lineAt(offset);
    const insertedNewlines = countChar(text, '\n');
    const piece: Piece = { buffer: 1, start: this.added.length, length: text.length };
    this.added += text;
    this.insertPiece(offset, piece);
    this.textLength += text.length;
    this.cachedText = null;
    this.rebuildLineStartsFrom(startLine);
    return { startLine, oldLineCount: 1, newLineCount: 1 + insertedNewlines };
  }

  delete(offset: number, length: number): EditResult {
    if (!Number.isInteger(length) || length < 0) {
      throw new RangeError(`invalid delete length ${length}`);
    }
    this.assertValidBoundary(offset);
    this.assertValidBoundary(offset + length);
    if (length === 0) {
      return { startLine: 0, oldLineCount: 0, newLineCount: 0 };
    }
    const deleted = this.getText().slice(offset, offset + length);
    const startLine = this.lineAt(offset);
    const oldLineCount = this.lineAt(offset + length) - startLine + 1;
    const deletedNewlines = countChar(deleted, '\n');
    this.deleteRange(offset, length);
    this.textLength -= length;
    this.cachedText = null;
    this.rebuildLineStartsFrom(startLine);
    return {
      startLine,
      oldLineCount,
      newLineCount: oldLineCount - deletedNewlines,
    };
  }

  private assertValidBoundary(offset: number): void {
    if (!Number.isInteger(offset) || offset < 0 || offset > this.textLength) {
      throw new RangeError(`offset ${offset} out of range [0, ${this.textLength}]`);
    }
    if (isMidSurrogatePair(this.getText(), offset)) {
      throw new Error(`offset ${offset} splits a surrogate pair`);
    }
  }

  private locate(offset: number): { index: number; inner: number } {
    let pos = 0;
    for (let i = 0; i < this.pieces.length; i++) {
      const p = this.pieces[i];
      if (offset <= pos + p.length) {
        return { index: i, inner: offset - pos };
      }
      pos += p.length;
    }
    return { index: this.pieces.length, inner: 0 };
  }

  private insertPiece(offset: number, piece: Piece): void {
    const { index, inner } = this.locate(offset);
    if (index === this.pieces.length) {
      this.pieces.push(piece);
      return;
    }
    const p = this.pieces[index];
    if (inner === 0) {
      this.pieces.splice(index, 0, piece);
      return;
    }
    if (inner === p.length) {
      this.pieces.splice(index + 1, 0, piece);
      return;
    }
    const right: Piece = {
      buffer: p.buffer,
      start: p.start + inner,
      length: p.length - inner,
    };
    p.length = inner;
    this.pieces.splice(index + 1, 0, piece, right);
  }

  private splitAt(offset: number): void {
    const { index, inner } = this.locate(offset);
    if (index === this.pieces.length || inner === 0) {
      return;
    }
    const p = this.pieces[index];
    if (inner === p.length) {
      return;
    }
    const right: Piece = {
      buffer: p.buffer,
      start: p.start + inner,
      length: p.length - inner,
    };
    p.length = inner;
    this.pieces.splice(index + 1, 0, right);
  }

  /** Index of the first piece starting at or after `offset` (offset must be piece-aligned). */
  private pieceIndexAt(offset: number): number {
    let pos = 0;
    for (let i = 0; i < this.pieces.length; i++) {
      if (pos === offset) {
        return i;
      }
      pos += this.pieces[i].length;
    }
    return this.pieces.length;
  }

  private deleteRange(offset: number, length: number): void {
    this.splitAt(offset + length);
    this.splitAt(offset);
    const first = this.pieceIndexAt(offset);
    const last = this.pieceIndexAt(offset + length);
    if (last > first) {
      this.pieces.splice(first, last - first);
    }
  }

  /** Recompute line starts from `startLine` onward; earlier entries stay valid. */
  private rebuildLineStartsFrom(startLine: number): void {
    const text = this.getText();
    const scanFrom = this.lineStarts[startLine] ?? 0;
    const tail: number[] = [];
    let idx = text.indexOf('\n', scanFrom);
    while (idx !== -1) {
      tail.push(idx + 1);
      idx = text.indexOf('\n', idx + 1);
    }
    this.lineStarts = this.lineStarts.slice(0, startLine + 1).concat(tail);
  }
}
