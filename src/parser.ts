import type {
  PdfDict,
  PdfObject,
  PdfRevision,
  PdfStream,
  XrefEntry,
} from './types.ts';

/** Raised when the raw bytes cannot be interpreted as a valid PDF structure. */
export class PdfFormatError extends Error {
  readonly offset?: number;

  constructor(message: string, offset?: number) {
    super(offset === undefined ? message : `${message} (at byte ${offset})`);
    this.name = 'PdfFormatError';
    this.offset = offset;
  }
}

const WHITESPACE = new Set([0x00, 0x09, 0x0a, 0x0c, 0x0d, 0x20]);
const DELIMITER = new Set([
  0x28, 0x29, 0x3c, 0x3e, 0x5b, 0x5d, 0x7b, 0x7d, 0x2f, 0x25,
]);

function isWhitespace(b: number): boolean {
  return WHITESPACE.has(b);
}

function isDigit(b: number): boolean {
  return b >= 0x30 && b <= 0x39;
}

function decodeText(bytes: Uint8Array): string {
  return new TextDecoder('latin1').decode(bytes);
}

const HEX_VALUE = new Int8Array(256).fill(-1);
for (let i = 0; i < 10; i += 1) HEX_VALUE[0x30 + i] = i;
for (let i = 0; i < 6; i += 1) {
  HEX_VALUE[0x41 + i] = 10 + i;
  HEX_VALUE[0x61 + i] = 10 + i;
}

/**
 * Cursor over an immutable `Uint8Array`. All offsets are absolute byte indices
 * into the original document so callers can correlate parsed objects with the
 * exact region covered by a signature's /ByteRange.
 */
export class ByteReader {
  pos = 0;

  readonly data: Uint8Array;

  constructor(data: Uint8Array) {
    this.data = data;
  }

  get length(): number {
    return this.data.length;
  }

  at(offset: number): number {
    if (offset < 0 || offset >= this.data.length) {
      throw new PdfFormatError('read past end of document', offset);
    }
    return this.data[offset];
  }

  slice(start: number, end: number): Uint8Array {
    return this.data.subarray(start, Math.min(end, this.data.length));
  }

  matchAt(offset: number, token: string): boolean {
    if (offset + token.length > this.data.length) return false;
    for (let i = 0; i < token.length; i += 1) {
      if (this.data[offset + i] !== token.charCodeAt(i)) return false;
    }
    return true;
  }

  skipWhitespace(): void {
    while (this.pos < this.data.length) {
      const b = this.data[this.pos];
      if (b === 0x25) {
        while (this.pos < this.data.length && this.data[this.pos] !== 0x0a) {
          this.pos += 1;
        }
        continue;
      }
      if (!isWhitespace(b)) break;
      this.pos += 1;
    }
  }

  readInteger(): number {
    this.skipWhitespace();
    const start = this.pos;
    if (
      this.pos < this.data.length &&
      (this.data[this.pos] === 0x2b || this.data[this.pos] === 0x2d)
    ) {
      this.pos += 1;
    }
    const digitsStart = this.pos;
    while (this.pos < this.data.length && isDigit(this.data[this.pos])) this.pos += 1;
    if (this.pos === digitsStart) {
      throw new PdfFormatError('expected integer', start);
    }
    return parseInt(decodeText(this.slice(start, this.pos)), 10);
  }

  readKeyword(): string {
    this.skipWhitespace();
    const start = this.pos;
    while (this.pos < this.data.length) {
      const b = this.data[this.pos];
      if (isWhitespace(b) || DELIMITER.has(b)) break;
      this.pos += 1;
    }
    return decodeText(this.slice(start, this.pos));
  }
}

function decodeName(raw: string): string {
  let out = '';
  for (let i = 0; i < raw.length; i += 1) {
    const ch = raw[i];
    if (ch === '#' && i + 2 < raw.length) {
      const hi = HEX_VALUE[raw.charCodeAt(i + 1)];
      const lo = HEX_VALUE[raw.charCodeAt(i + 2)];
      if (hi >= 0 && lo >= 0) {
        out += String.fromCharCode(hi * 16 + lo);
        i += 2;
        continue;
      }
    }
    out += ch;
  }
  return out;
}

interface ParsedObject {
  value: PdfObject;
  next: number;
}

/** Fully parses one PDF document and materializes its revision chain. */
export class PdfDocument {
  readonly revisions: PdfRevision[] = [];
  private readonly objectCache = new Map<string, PdfObject>();
  private readonly objectStreamCache = new Map<number, PdfObject[]>();
  readonly headerValid: boolean;

  reader: ByteReader;

  constructor(reader: ByteReader) {
    this.reader = reader;
    this.headerValid = this.checkHeader();
    this.buildRevisionChain();
  }

  get data(): Uint8Array {
    return this.reader.data;
  }

  get latestRevision(): PdfRevision {
    if (this.revisions.length === 0) throw new PdfFormatError('no revisions parsed');
    return this.revisions[this.revisions.length - 1];
  }

  private checkHeader(): boolean {
    const data = this.reader.data;
    if (data.length < 8) return false;
    return (
      data[0] === 0x25 &&
      data[1] === 0x50 && data[2] === 0x44 && data[3] === 0x46 &&
      data[4] === 0x2d
    );
  }

  /** Locate every `startxref` declaration in document order. */
  private findStartXrefs(): number[] {
    const results: number[] = [];
    const token = 'startxref';
    const data = this.reader.data;
    outer: for (let i = 0; i + token.length <= data.length; i += 1) {
      for (let j = 0; j < token.length; j += 1) {
        if (data[i + j] !== token.charCodeAt(j)) continue outer;
      }
      const reader = new ByteReader(data);
      reader.pos = i + token.length;
      results.push(reader.readInteger());
      i += token.length - 1;
    }
    if (results.length === 0) throw new PdfFormatError('no startxref found');
    return results;
  }

  private buildRevisionChain(): void {
    const starts = this.findStartXrefs();
    const newestOffset = starts[starts.length - 1];
    const seen = new Set<number>();
    let currentOffset: number | null = newestOffset;

    while (currentOffset !== null) {
      if (seen.has(currentOffset)) {
        throw new PdfFormatError('cyclic xref /Prev chain', currentOffset);
      }
      seen.add(currentOffset);
      const revision = this.parseRevision(currentOffset);
      this.revisions.push(revision);
      currentOffset = revision.prevOffset;
    }
    this.revisions.reverse();
    this.revisions.forEach((revision, index) => {
      revision.index = index;
    });
  }

  private parseRevision(xrefOffset: number): PdfRevision {
    const reader = this.reader;
    reader.pos = xrefOffset;
    reader.skipWhitespace();
    let entries: Map<number, XrefEntry>;
    let trailer: PdfDict;

    if (reader.matchAt(reader.pos, 'xref')) {
      const after = reader.pos + 4;
      if (after < reader.length && !isWhitespace(reader.data[after])) {
        throw new PdfFormatError('invalid xref keyword', reader.pos);
      }
      reader.pos = after;
      entries = this.parseClassicXref();
      reader.skipWhitespace();
      if (!reader.matchAt(reader.pos, 'trailer')) {
        throw new PdfFormatError('xref table missing trailer', reader.pos);
      }
      reader.pos += 7;
      const parsed = this.parseObjectAt(reader.pos);
      reader.pos = parsed.next;
      if (parsed.value.kind !== 'dict') {
        throw new PdfFormatError('trailer is not a dictionary', xrefOffset);
      }
      trailer = parsed.value;
    } else {
      const parsed = this.parseIndirectAt(reader.pos);
      reader.pos = parsed.next;
      if (parsed.value.kind !== 'stream') {
        throw new PdfFormatError('expected xref table or xref stream', xrefOffset);
      }
      const decoded = this.parseXrefStream(parsed.value);
      entries = decoded.entries;
      trailer = parsed.value.dict;
    }

    const prevEntry = trailer.entries.get('Prev');
    const prevOffset = prevEntry && prevEntry.kind === 'num' ? prevEntry.value : null;
    const revisionEnd = this.findRevisionEnd(reader.pos);
    return { index: -1, xrefOffset, revisionEnd, entries, trailer, prevOffset };
  }

  /** Return the byte offset immediately following this revision's `%%EOF`. */
  private findRevisionEnd(fromPos: number): number {
    const marker = '%%EOF';
    const data = this.reader.data;
    let search = fromPos;
    while (search + marker.length <= data.length) {
      let matched = true;
      for (let j = 0; j < marker.length; j += 1) {
        if (data[search + j] !== marker.charCodeAt(j)) {
          matched = false;
          break;
        }
      }
      if (matched) {
        let found = search + marker.length;
        if (found < data.length && data[found] === 0x0d) found += 1;
        if (found < data.length && data[found] === 0x0a) found += 1;
        return found;
      }
      search += 1;
    }
    throw new PdfFormatError('revision missing %%EOF', fromPos);
  }

  private parseClassicXref(): Map<number, XrefEntry> {
    const reader = this.reader;
    const entries = new Map<number, XrefEntry>();
    reader.skipWhitespace();
    while (true) {
      const subsectionStart = reader.pos;
      const first = reader.readInteger();
      const count = reader.readInteger();
      if (count < 0 || first < 0) {
        throw new PdfFormatError('invalid xref subsection bounds', subsectionStart);
      }
      for (let i = 0; i < count; i += 1) {
        reader.skipWhitespace();
        const lineStart = reader.pos;
        const offset = reader.readInteger();
        const gen = reader.readInteger();
        reader.skipWhitespace();
        if (reader.pos >= reader.length) {
          throw new PdfFormatError('truncated xref entry', lineStart);
        }
        const flag = reader.data[reader.pos];
        reader.pos += 1;
        if (flag !== 0x66 && flag !== 0x6e) {
          throw new PdfFormatError('xref entry must end with f or n', lineStart);
        }
        if (reader.pos < reader.length && reader.data[reader.pos] === 0x0d) reader.pos += 1;
        if (reader.pos < reader.length && reader.data[reader.pos] === 0x0a) reader.pos += 1;
        if (flag === 0x6e) {
          entries.set(first + i, { type: 1, offset, gen });
        } else {
          entries.set(first + i, { type: 0, offset, gen });
        }
      }
      reader.skipWhitespace();
      if (reader.matchAt(reader.pos, 'trailer')) break;
      if (reader.pos >= reader.length) {
        throw new PdfFormatError('xref table not terminated', subsectionStart);
      }
    }
    return entries;
  }

  private parseXrefStream(stream: PdfStream): { entries: Map<number, XrefEntry> } {
    const dict = stream.dict;
    const sizeNum = dict.entries.get('Size');
    const indexObj = dict.entries.get('Index');
    const wObj = dict.entries.get('W');
    if (!sizeNum || sizeNum.kind !== 'num' || !wObj || wObj.kind !== 'array') {
      throw new PdfFormatError('xref stream missing /Size or /W');
    }
    const widths = wObj.items.map((item) => {
      if (item.kind !== 'num') throw new PdfFormatError('invalid /W in xref stream');
      return item.value;
    });
    if (widths.length !== 3 || widths.some((width) => width < 0)) {
      throw new PdfFormatError('xref stream /W must contain three non-negative widths');
    }
    let subsections: Array<[number, number]>;
    if (indexObj && indexObj.kind === 'array') {
      subsections = [];
      for (let i = 0; i + 1 < indexObj.items.length; i += 2) {
        const first = indexObj.items[i];
        const count = indexObj.items[i + 1];
        if (first.kind !== 'num' || count.kind !== 'num') {
          throw new PdfFormatError('invalid /Index in xref stream');
        }
        subsections.push([first.value, count.value]);
      }
    } else {
      subsections = [[0, sizeNum.value]];
    }
    const entries = new Map<number, XrefEntry>();
    const raw = stream.raw;
    let cursor = 0;
    const readField = (width: number): number => {
      if (width === 0) return 0;
      if (cursor + width > raw.length) {
        throw new PdfFormatError('truncated xref stream data');
      }
      let value = 0;
      for (let i = 0; i < width; i += 1) value = value * 256 + raw[cursor + i];
      cursor += width;
      return value;
    };
    for (const [first, count] of subsections) {
      for (let i = 0; i < count; i += 1) {
        const type = readField(widths[0]) as 0 | 1 | 2;
        const fieldTwo = readField(widths[1]);
        const fieldThree = readField(widths[2]);
        if (type === 0) {
          entries.set(first + i, { type: 0, offset: fieldTwo, gen: fieldThree });
        } else if (type === 1) {
          entries.set(first + i, { type: 1, offset: fieldTwo, gen: fieldThree });
        } else if (type === 2) {
          entries.set(first + i, {
            type: 2,
            offset: fieldTwo,
            gen: 0,
            streamIndex: fieldThree,
          });
        } else {
          throw new PdfFormatError(`unknown xref stream entry type ${type}`);
        }
      }
    }
    return { entries };
  }

  /** Parse the indirect object (`N G obj ... endobj`) located at `offset`. */
  parseIndirectAt(offset: number): ParsedObject {
    const reader = this.reader;
    reader.pos = offset;
    const objectNumber = reader.readInteger();
    const generation = reader.readInteger();
    reader.skipWhitespace();
    if (!reader.matchAt(reader.pos, 'obj')) {
      throw new PdfFormatError(`expected obj keyword for object ${objectNumber} ${generation}`, offset);
    }
    reader.pos += 3;
    const inner = this.parseObjectAt(reader.pos);
    reader.pos = inner.next;
    reader.skipWhitespace();
    if (reader.matchAt(reader.pos, 'endobj')) {
      reader.pos += 6;
    }
    return { value: inner.value, next: reader.pos };
  }

  private parseObjectAt(start: number): ParsedObject {
    const reader = this.reader;
    reader.pos = start;
    reader.skipWhitespace();
    if (reader.pos >= reader.length) {
      throw new PdfFormatError('unexpected end of object', start);
    }
    const b = reader.data[reader.pos];

    if (b === 0x3c && reader.pos + 1 < reader.length && reader.data[reader.pos + 1] === 0x3c) {
      return this.parseDictionary();
    }
    if (b === 0x3c) return this.parseHexString();
    if (b === 0x28) return this.parseLiteralString();
    if (b === 0x5b) return this.parseArray();
    if (b === 0x2f) return this.parseName();
    if (isDigit(b) || b === 0x2b || b === 0x2d || b === 0x2e) {
      return this.parseNumberOrRef();
    }
    const keyword = reader.readKeyword();
    if (keyword === 'true') return { value: { kind: 'bool', value: true }, next: reader.pos };
    if (keyword === 'false') return { value: { kind: 'bool', value: false }, next: reader.pos };
    if (keyword === 'null') return { value: { kind: 'null' }, next: reader.pos };
    throw new PdfFormatError(`unexpected token '${keyword}'`, reader.pos);
  }

  private parseName(): ParsedObject {
    const reader = this.reader;
    const start = reader.pos + 1;
    reader.pos = start;
    while (reader.pos < reader.length) {
      const b = reader.data[reader.pos];
      if (isWhitespace(b) || DELIMITER.has(b)) break;
      reader.pos += 1;
    }
    const raw = decodeText(reader.slice(start, reader.pos));
    const value: PdfObject = { kind: 'name', value: decodeName(raw) };
    return { value, next: reader.pos };
  }

  private parseArray(): ParsedObject {
    const reader = this.reader;
    reader.pos += 1;
    const items: PdfObject[] = [];
    while (true) {
      reader.skipWhitespace();
      if (reader.pos >= reader.length) {
        throw new PdfFormatError('unterminated array');
      }
      if (reader.data[reader.pos] === 0x5d) {
        reader.pos += 1;
        break;
      }
      const element = this.parseObjectAt(reader.pos);
      items.push(element.value);
      reader.pos = element.next;
    }
    const value: PdfObject = { kind: 'array', items };
    return { value, next: reader.pos };
  }

  private parseDictionary(): ParsedObject {
    const reader = this.reader;
    const entries = new Map<string, PdfObject>();
    reader.pos += 2;
    while (true) {
      reader.skipWhitespace();
      if (reader.pos + 1 < reader.length &&
        reader.data[reader.pos] === 0x3e && reader.data[reader.pos + 1] === 0x3e) {
        reader.pos += 2;
        break;
      }
      if (reader.pos < reader.length && reader.data[reader.pos] === 0x3e) {
        reader.pos += 1;
        break;
      }
      if (reader.pos >= reader.length || reader.data[reader.pos] !== 0x2f) {
        throw new PdfFormatError('malformed dictionary key', reader.pos);
      }
      const key = this.parseName();
      const value = this.parseObjectAt(key.next);
      if (key.value.kind !== 'name') {
        throw new PdfFormatError('dictionary key is not a name');
      }
      entries.set(key.value.value, value.value);
      reader.pos = value.next;
    }
    let dict: PdfObject = { kind: 'dict', entries };
    let next = reader.pos;
    reader.skipWhitespace();
    if (reader.matchAt(reader.pos, 'stream')) {
      const after = reader.pos + 6;
      if (after < reader.length && reader.data[after] === 0x0d) {
        reader.pos = after + (after + 1 < reader.length && reader.data[after + 1] === 0x0a ? 2 : 1);
      } else if (after < reader.length && reader.data[after] === 0x0a) {
        reader.pos = after + 1;
      } else {
        throw new PdfFormatError('stream keyword not followed by EOL', after);
      }
      const length = this.resolveStreamLength(dict);
      const rawStart = reader.pos;
      const rawEnd = rawStart + length;
      if (rawEnd > reader.length) {
        throw new PdfFormatError('stream extends past end of document', rawStart);
      }
      const raw = reader.slice(rawStart, rawEnd);
      reader.pos = rawEnd;
      reader.skipWhitespace();
      if (reader.matchAt(reader.pos, 'endstream')) reader.pos += 9;
      dict = { kind: 'stream', dict: dict as PdfDict, raw };
      next = reader.pos;
    }
    return { value: dict, next };
  }

  private resolveStreamLength(dict: PdfObject): number {
    if (dict.kind !== 'dict') throw new PdfFormatError('stream header is not a dictionary');
    const lengthObj = dict.entries.get('Length');
    if (!lengthObj) throw new PdfFormatError('stream missing /Length');
    if (lengthObj.kind === 'num') return lengthObj.value;
    if (lengthObj.kind === 'ref') {
      const resolved = this.getObject(lengthObj.num, this.latestRevision.index);
      if (resolved && resolved.kind === 'num') return resolved.value;
    }
    throw new PdfFormatError('unsupported stream /Length reference');
  }

  private parseHexString(): ParsedObject {
    const reader = this.reader;
    reader.pos += 1;
    const bytes: number[] = [];
    let high = -1;
    while (reader.pos < reader.length) {
      const b = reader.data[reader.pos];
      if (b === 0x3e) {
        reader.pos += 1;
        if (high >= 0) bytes.push(high * 16);
        const value: PdfObject = { kind: 'string', value: Uint8Array.from(bytes) };
        return { value, next: reader.pos };
      }
      if (isWhitespace(b)) {
        reader.pos += 1;
        continue;
      }
      const nibble = HEX_VALUE[b];
      if (nibble < 0) throw new PdfFormatError('invalid hex digit', reader.pos);
      if (high < 0) {
        high = nibble;
      } else {
        bytes.push(high * 16 + nibble);
        high = -1;
      }
      reader.pos += 1;
    }
    throw new PdfFormatError('unterminated hex string');
  }

  private parseLiteralString(): ParsedObject {
    const reader = this.reader;
    reader.pos += 1;
    const bytes: number[] = [];
    let depth = 1;
    while (reader.pos < reader.length) {
      const b = reader.data[reader.pos];
      if (b === 0x28) {
        depth += 1;
        bytes.push(b);
        reader.pos += 1;
        continue;
      }
      if (b === 0x29) {
        depth -= 1;
        reader.pos += 1;
        if (depth === 0) {
          const value: PdfObject = { kind: 'string', value: Uint8Array.from(bytes) };
          return { value, next: reader.pos };
        }
        bytes.push(b);
        continue;
      }
      if (b === 0x5c) {
        reader.pos += 1;
        if (reader.pos >= reader.length) break;
        const escaped = reader.data[reader.pos];
        if (escaped === 0x6e) bytes.push(0x0a);
        else if (escaped === 0x72) bytes.push(0x0d);
        else if (escaped === 0x74) bytes.push(0x09);
        else if (escaped === 0x62) bytes.push(0x08);
        else if (escaped === 0x66) bytes.push(0x0c);
        else if (escaped === 0x28) bytes.push(0x28);
        else if (escaped === 0x29) bytes.push(0x29);
        else if (escaped === 0x5c) bytes.push(0x5c);
        else if (isDigit(escaped)) {
          let octal = '';
          for (let i = 0; i < 3 && reader.pos < reader.length && isDigit(reader.data[reader.pos]); i += 1) {
            octal += String.fromCharCode(reader.data[reader.pos]);
            reader.pos += 1;
          }
          bytes.push(parseInt(octal, 8) & 0xff);
          continue;
        } else if (escaped === 0x0d || escaped === 0x0a) {
          if (escaped === 0x0d && reader.pos + 1 < reader.length &&
            reader.data[reader.pos + 1] === 0x0a) {
            reader.pos += 1;
          }
        } else {
          bytes.push(escaped);
        }
        reader.pos += 1;
        continue;
      }
      bytes.push(b);
      reader.pos += 1;
    }
    throw new PdfFormatError('unterminated literal string');
  }

  private parseNumberOrRef(): ParsedObject {
    const reader = this.reader;
    const start = reader.pos;
    const first = reader.readInteger();
    const saved = reader.pos;
    reader.skipWhitespace();
    if (reader.pos < reader.length && isDigit(reader.data[reader.pos])) {
      const second = reader.readInteger();
      reader.skipWhitespace();
      const tokenStart = reader.pos;
      const keyword = reader.readKeyword();
      if (keyword === 'R') {
        const ref: PdfObject = { kind: 'ref', num: first, gen: second };
        return { value: ref, next: reader.pos };
      }
      if (keyword === 'obj') {
        reader.pos = tokenStart;
      }
      reader.pos = saved;
    }
    reader.pos = saved;
    const text = decodeText(reader.slice(start, this.scanNumberEnd(start)));
    const value: PdfObject = { kind: 'num', value: parseFloat(text) };
    reader.pos = start + text.length;
    return { value, next: reader.pos };
  }

  private scanNumberEnd(start: number): number {
    const reader = this.reader;
    let pos = start;
    while (pos < reader.length) {
      const b = reader.data[pos];
      if (isWhitespace(b) || DELIMITER.has(b)) break;
      pos += 1;
    }
    return pos;
  }

  /** Resolve an indirect object exactly as visible in the given revision. */
  getObject(num: number, revisionIndex: number): PdfObject | null {
    const revision = this.revisions[revisionIndex];
    if (!revision) return null;
    const entry = revision.entries.get(num);
    if (!entry || entry.type === 0) return null;
    const cacheKey = `${revisionIndex}:${num}`;
    const cached = this.objectCache.get(cacheKey);
    if (cached) return cached;

    let value: PdfObject;
    if (entry.type === 1) {
      value = this.parseIndirectAt(entry.offset).value;
    } else {
      const members = this.parseObjectStream(entry.offset, revisionIndex);
      const index = entry.streamIndex ?? 0;
      const member = members[index];
      if (!member) {
        throw new PdfFormatError(
          `object ${num} not found at index ${index} of object stream ${entry.offset}`,
        );
      }
      value = member;
    }
    this.objectCache.set(cacheKey, value);
    return value;
  }

  getEntry(num: number, revisionIndex: number): XrefEntry | undefined {
    return this.revisions[revisionIndex]?.entries.get(num);
  }

  private parseObjectStream(streamObjectNum: number, revisionIndex: number): PdfObject[] {
    const cached = this.objectStreamCache.get(streamObjectNum);
    if (cached) return cached;
    const streamObj = this.getObjectDirect(streamObjectNum, revisionIndex);
    if (!streamObj || streamObj.kind !== 'stream') {
      throw new PdfFormatError(`object stream ${streamObjectNum} is not a stream`);
    }
    const dict = streamObj.dict;
    const nObj = dict.entries.get('N');
    const firstObj = dict.entries.get('First');
    if (!nObj || nObj.kind !== 'num' || !firstObj || firstObj.kind !== 'num') {
      throw new PdfFormatError('object stream missing /N or /First');
    }
    const raw = streamObj.raw;
    const reader = new ByteReader(raw);
    const pairs: Array<[number, number]> = [];
    for (let i = 0; i < nObj.value; i += 1) {
      const num = reader.readInteger();
      const relative = reader.readInteger();
      pairs.push([num, relative]);
    }
    const members: PdfObject[] = [];
    for (const [num, relative] of pairs) {
      const absolute = firstObj.value + relative;
      const parsed = this.parseObjectAtWithBase(raw, absolute);
      members[num] = parsed.value;
    }
    this.objectStreamCache.set(streamObjectNum, members);
    return members;
  }

  private getObjectDirect(num: number, revisionIndex: number): PdfObject | null {
    const revision = this.revisions[revisionIndex];
    const entry = revision?.entries.get(num);
    if (!entry || entry.type !== 1) return null;
    return this.parseIndirectAt(entry.offset).value;
  }

  private parseObjectAtWithBase(bytes: Uint8Array, offset: number): ParsedObject {
    const sub = new ByteReader(bytes);
    sub.pos = offset;
    const previousReader = this.reader;
    const savedPos = previousReader.pos;
    this.reader = sub as unknown as ByteReader;
    try {
      return this.parseObjectAt(offset);
    } finally {
      this.reader = previousReader;
      this.reader.pos = savedPos;
    }
  }
}
