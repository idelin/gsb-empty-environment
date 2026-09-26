/**
 * Incremental, chunk-safe strict JSON parser.
 *
 * Feed it arbitrary pieces (`feed(string | Uint8Array)`) and call `end()`
 * when the stream is finished. Parsing is a pure char-level state machine:
 * a feed may cut at any point - inside a string, inside a \u escape, in the
 * middle of a number or a literal - and the next feed resumes transparently.
 * Uint8Array chunks are decoded with a streaming UTF-8 decoder, so a feed may
 * even cut inside a multi-byte character.
 *
 * Numbers are emitted as exact `Decimal` values, and a number is only
 * published once its terminator (whitespace, ',', ']', '}', or end of input)
 * has been seen - callers never observe a half number.
 */

import { Decimal, DecimalError, DEFAULT_BUDGET } from './decimal.ts';
import type { PrecisionBudget } from './decimal.ts';

export class JsonParseError extends Error {
  readonly offset: number;
  constructor(message: string, offset: number) {
    super(`${message} (at offset ${offset})`);
    this.name = 'JsonParseError';
    this.offset = offset;
  }
}

export type JsonValue =
  | null
  | boolean
  | string
  | Decimal
  | JsonValue[]
  | { [key: string]: JsonValue };

export type ScalarValue = null | boolean | string | Decimal;

export type ParseEvent =
  | { type: 'startObject' }
  | { type: 'endObject' }
  | { type: 'startArray' }
  | { type: 'endArray' }
  | { type: 'key'; key: string }
  | { type: 'value'; value: ScalarValue };

export interface ParserOptions {
  budget?: PrecisionBudget;
}

type ArrayContext = {
  kind: 'array';
  state: 'valueOrEnd' | 'value' | 'commaOrEnd';
};

type ObjectContext = {
  kind: 'object';
  state: 'keyOrEnd' | 'key' | 'colon' | 'value' | 'commaOrEnd';
};

type Context = ArrayContext | ObjectContext;

type Mode = 'value' | 'string' | 'escape' | 'unicode' | 'number' | 'literal';

const WHITESPACE = new Set([' ', '\t', '\n', '\r']);

function isDigit(ch: string): boolean {
  return ch >= '0' && ch <= '9';
}

export class JsonParser {
  private readonly budget: PrecisionBudget;
  private readonly stack: Context[] = [];
  private mode: Mode = 'value';
  private rootDone = false;
  private consumed = 0;

  private stringBuffer = '';
  private stringIsKey = false;
  private unicodeDigits = '';
  private highSurrogate = 0;

  private numberBuffer = '';
  private numberStart = 0;

  private literalBuffer = '';
  private literalStart = 0;

  private decoder: TextDecoder | null = null;

  constructor(options: ParserOptions = {}) {
    this.budget = options.budget ?? DEFAULT_BUDGET;
  }

  feed(chunk: string | Uint8Array): ParseEvent[] {
    let text: string;
    if (typeof chunk === 'string') {
      text = chunk;
    } else {
      this.decoder ??= new TextDecoder('utf-8', { fatal: true });
      text = this.decoder.decode(chunk, { stream: true });
    }
    const events: ParseEvent[] = [];
    let i = 0;
    while (i < text.length) {
      const ch = text[i];
      switch (this.mode) {
        case 'value':
          i = this.stepValue(text, i, events);
          break;
        case 'string':
          i = this.stepString(text, i, events);
          break;
        case 'escape':
          i = this.stepEscape(text, i, events);
          break;
        case 'unicode':
          i = this.stepUnicode(text, i, events);
          break;
        case 'number':
          i = this.stepNumber(text, i, events);
          break;
        case 'literal':
          i = this.stepLiteral(text, i, events);
          break;
      }
    }
    this.consumed += text.length;
    return events;
  }

  /**
   * Flush the stream. A trailing number is finalized at EOF (it is complete
   * there), while an unterminated string, escape, literal or container is an
   * error.
   */
  end(): ParseEvent[] {
    const events: ParseEvent[] = [];
    if (this.decoder !== null) {
      const tail = this.decoder.decode();
      if (tail !== '') events.push(...this.feed(tail));
    }
    if (this.mode === 'number') this.finishNumber(events);
    if (this.mode !== 'value') {
      throw new JsonParseError('unexpected end of input', this.consumed);
    }
    if (this.stack.length > 0) {
      const top = this.stack[this.stack.length - 1];
      throw new JsonParseError(`unclosed ${top.kind} at end of input`, this.consumed);
    }
    if (!this.rootDone) {
      throw new JsonParseError('expected a JSON value but input was empty', this.consumed);
    }
    return events;
  }

  private fail(message: string, index: number): never {
    throw new JsonParseError(message, this.consumed + index);
  }

  private expectingValue(): boolean {
    const top = this.stack[this.stack.length - 1];
    if (top === undefined) return !this.rootDone;
    if (top.kind === 'array') return top.state === 'valueOrEnd' || top.state === 'value';
    return top.state === 'value';
  }

  private stepValue(text: string, i: number, events: ParseEvent[]): number {
    const ch = text[i];

    if (WHITESPACE.has(ch)) return i + 1;
    if (this.rootDone) this.fail('unexpected character after JSON root value', i);

    const top = this.stack[this.stack.length - 1];

    switch (ch) {
      case '{': {
        if (!this.expectingValue()) this.fail("expected ',' or ']'", i);
        this.stack.push({ kind: 'object', state: 'keyOrEnd' });
        events.push({ type: 'startObject' });
        return i + 1;
      }
      case '[': {
        if (!this.expectingValue()) this.fail("expected ',' or ']'", i);
        this.stack.push({ kind: 'array', state: 'valueOrEnd' });
        events.push({ type: 'startArray' });
        return i + 1;
      }
      case '}': {
        if (top !== undefined && top.kind === 'object' &&
            (top.state === 'keyOrEnd' || top.state === 'commaOrEnd')) {
          this.stack.pop();
          events.push({ type: 'endObject' });
          this.afterValue();
          return i + 1;
        }
        this.fail("unexpected '}'", i);
      }
      // fallthrough is structurally impossible; return keeps tsc happy
      // eslint-disable-next-line no-fallthrough
      case ']': {
        if (top !== undefined && top.kind === 'array' &&
            (top.state === 'valueOrEnd' || top.state === 'commaOrEnd')) {
          this.stack.pop();
          events.push({ type: 'endArray' });
          this.afterValue();
          return i + 1;
        }
        this.fail("unexpected ']'", i);
      }
      // eslint-disable-next-line no-fallthrough
      case ',': {
        if (top !== undefined && top.kind === 'array' && top.state === 'commaOrEnd') {
          top.state = 'value';
          return i + 1;
        }
        if (top !== undefined && top.kind === 'object' && top.state === 'commaOrEnd') {
          top.state = 'key';
          return i + 1;
        }
        this.fail("unexpected ','", i);
      }
      // eslint-disable-next-line no-fallthrough
      case ':': {
        if (top !== undefined && top.kind === 'object' && top.state === 'colon') {
          top.state = 'value';
          return i + 1;
        }
        this.fail("unexpected ':'", i);
      }
      // eslint-disable-next-line no-fallthrough
      case '"': {
        this.stringBuffer = '';
        this.highSurrogate = 0;
        this.stringIsKey =
          top !== undefined && top.kind === 'object' &&
          (top.state === 'keyOrEnd' || top.state === 'key');
        if (!this.stringIsKey && !this.expectingValue()) {
          this.fail('unexpected string in this position', i);
        }
        this.mode = 'string';
        return i + 1;
      }
      case '-':
      case '0': case '1': case '2': case '3': case '4':
      case '5': case '6': case '7': case '8': case '9': {
        if (!this.expectingValue()) this.fail('unexpected number in this position', i);
        this.numberBuffer = ch;
        this.numberStart = this.consumed + i;
        this.mode = 'number';
        return i + 1;
      }
      case 't': case 'f': case 'n': {
        if (!this.expectingValue()) this.fail('unexpected literal in this position', i);
        this.literalBuffer = ch;
        this.literalStart = this.consumed + i;
        this.mode = 'literal';
        return i + 1;
      }
      default:
        this.fail(`unexpected character ${JSON.stringify(ch)}`, i);
    }
  }

  private afterValue(): void {
    const top = this.stack[this.stack.length - 1];
    if (top === undefined) {
      this.rootDone = true;
      return;
    }
    top.state = 'commaOrEnd';
  }

  private stepString(text: string, i: number, events: ParseEvent[]): number {
    const ch = text[i];

    if (ch === '"') {
      if (this.highSurrogate !== 0) {
        this.fail('unpaired high surrogate at end of string', i);
      }
      const value = this.stringBuffer;
      this.stringBuffer = '';
      this.mode = 'value';
      if (this.stringIsKey) {
        const top = this.stack[this.stack.length - 1];
        if (top === undefined || top.kind !== 'object') {
          this.fail('string key outside of an object', i);
        }
        top.state = 'colon';
        events.push({ type: 'key', key: value });
      } else {
        events.push({ type: 'value', value });
        this.afterValue();
      }
      return i + 1;
    }

    if (ch === '\\') {
      this.mode = 'escape';
      return i + 1;
    }

    if (this.highSurrogate !== 0) {
      this.fail('expected "\\u" low surrogate after high surrogate', i);
    }

    if (ch.charCodeAt(0) < 0x20) {
      this.fail('unescaped control character in string', i);
    }

    this.stringBuffer += ch;
    return i + 1;
  }

  private stepEscape(text: string, i: number, events: ParseEvent[]): number {
    const ch = text[i];
    if (this.highSurrogate !== 0 && ch !== 'u') {
      this.fail('expected "\\u" low surrogate after high surrogate', i);
    }
    switch (ch) {
      case '"': this.stringBuffer += '"'; break;
      case '\\': this.stringBuffer += '\\'; break;
      case '/': this.stringBuffer += '/'; break;
      case 'b': this.stringBuffer += '\b'; break;
      case 'f': this.stringBuffer += '\f'; break;
      case 'n': this.stringBuffer += '\n'; break;
      case 'r': this.stringBuffer += '\r'; break;
      case 't': this.stringBuffer += '\t'; break;
      case 'u':
        this.unicodeDigits = '';
        this.mode = 'unicode';
        return i + 1;
      default:
        this.fail(`invalid escape sequence \\${ch}`, i);
    }
    this.mode = 'string';
    return i + 1;
  }

  private stepUnicode(text: string, i: number, events: ParseEvent[]): number {
    const ch = text[i];
    if (!/^[0-9a-fA-F]$/.test(ch)) this.fail('invalid \\u hexadecimal escape', i);
    this.unicodeDigits += ch;
    if (this.unicodeDigits.length < 4) return i + 1;

    const code = parseInt(this.unicodeDigits, 16);

    if (this.highSurrogate !== 0) {
      if (code < 0xdc00 || code > 0xdfff) {
        this.fail('expected low surrogate (\\uDC00-\\uDFFF)', i);
      }
      const high = this.highSurrogate - 0xd800;
      const low = code - 0xdc00;
      const codepoint = 0x10000 + ((high << 10) | low);
      this.stringBuffer += String.fromCodePoint(codepoint);
      this.highSurrogate = 0;
    } else if (code >= 0xd800 && code <= 0xdbff) {
      this.highSurrogate = code;
    } else if (code >= 0xdc00 && code <= 0xdfff) {
      this.fail('unpaired low surrogate \\u' + this.unicodeDigits, i);
    } else {
      this.stringBuffer += String.fromCharCode(code);
    }

    this.mode = 'string';
    return i + 1;
  }

  private stepNumber(text: string, i: number, events: ParseEvent[]): number {
    const ch = text[i];
    if (isDigit(ch) || ch === '.' || ch === 'e' || ch === 'E' || ch === '+' || ch === '-') {
      this.numberBuffer += ch;
      return i + 1;
    }
    // A number ends at the first non-number character; that character belongs
    // to the next token and is reprocessed in value mode.
    this.finishNumber(events);
    return i;
  }

  private finishNumber(events: ParseEvent[]): void {
    let value: Decimal;
    try {
      value = Decimal.fromLexeme(this.numberBuffer, this.budget);
    } catch (error) {
      const message = error instanceof DecimalError ? error.message : String(error);
      throw new JsonParseError(message, this.numberStart);
    }
    this.numberBuffer = '';
    this.mode = 'value';
    events.push({ type: 'value', value });
    this.afterValue();
  }

  private stepLiteral(text: string, i: number, events: ParseEvent[]): number {
    this.literalBuffer += text[i];
    const accepted = (['true', 'false', 'null'] as const).some((word) =>
      word.startsWith(this.literalBuffer),
    );
    if (!accepted) {
      throw new JsonParseError(
        `invalid literal ${JSON.stringify(this.literalBuffer)}`,
        this.literalStart,
      );
    }
    if (this.literalBuffer === 'true' || this.literalBuffer === 'false') {
      const value = this.literalBuffer === 'true';
      this.literalBuffer = '';
      this.mode = 'value';
      events.push({ type: 'value', value });
      this.afterValue();
    } else if (this.literalBuffer === 'null') {
      this.literalBuffer = '';
      this.mode = 'value';
      events.push({ type: 'value', value: null });
      this.afterValue();
    }
    return i + 1;
  }
}

/** Parse a complete JSON text into a DOM in which numbers are `Decimal`s. */
export function parseJson(text: string | Uint8Array, options: ParserOptions = {}): JsonValue {
  const parser = new JsonParser(options);
  const events = parser.feed(text);
  events.push(...parser.end());
  return buildDocument(events);
}

interface ObjectFrame {
  object: { [key: string]: JsonValue };
  key: string;
}

function buildDocument(events: ParseEvent[]): JsonValue {
  const frames: Array<JsonValue[] | ObjectFrame> = [];
  let root: JsonValue = null;
  let hasRoot = false;

  const attach = (value: JsonValue): void => {
    const parent = frames[frames.length - 1];
    if (parent === undefined) {
      root = value;
      hasRoot = true;
    } else if (Array.isArray(parent)) {
      parent.push(value);
    } else {
      parent.object[parent.key] = value;
    }
  };

  for (const event of events) {
    switch (event.type) {
      case 'startArray': {
        const array: JsonValue[] = [];
        attach(array);
        frames.push(array);
        break;
      }
      case 'startObject': {
        const frame: ObjectFrame = { object: {}, key: '' };
        attach(frame.object);
        frames.push(frame);
        break;
      }
      case 'key': {
        const frame = frames[frames.length - 1];
        if (!Array.isArray(frame) && frame !== undefined) frame.key = event.key;
        break;
      }
      case 'value':
        attach(event.value);
        break;
      case 'endArray':
      case 'endObject':
        frames.pop();
        break;
    }
  }

  if (!hasRoot) throw new JsonParseError('no JSON value produced', 0);
  return root;
}
