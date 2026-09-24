/**
 * ChunkedText: text stored as a list of string chunks.
 *
 * - All public offsets are UTF-16 code-unit offsets into the logical text.
 * - Chunk boundaries never split a surrogate pair.
 * - Edit ranges that would split a surrogate pair are rejected.
 * - `charsRead` counts every code unit actually read from the chunks, so
 *   tests can assert that searches/edits only re-read a bounded window.
 */

export interface EditRecord {
  /** UTF-16 offset where the replaced range started (same in old/new text). */
  readonly start: number;
  /** End of the replaced range in the old text (exclusive). */
  readonly oldEnd: number;
  /** End of the inserted text in the new text (exclusive). */
  readonly newEnd: number;
  /** newEnd - oldEnd shift applied to everything at/after oldEnd. */
  readonly delta: number;
}

export type EditListener = (edit: EditRecord) => void;

export const isHighSurrogate = (code: number): boolean =>
  code >= 0xd800 && code <= 0xdbff;
export const isLowSurrogate = (code: number): boolean =>
  code >= 0xdc00 && code <= 0xdfff;

/**
 * Split `text` into chunks of at most `chunkSize` code units, never cutting
 * between a high and a low surrogate. A pair is kept whole even if chunkSize
 * is 1 (the chunk grows to 2 units in that case).
 */
export function splitChunks(text: string, chunkSize: number): string[] {
  if (!Number.isInteger(chunkSize) || chunkSize < 1) {
    throw new RangeError("chunkSize must be a positive integer");
  }
  const chunks: string[] = [];
  let i = 0;
  while (i < text.length) {
    let end = Math.min(i + chunkSize, text.length);
    if (
      end < text.length &&
      isHighSurrogate(text.charCodeAt(end - 1)) &&
      isLowSurrogate(text.charCodeAt(end))
    ) {
      end -= 1;
    }
    if (end === i) {
      // chunkSize too small to hold the pair starting at i; keep it together.
      end = Math.min(i + 2, text.length);
    }
    chunks.push(text.slice(i, end));
    i = end;
  }
  if (chunks.length === 0) chunks.push("");
  return chunks;
}

export class ChunkedText {
  readonly chunkSize: number;
  private chunks: string[];
  private starts: number[] = [];
  private _length = 0;
  /** Number of code units read from chunks since construction/last reset. */
  charsRead = 0;
  private listeners: EditListener[] = [];

  constructor(text = "", chunkSize = 16) {
    this.chunkSize = chunkSize;
    this.chunks = splitChunks(text, chunkSize);
    this.recomputeStarts(0);
  }

  get length(): number {
    return this._length;
  }

  get chunkCount(): number {
    return this.chunks.length;
  }

  chunkAt(index: number): string {
    return this.chunks[index];
  }

  chunkStart(index: number): number {
    return this.starts[index];
  }

  addListener(listener: EditListener): void {
    this.listeners.push(listener);
  }

  private recomputeStarts(from: number): void {
    let pos = from === 0 ? 0 : this.starts[from - 1] + this.chunks[from - 1].length;
    this.starts.length = this.chunks.length;
    for (let i = from; i < this.chunks.length; i++) {
      this.starts[i] = pos;
      pos += this.chunks[i].length;
    }
    this._length = pos;
  }

  /** Index of the chunk containing `offset` (offset == length maps to last). */
  private locate(offset: number): number {
    let lo = 0;
    let hi = this.starts.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (this.starts[mid] <= offset) lo = mid + 1;
      else hi = mid;
    }
    return lo - 1;
  }

  /** Code unit at `offset`; counts towards charsRead. */
  codeAt(offset: number): number {
    if (offset < 0 || offset >= this._length) {
      throw new RangeError(`offset ${offset} out of range [0, ${this._length})`);
    }
    this.charsRead++;
    const ci = this.locate(offset);
    return this.chunks[ci].charCodeAt(offset - this.starts[ci]);
  }

  /** Character (one UTF-16 code unit) at `offset`; counts towards charsRead. */
  charAt(offset: number, lower = false): string {
    const ch = String.fromCharCode(this.codeAt(offset));
    return lower ? ch.toLowerCase() : ch;
  }

  /** Read text[start, end); counts end-start code units towards charsRead. */
  slice(start: number, end: number): string {
    if (!(0 <= start && start <= end && end <= this._length)) {
      throw new RangeError(`invalid slice [${start}, ${end}) of length ${this._length}`);
    }
    this.charsRead += end - start;
    let out = "";
    let ci = this.locate(start);
    let pos = start;
    while (pos < end) {
      const chunk = this.chunks[ci];
      const from = pos - this.starts[ci];
      const take = Math.min(end - pos, chunk.length - from);
      out += chunk.slice(from, from + take);
      pos += take;
      ci++;
    }
    return out;
  }

  /** True if cutting the text at `offset` would split a surrogate pair. */
  splitsSurrogatePair(offset: number): boolean {
    return (
      offset > 0 &&
      offset < this._length &&
      isHighSurrogate(this.codeAt(offset - 1)) &&
      isLowSurrogate(this.codeAt(offset))
    );
  }

  /**
   * Replace text[start, end) with `text`. Both boundaries must not split a
   * surrogate pair. Only the chunks overlapping the edit are rebuilt, so the
   * temporary buffer is O(chunkSize + inserted length), never the full text.
   */
  replace(start: number, end: number, text: string): EditRecord {
    if (!(0 <= start && start <= end && end <= this._length)) {
      throw new RangeError(`invalid edit range [${start}, ${end}) of length ${this._length}`);
    }
    if (this.splitsSurrogatePair(start) || this.splitsSurrogatePair(end)) {
      throw new RangeError("edit range splits a surrogate pair");
    }
    const first = this.locate(start);
    const last = this.locate(end);
    const prefix = this.chunks[first].slice(0, start - this.starts[first]);
    const suffix = this.chunks[last].slice(end - this.starts[last]);
    this.charsRead += prefix.length + suffix.length;
    const middle = splitChunks(prefix + text + suffix, this.chunkSize);
    this.chunks.splice(first, last - first + 1, ...middle);
    this.recomputeStarts(first);
    const record: EditRecord = {
      start,
      oldEnd: end,
      newEnd: start + text.length,
      delta: text.length - (end - start),
    };
    for (const listener of this.listeners) listener(record);
    return record;
  }

  /** Full text as one string (diagnostics/tests only; search never uses it). */
  toString(): string {
    return this.chunks.join("");
  }
}
