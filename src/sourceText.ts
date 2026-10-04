/**
 * SourceText stores the underlying source as an array of Unicode scalar
 * values (code points) and exposes bidirectional mapping between UTF-16
 * code-unit offsets (used by semantic tokens, LSP-style) and scalar indices
 * (the internal storage coordinate).
 */
export class SourceText {
  private readonly scalars: number[];
  /** utf16Offsets[i] is the UTF-16 offset at which scalar i begins. Has scalars.length + 1 entries. */
  private readonly utf16Offsets: number[];
  private readonly text: string;

  constructor(text: string) {
    this.text = text;
    this.scalars = [];
    this.utf16Offsets = [];
    let utf16 = 0;
    for (const ch of text) {
      const cp = ch.codePointAt(0)!;
      this.scalars.push(cp);
      this.utf16Offsets.push(utf16);
      utf16 += cp > 0xffff ? 2 : 1;
    }
    this.utf16Offsets.push(utf16);
  }

  /** Number of Unicode scalar values in the source. */
  get scalarLength(): number {
    return this.scalars.length;
  }

  /** Number of UTF-16 code units in the source. */
  get utf16Length(): number {
    return this.utf16Offsets[this.scalars.length];
  }

  /** Scalar value (code point) at the given scalar index. */
  scalarAt(index: number): number {
    if (index < 0 || index >= this.scalars.length) {
      throw new RangeError(`scalar index ${index} out of range [0, ${this.scalars.length})`);
    }
    return this.scalars[index];
  }

  /** Map a scalar index to its UTF-16 code-unit offset. */
  scalarIndexToUtf16Offset(index: number): number {
    if (index < 0 || index > this.scalars.length) {
      throw new RangeError(`scalar index ${index} out of range [0, ${this.scalars.length}]`);
    }
    return this.utf16Offsets[index];
  }

  /**
   * Map a UTF-16 code-unit offset to a scalar index.
   * Throws if the offset falls in the middle of a surrogate pair.
   */
  utf16OffsetToScalarIndex(offset: number): number {
    if (offset < 0 || offset > this.utf16Length) {
      throw new RangeError(`UTF-16 offset ${offset} out of range [0, ${this.utf16Length}]`);
    }
    let lo = 0;
    let hi = this.scalars.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (this.utf16Offsets[mid] < offset) {
        lo = mid + 1;
      } else {
        hi = mid;
      }
    }
    if (this.utf16Offsets[lo] !== offset) {
      throw new RangeError(`UTF-16 offset ${offset} splits a surrogate pair`);
    }
    return lo;
  }

  /** Substring by scalar indices, returned as a JS (UTF-16) string. */
  sliceScalars(start: number, end: number): string {
    return String.fromCodePoint(...this.scalars.slice(start, end));
  }

  /** Substring by UTF-16 offsets. */
  sliceUtf16(start: number, end: number): string {
    return this.text.slice(start, end);
  }

  toString(): string {
    return this.text;
  }
}
