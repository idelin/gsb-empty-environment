/**
 * Compressed, block-structured bidirectional map between absolute byte
 * offsets (UTF-8 input) and UTF-16 code-unit offsets (decoded text).
 *
 * Each decoded "unit" (one scalar value or one U+FFFD replacement) is stored
 * as (byteLen, utf16Len, replacementFlag). Units are grouped into blocks of
 * MAP_BLOCK_SIZE; every block header carries absolute byte/UTF-16 starts, so
 * random access binary-searches a block and scans at most MAP_BLOCK_SIZE
 * entries -- the decoded prefix is never re-decoded or re-scanned in full.
 */

export const MAP_BLOCK_SIZE = 128;

export interface MapBlockJSON {
  byteStart: number;
  utf16Start: number;
  byteLens: number[];
  utf16Lens: number[];
  flags: number[]; // bit 0: produced by replacement of an ill-formed sequence
}

export interface OffsetMapJSON {
  totalBytes: number;
  totalUtf16: number;
  blocks: MapBlockJSON[];
}

export interface LocatedUnit {
  byteStart: number;
  utf16Start: number;
  byteLen: number;
  utf16Len: number;
  replacement: boolean;
}

export interface CoveredInterval {
  byteStart: number;
  byteEnd: number;
  textStart: number;
  textEnd: number;
  reason: 'replacement' | 'multibyte';
}

export interface ByteToTextRange {
  /** UTF-16 offset of the first unit intersecting the byte range. */
  start: number;
  /** UTF-16 offset just past the last unit intersecting the byte range. */
  end: number;
  /** True when at least one many-to-one unit (byteLen > utf16Len) is covered. */
  collapsed: boolean;
  /** Explicit intervals where several input bytes map to fewer UTF-16 units. */
  covered: CoveredInterval[];
}

export interface TextToByteRange {
  byteStart: number;
  byteEnd: number;
  /** Input range snapped to character boundaries (never splits a surrogate pair). */
  textStart: number;
  textEnd: number;
}

interface MapBlock {
  byteStart: number;
  utf16Start: number;
  byteLens: number[];
  utf16Lens: number[];
  flags: number[];
}

export class OffsetMap {
  private blocks: MapBlock[] = [];
  private totalBytes = 0;
  private totalUtf16 = 0;
  /** Units scanned by the most recent query (instrumentation for tests). */
  lastScanSize = 0;

  get byteLength(): number {
    return this.totalBytes;
  }

  get utf16Length(): number {
    return this.totalUtf16;
  }

  get blockCount(): number {
    return this.blocks.length;
  }

  addUnit(byteLen: number, utf16Len: number, replacement: boolean): void {
    let block = this.blocks[this.blocks.length - 1];
    if (block === undefined || block.byteLens.length >= MAP_BLOCK_SIZE) {
      block = {
        byteStart: this.totalBytes,
        utf16Start: this.totalUtf16,
        byteLens: [],
        utf16Lens: [],
        flags: [],
      };
      this.blocks.push(block);
    }
    block.byteLens.push(byteLen);
    block.utf16Lens.push(utf16Len);
    block.flags.push(replacement ? 1 : 0);
    this.totalBytes += byteLen;
    this.totalUtf16 += utf16Len;
  }

  /** UTF-16 offset of the character containing `byteOffset`. */
  byteToUtf16(byteOffset: number): number {
    this.checkByteOffset(byteOffset);
    if (byteOffset === this.totalBytes) return this.totalUtf16;
    const unit = this.unitAtByte(byteOffset);
    if (unit === null) throw new RangeError(`byte offset ${byteOffset} not mapped`);
    return unit.utf16Start;
  }

  /**
   * Byte offset of the character containing UTF-16 offset `u`.
   * Offsets inside a multi-unit character (e.g. between surrogate halves)
   * snap down to the character start, so a surrogate pair is never split.
   */
  utf16ToByte(u: number): number {
    this.checkUtf16Offset(u);
    if (u === this.totalUtf16) return this.totalBytes;
    const unit = this.unitAtUtf16(u);
    if (unit === null) throw new RangeError(`UTF-16 offset ${u} not mapped`);
    return unit.byteStart;
  }

  /** Map a byte range to the UTF-16 range covering it, listing collapsed intervals. */
  byteRangeToTextRange(byteStart: number, byteEnd: number): ByteToTextRange {
    this.checkByteOffset(byteStart);
    this.checkByteOffset(byteEnd);
    if (byteStart > byteEnd) throw new RangeError('byteStart must be <= byteEnd');
    const start = this.byteToUtf16(byteStart);
    if (byteStart === byteEnd) {
      return { start, end: start, collapsed: false, covered: [] };
    }
    const last = this.unitAtByte(byteEnd - 1);
    if (last === null) throw new RangeError('byte range not mapped');
    const end = last.utf16Start + last.utf16Len;
    const covered: CoveredInterval[] = [];
    for (const unit of this.unitsIntersecting(byteStart, byteEnd)) {
      if (unit.byteLen > unit.utf16Len) {
        covered.push({
          byteStart: unit.byteStart,
          byteEnd: unit.byteStart + unit.byteLen,
          textStart: unit.utf16Start,
          textEnd: unit.utf16Start + unit.utf16Len,
          reason: unit.replacement ? 'replacement' : 'multibyte',
        });
      }
    }
    return { start, end, collapsed: covered.length > 0, covered };
  }

  /** Map a UTF-16 range to the byte range covering it, snapping to char boundaries. */
  textRangeToByteRange(textStart: number, textEnd: number): TextToByteRange {
    this.checkUtf16Offset(textStart);
    this.checkUtf16Offset(textEnd);
    if (textStart > textEnd) throw new RangeError('textStart must be <= textEnd');
    const first = textStart === this.totalUtf16 ? null : this.unitAtUtf16(textStart);
    const snappedStart = first === null ? this.totalUtf16 : first.utf16Start;
    const byteStart = first === null ? this.totalBytes : first.byteStart;
    if (textStart === textEnd) {
      // A point strictly inside a character expands to the whole character,
      // so a query can never land between the halves of a surrogate pair.
      if (first !== null && textStart > first.utf16Start) {
        return {
          byteStart: first.byteStart,
          byteEnd: first.byteStart + first.byteLen,
          textStart: first.utf16Start,
          textEnd: first.utf16Start + first.utf16Len,
        };
      }
      return { byteStart, byteEnd: byteStart, textStart: snappedStart, textEnd: snappedStart };
    }
    const last = this.unitAtUtf16(textEnd - 1);
    if (last === null) throw new RangeError('text range not mapped');
    return {
      byteStart,
      byteEnd: last.byteStart + last.byteLen,
      textStart: snappedStart,
      textEnd: last.utf16Start + last.utf16Len,
    };
  }

  toJSON(): OffsetMapJSON {
    return {
      totalBytes: this.totalBytes,
      totalUtf16: this.totalUtf16,
      blocks: this.blocks.map((b) => ({
        byteStart: b.byteStart,
        utf16Start: b.utf16Start,
        byteLens: [...b.byteLens],
        utf16Lens: [...b.utf16Lens],
        flags: [...b.flags],
      })),
    };
  }

  static fromJSON(json: OffsetMapJSON): OffsetMap {
    const map = new OffsetMap();
    map.totalBytes = json.totalBytes;
    map.totalUtf16 = json.totalUtf16;
    map.blocks = json.blocks.map((b) => ({
      byteStart: b.byteStart,
      utf16Start: b.utf16Start,
      byteLens: [...b.byteLens],
      utf16Lens: [...b.utf16Lens],
      flags: [...b.flags],
    }));
    return map;
  }

  private checkByteOffset(offset: number): void {
    if (!Number.isInteger(offset) || offset < 0 || offset > this.totalBytes) {
      throw new RangeError(`byte offset ${offset} out of range [0, ${this.totalBytes}]`);
    }
  }

  private checkUtf16Offset(offset: number): void {
    if (!Number.isInteger(offset) || offset < 0 || offset > this.totalUtf16) {
      throw new RangeError(`UTF-16 offset ${offset} out of range [0, ${this.totalUtf16}]`);
    }
  }

  /** Last block whose byteStart <= offset (blocks never overlap). */
  private blockForByte(offset: number): MapBlock {
    let lo = 0;
    let hi = this.blocks.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (this.blocks[mid].byteStart <= offset) lo = mid;
      else hi = mid - 1;
    }
    return this.blocks[lo];
  }

  /** Last block whose utf16Start <= offset. */
  private blockForUtf16(offset: number): MapBlock {
    let lo = 0;
    let hi = this.blocks.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (this.blocks[mid].utf16Start <= offset) lo = mid;
      else hi = mid - 1;
    }
    return this.blocks[lo];
  }

  private unitAtByte(offset: number): LocatedUnit | null {
    const block = this.blockForByte(offset);
    let bytePos = block.byteStart;
    let utf16Pos = block.utf16Start;
    this.lastScanSize = 0;
    for (let i = 0; i < block.byteLens.length; i++) {
      this.lastScanSize++;
      const byteLen = block.byteLens[i];
      if (offset < bytePos + byteLen) {
        return {
          byteStart: bytePos,
          utf16Start: utf16Pos,
          byteLen,
          utf16Len: block.utf16Lens[i],
          replacement: block.flags[i] === 1,
        };
      }
      bytePos += byteLen;
      utf16Pos += block.utf16Lens[i];
    }
    return null;
  }

  private unitAtUtf16(offset: number): LocatedUnit | null {
    const block = this.blockForUtf16(offset);
    let bytePos = block.byteStart;
    let utf16Pos = block.utf16Start;
    this.lastScanSize = 0;
    for (let i = 0; i < block.utf16Lens.length; i++) {
      this.lastScanSize++;
      const utf16Len = block.utf16Lens[i];
      if (offset < utf16Pos + utf16Len) {
        return {
          byteStart: bytePos,
          utf16Start: utf16Pos,
          byteLen: block.byteLens[i],
          utf16Len,
          replacement: block.flags[i] === 1,
        };
      }
      bytePos += block.byteLens[i];
      utf16Pos += utf16Len;
    }
    return null;
  }

  private *unitsIntersecting(byteStart: number, byteEnd: number): Generator<LocatedUnit> {
    let index = 0;
    // Position on the block containing byteStart, then walk forward.
    let lo = 0;
    let hi = this.blocks.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (this.blocks[mid].byteStart <= byteStart) lo = mid;
      else hi = mid - 1;
    }
    index = lo;
    for (; index < this.blocks.length; index++) {
      const block = this.blocks[index];
      let bytePos = block.byteStart;
      let utf16Pos = block.utf16Start;
      for (let i = 0; i < block.byteLens.length; i++) {
        const byteLen = block.byteLens[i];
        const utf16Len = block.utf16Lens[i];
        if (bytePos >= byteEnd) return;
        if (bytePos + byteLen > byteStart) {
          yield {
            byteStart: bytePos,
            utf16Start: utf16Pos,
            byteLen,
            utf16Len,
            replacement: block.flags[i] === 1,
          };
        }
        bytePos += byteLen;
        utf16Pos += utf16Len;
      }
    }
  }
}
