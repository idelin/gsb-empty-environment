import {
  clampToSurrogateBoundary,
  isHighSurrogate,
  isLowSurrogate,
  splitsSurrogatePair,
} from './textutil.ts';

/** Zero-based position. `character` is a UTF-16 code-unit offset within the line. */
export interface Position {
  line: number;
  character: number;
}

function computeLineStarts(text: string): number[] {
  const starts: number[] = [0];
  for (let i = 0; i < text.length; i++) {
    if (text[i] === '\n') starts.push(i + 1);
  }
  return starts;
}

/**
 * A text document with UTF-16 offsets, surrogate-pair-safe edits and
 * an LF/CRLF-aware line index.
 */
export class TextDocument {
  private _text: string;
  private lineStarts: number[];

  constructor(text: string) {
    this._text = text;
    this.lineStarts = computeLineStarts(text);
  }

  get text(): string {
    return this._text;
  }

  get lineCount(): number {
    return this.lineStarts.length;
  }

  /**
   * Replaces the range [start, end) with `newText`.
   * Throws a RangeError when the edit would split a surrogate pair, either
   * by cutting the existing text or by inserting a lone half pair.
   */
  applyEdit(start: number, end: number, newText: string): void {
    if (!Number.isInteger(start) || !Number.isInteger(end) || start < 0 || end < start || end > this._text.length) {
      throw new RangeError(`Invalid edit range [${start}, ${end}) for text of length ${this._text.length}`);
    }
    if (splitsSurrogatePair(this._text, start)) {
      throw new RangeError(`Edit start ${start} splits a surrogate pair`);
    }
    if (splitsSurrogatePair(this._text, end)) {
      throw new RangeError(`Edit end ${end} splits a surrogate pair`);
    }
    if (newText.length > 0) {
      if (isLowSurrogate(newText.charCodeAt(0))) {
        throw new RangeError('Inserted text must not start with a lone low surrogate');
      }
      if (isHighSurrogate(newText.charCodeAt(newText.length - 1))) {
        throw new RangeError('Inserted text must not end with a lone high surrogate');
      }
    }
    this._text = this._text.slice(0, start) + newText + this._text.slice(end);
    this.updateLineStarts(start, end, newText);
  }

  private updateLineStarts(start: number, end: number, newText: string): void {
    const delta = newText.length - (end - start);
    const added: number[] = [];
    for (let i = 0; i < newText.length; i++) {
      if (newText[i] === '\n') added.push(start + i + 1);
    }
    // Old line starts in (start, end] disappear; starts after `end` shift by delta.
    let removeFrom = this.lineStarts.length;
    let removeTo = this.lineStarts.length;
    for (let i = this.lineStarts.length - 1; i >= 0; i--) {
      const s = this.lineStarts[i];
      if (s > end) {
        this.lineStarts[i] = s + delta;
        removeFrom = i;
        removeTo = i;
      } else if (s > start) {
        removeFrom = i;
      } else {
        break;
      }
    }
    const removeCount = removeTo - removeFrom;
    if (added.length > 0 || removeCount > 0) {
      this.lineStarts.splice(removeFrom, removeCount, ...added);
    }
  }

  /** End offset of `line`, excluding its line terminator (handles LF and CRLF). */
  private lineContentEnd(line: number): number {
    const next = line + 1;
    if (next >= this.lineStarts.length) return this._text.length;
    const nextStart = this.lineStarts[next];
    // Terminator is "\n" or "\r\n".
    return this._text[nextStart - 2] === '\r' ? nextStart - 2 : nextStart - 1;
  }

  /** Converts a UTF-16 offset to a position. Never lands inside a surrogate pair. */
  positionAt(offset: number): Position {
    const safe = clampToSurrogateBoundary(this._text, offset);
    let low = 0;
    let high = this.lineStarts.length - 1;
    while (low < high) {
      const mid = (low + high + 1) >> 1;
      if (this.lineStarts[mid] <= safe) low = mid;
      else high = mid - 1;
    }
    return { line: low, character: safe - this.lineStarts[low] };
  }

  /** Converts a position to a UTF-16 offset, clamped to the line content. */
  offsetAt(position: Position): number {
    const line = Math.max(0, Math.min(position.line, this.lineStarts.length - 1));
    const start = this.lineStarts[line];
    const end = this.lineContentEnd(line);
    const raw = Math.max(start, Math.min(start + position.character, end));
    return clampToSurrogateBoundary(this._text, raw);
  }
}
