/**
 * Source text stored as a sequence of Unicode scalar values (code points),
 * with a bidirectional mapping to UTF-16 code unit offsets.
 *
 * Semantic tokens are reported in UTF-16 units (the LSP convention), while
 * the lexer/analyzer work in scalar offsets. This class is the single place
 * that converts between the two coordinate spaces.
 */

export interface Position {
  /** Zero-based line number. */
  line: number;
  /** Zero-based column in UTF-16 code units. */
  character: number;
}

const REPLACEMENT_CHARACTER = 0xfffd;
const LINE_FEED = 0x0a;
const CARRIAGE_RETURN = 0x0d;

export class ScalarText {
  /** Unicode scalar values of the source. Lone surrogates are replaced by U+FFFD. */
  readonly scalars: readonly number[];

  /** scalarToUnit[i] = UTF-16 offset of the boundary before scalar i. Length n+1. */
  private readonly scalarToUnit: Uint32Array;

  /** unitToScalar[u] = scalar index whose boundary is at UTF-16 offset u, or -1
   *  when u falls inside a surrogate pair. Length utf16Length+1. */
  private readonly unitToScalar: Int32Array;

  /** Scalar offsets at which each line starts. */
  private readonly lineStartScalars: readonly number[];

  private constructor(scalars: number[]) {
    this.scalars = scalars;

    const n = scalars.length;
    const scalarToUnit = new Uint32Array(n + 1);
    let units = 0;
    for (let i = 0; i < n; i++) {
      scalarToUnit[i] = units;
      units += scalars[i] > 0xffff ? 2 : 1;
    }
    scalarToUnit[n] = units;
    this.scalarToUnit = scalarToUnit;

    const unitToScalar = new Int32Array(units + 1).fill(-1);
    for (let i = 0; i <= n; i++) {
      unitToScalar[scalarToUnit[i]] = i;
    }
    this.unitToScalar = unitToScalar;

    const lineStarts: number[] = [0];
    for (let i = 0; i < n; i++) {
      const cp = scalars[i];
      if (cp === LINE_FEED) {
        lineStarts.push(i + 1);
      } else if (cp === CARRIAGE_RETURN && scalars[i + 1] !== LINE_FEED) {
        lineStarts.push(i + 1);
      }
    }
    this.lineStartScalars = lineStarts;
  }

  static from(source: string): ScalarText {
    const scalars: number[] = [];
    for (let i = 0; i < source.length; ) {
      const cp = source.codePointAt(i)!;
      const width = cp > 0xffff ? 2 : 1;
      // Unicode scalar values exclude surrogates; sanitize lone surrogates.
      scalars.push(cp >= 0xd800 && cp <= 0xdfff ? REPLACEMENT_CHARACTER : cp);
      i += width;
    }
    return new ScalarText(scalars);
  }

  /** Number of Unicode scalar values in the source. */
  get scalarLength(): number {
    return this.scalars.length;
  }

  /** Number of UTF-16 code units in the source. */
  get utf16Length(): number {
    return this.scalarToUnit[this.scalars.length];
  }

  /** Number of lines (always >= 1). */
  get lineCount(): number {
    return this.lineStartScalars.length;
  }

  scalarAt(index: number): number {
    if (index < 0 || index >= this.scalars.length) {
      throw new RangeError(`scalar index ${index} out of range [0, ${this.scalars.length})`);
    }
    return this.scalars[index];
  }

  /** Map a scalar offset (0..scalarLength) to a UTF-16 code unit offset. */
  scalarOffsetToUtf16(scalarOffset: number): number {
    if (!Number.isInteger(scalarOffset) || scalarOffset < 0 || scalarOffset > this.scalars.length) {
      throw new RangeError(`scalar offset ${scalarOffset} out of range [0, ${this.scalars.length}]`);
    }
    return this.scalarToUnit[scalarOffset];
  }

  /** True when the UTF-16 offset lies on a scalar boundary (not inside a surrogate pair). */
  isUtf16Boundary(utf16Offset: number): boolean {
    return (
      Number.isInteger(utf16Offset) &&
      utf16Offset >= 0 &&
      utf16Offset <= this.utf16Length &&
      this.unitToScalar[utf16Offset] !== -1
    );
  }

  /** Map a UTF-16 code unit offset (0..utf16Length) to a scalar offset.
   *  Throws when the offset splits a surrogate pair. */
  utf16OffsetToScalar(utf16Offset: number): number {
    if (!Number.isInteger(utf16Offset) || utf16Offset < 0 || utf16Offset > this.utf16Length) {
      throw new RangeError(`UTF-16 offset ${utf16Offset} out of range [0, ${this.utf16Length}]`);
    }
    const scalar = this.unitToScalar[utf16Offset];
    if (scalar === -1) {
      throw new RangeError(`UTF-16 offset ${utf16Offset} splits a surrogate pair`);
    }
    return scalar;
  }

  /** Convert a UTF-16 offset to a (line, UTF-16 column) position. */
  utf16OffsetToPosition(utf16Offset: number): Position {
    const scalar = this.utf16OffsetToScalar(utf16Offset);
    let low = 0;
    let high = this.lineStartScalars.length - 1;
    while (low < high) {
      const mid = (low + high + 1) >> 1;
      if (this.lineStartScalars[mid] <= scalar) low = mid;
      else high = mid - 1;
    }
    const lineStartUnit = this.scalarToUnit[this.lineStartScalars[low]];
    return { line: low, character: utf16Offset - lineStartUnit };
  }

  /** Convert a (line, UTF-16 column) position to a UTF-16 offset.
   *  Throws when the position is past the end of the line or splits a surrogate pair. */
  positionToUtf16Offset(position: Position): number {
    const { line, character } = position;
    if (!Number.isInteger(line) || line < 0 || line >= this.lineStartScalars.length) {
      throw new RangeError(`line ${line} out of range [0, ${this.lineStartScalars.length})`);
    }
    if (!Number.isInteger(character) || character < 0) {
      throw new RangeError(`character ${character} out of range`);
    }
    const lineStartScalar = this.lineStartScalars[line];
    let lineEndScalar =
      line + 1 < this.lineStartScalars.length ? this.lineStartScalars[line + 1] : this.scalars.length;
    if (line + 1 < this.lineStartScalars.length) {
      // Exclude the line terminator itself from the valid character range.
      if (lineEndScalar > lineStartScalar && this.scalars[lineEndScalar - 1] === LINE_FEED) {
        lineEndScalar--;
      }
      if (lineEndScalar > lineStartScalar && this.scalars[lineEndScalar - 1] === CARRIAGE_RETURN) {
        lineEndScalar--;
      }
    }
    const offset = this.scalarToUnit[lineStartScalar] + character;
    if (offset > this.scalarToUnit[lineEndScalar]) {
      throw new RangeError(`position ${line}:${character} is past the end of line ${line}`);
    }
    // Validates surrogate-pair boundaries as a side effect.
    this.utf16OffsetToScalar(offset);
    return offset;
  }

  /** Source text of scalars in [startScalar, endScalar) as a JS string. */
  sliceScalars(startScalar: number, endScalar: number): string {
    return String.fromCodePoint(...this.scalars.slice(startScalar, endScalar));
  }

  toString(): string {
    return String.fromCodePoint(...this.scalars);
  }
}
