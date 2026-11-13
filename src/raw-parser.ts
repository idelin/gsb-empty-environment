import { BodyParseError } from './errors.ts';

export type RawNode =
  | { kind: 'object'; entries: Array<{ key: string; value: RawNode }>; index: number }
  | { kind: 'array'; items: RawNode[]; index: number }
  | { kind: 'string'; value: string; index: number }
  | { kind: 'number'; raw: string; index: number }
  | { kind: 'boolean'; value: boolean; index: number }
  | { kind: 'null'; index: number };

/**
 * Strict RFC 8259 parser. Numbers are kept as their original source text so
 * that precision is never lost before the declaration-based conversion pass.
 * Duplicate object keys are rejected during parsing.
 */
export function parseRawJson(text: string): RawNode {
  const parser = new RawParser(text);
  const node = parser.parseValue('');
  parser.skipWhitespace();
  if (!parser.atEnd()) {
    throw new BodyParseError('syntax', 'Unexpected trailing characters', '', parser.position);
  }
  return node;
}

class RawParser {
  private position = 0;
  private readonly text: string;

  constructor(text: string) {
    this.text = text;
  }

  atEnd(): boolean {
    return this.position >= this.text.length;
  }

  parseValue(path: string): RawNode {
    this.skipWhitespace();
    if (this.atEnd()) {
      throw new BodyParseError('syntax', 'Unexpected end of input', path, this.position);
    }
    const char = this.text[this.position];
    switch (char) {
      case '{':
        return this.parseObject(path);
      case '[':
        return this.parseArray(path);
      case '"':
        return this.parseString(path);
      case 't':
      case 'f':
        return this.parseBoolean(path);
      case 'n':
        return this.parseNull(path);
      default:
        if (char === '-' || (char >= '0' && char <= '9')) {
          return this.parseNumber(path);
        }
        throw new BodyParseError('syntax', `Unexpected character '${char}'`, path, this.position);
    }
  }

  private parseObject(path: string): RawNode {
    const index = this.position;
    this.expect('{');
    const entries: Array<{ key: string; value: RawNode }> = [];
    const seenKeys = new Set<string>();
    this.skipWhitespace();
    if (this.peek() === '}') {
      this.position += 1;
      return { kind: 'object', entries, index };
    }
    while (true) {
      this.skipWhitespace();
      if (this.peek() !== '"') {
        throw new BodyParseError('syntax', 'Expected object key string', path, this.position);
      }
      const keyIndex = this.position;
      const key = this.readStringContents(path);
      if (seenKeys.has(key)) {
        throw new BodyParseError(
          'duplicate_key',
          `Duplicate object key '${key}'`,
          joinPath(path, key),
          keyIndex,
        );
      }
      seenKeys.add(key);
      this.skipWhitespace();
      this.expectChar(':', path);
      const value = this.parseValue(joinPath(path, key));
      entries.push({ key, value });
      this.skipWhitespace();
      const next = this.advance();
      if (next === ',') {
        continue;
      }
      if (next === '}') {
        return { kind: 'object', entries, index };
      }
      throw new BodyParseError('syntax', "Expected ',' or '}' in object", path, this.position);
    }
  }

  private parseArray(path: string): RawNode {
    const index = this.position;
    this.expect('[');
    const items: RawNode[] = [];
    this.skipWhitespace();
    if (this.peek() === ']') {
      this.position += 1;
      return { kind: 'array', items, index };
    }
    let itemIndex = 0;
    while (true) {
      items.push(this.parseValue(joinPath(path, String(itemIndex))));
      itemIndex += 1;
      this.skipWhitespace();
      const next = this.advance();
      if (next === ',') {
        continue;
      }
      if (next === ']') {
        return { kind: 'array', items, index };
      }
      throw new BodyParseError('syntax', "Expected ',' or ']' in array", path, this.position);
    }
  }

  private parseString(path: string): RawNode {
    const index = this.position;
    return { kind: 'string', value: this.readStringContents(path), index };
  }

  private readStringContents(path: string): string {
    const start = this.position;
    this.expectChar('"', path);
    let result = '';
    while (true) {
      if (this.atEnd()) {
        throw new BodyParseError('syntax', 'Unterminated string', path, start);
      }
      const char = this.text[this.position];
      if (char === '"') {
        this.position += 1;
        return result;
      }
      if (char === '\\') {
        result += this.readEscape(path);
        continue;
      }
      const code = char.charCodeAt(0);
      if (code <= 0x1f) {
        throw new BodyParseError('syntax', 'Unescaped control character in string', path, this.position);
      }
      result += char;
      this.position += 1;
    }
  }

  private readEscape(path: string): string {
    const escapeIndex = this.position;
    this.position += 1;
    if (this.atEnd()) {
      throw new BodyParseError('syntax', 'Unterminated escape sequence', path, escapeIndex);
    }
    const escape = this.text[this.position];
    this.position += 1;
    switch (escape) {
      case '"':
        return '"';
      case '\\':
        return '\\';
      case '/':
        return '/';
      case 'b':
        return '\b';
      case 'f':
        return '\f';
      case 'n':
        return '\n';
      case 'r':
        return '\r';
      case 't':
        return '\t';
      case 'u':
        return this.readUnicodeEscape(path, escapeIndex);
      default:
        throw new BodyParseError('syntax', `Invalid escape sequence '\\${escape}'`, path, escapeIndex);
    }
  }

  private readUnicodeEscape(path: string, escapeIndex: number): string {
    const high = this.readHex4(path, escapeIndex);
    if (high < 0xd800 || high > 0xdbff) {
      return String.fromCharCode(high);
    }
    if (
      this.text[this.position] !== '\\' ||
      this.text[this.position + 1] !== 'u'
    ) {
      throw new BodyParseError('syntax', 'Expected low surrogate after high surrogate', path, escapeIndex);
    }
    this.position += 2;
    const low = this.readHex4(path, escapeIndex);
    if (low < 0xdc00 || low > 0xdfff) {
      throw new BodyParseError('syntax', 'Invalid Unicode low surrogate', path, escapeIndex);
    }
    return String.fromCharCode(high, low);
  }

  private readHex4(path: string, escapeIndex: number): number {
    let value = 0;
    for (let digitIndex = 0; digitIndex < 4; digitIndex += 1) {
      if (this.atEnd()) {
        throw new BodyParseError('syntax', 'Incomplete Unicode escape', path, escapeIndex);
      }
      const digit = this.text[this.position];
      const parsed = parseHexDigit(digit);
      if (parsed < 0) {
        throw new BodyParseError('syntax', 'Invalid hexadecimal digit in Unicode escape', path, this.position);
      }
      value = value * 16 + parsed;
      this.position += 1;
    }
    return value;
  }

  private parseNumber(path: string): RawNode {
    const index = this.position;
    const start = this.position;
    if (this.peek() === '-') {
      this.position += 1;
    }
    if (this.peek() === '0') {
      this.position += 1;
    } else if (this.peek() >= '1' && this.peek() <= '9') {
      while (this.isDigit(this.peek())) {
        this.position += 1;
      }
    } else {
      throw new BodyParseError('syntax', 'Invalid number', path, index);
    }
    if (this.peek() === '.') {
      this.position += 1;
      if (!this.isDigit(this.peek())) {
        throw new BodyParseError('syntax', 'Expected digit after decimal point', path, this.position);
      }
      while (this.isDigit(this.peek())) {
        this.position += 1;
      }
    }
    if (this.peek() === 'e' || this.peek() === 'E') {
      this.position += 1;
      if (this.peek() === '+' || this.peek() === '-') {
        this.position += 1;
      }
      if (!this.isDigit(this.peek())) {
        throw new BodyParseError('syntax', 'Expected digit in exponent', path, this.position);
      }
      while (this.isDigit(this.peek())) {
        this.position += 1;
      }
    }
    return { kind: 'number', raw: this.text.slice(start, this.position), index };
  }

  private parseBoolean(path: string): RawNode {
    const index = this.position;
    if (this.text.startsWith('true', this.position)) {
      this.position += 4;
      return { kind: 'boolean', value: true, index };
    }
    if (this.text.startsWith('false', this.position)) {
      this.position += 5;
      return { kind: 'boolean', value: false, index };
    }
    throw new BodyParseError('syntax', 'Invalid literal', path, index);
  }

  private parseNull(path: string): RawNode {
    const index = this.position;
    if (this.text.startsWith('null', this.position)) {
      this.position += 4;
      return { kind: 'null', index };
    }
    throw new BodyParseError('syntax', 'Invalid literal', path, index);
  }

  private expectChar(char: string, path: string): void {
    if (this.text[this.position] !== char) {
      throw new BodyParseError('syntax', `Expected '${char}'`, path, this.position);
    }
    this.position += 1;
  }

  private expect(token: string): void {
    this.position += token.length;
  }

  private advance(): string {
    if (this.atEnd()) {
      throw new BodyParseError('syntax', 'Unexpected end of input', '', this.position);
    }
    const char = this.text[this.position];
    this.position += 1;
    return char;
  }

  private peek(): string {
    return this.atEnd() ? '' : this.text[this.position];
  }

  private isDigit(char: string): boolean {
    return char >= '0' && char <= '9';
  }

  skipWhitespace(): void {
    while (!this.atEnd()) {
      const char = this.text[this.position];
      if (char === ' ' || char === '\t' || char === '\n' || char === '\r') {
        this.position += 1;
      } else {
        break;
      }
    }
  }
}

function parseHexDigit(char: string): number {
  if (char >= '0' && char <= '9') {
    return char.charCodeAt(0) - 48;
  }
  if (char >= 'a' && char <= 'f') {
    return char.charCodeAt(0) - 87;
  }
  if (char >= 'A' && char <= 'F') {
    return char.charCodeAt(0) - 55;
  }
  return -1;
}

function joinPath(path: string, segment: string): string {
  if (path === '') {
    return `$.${segment}`;
  }
  if (/^\d+$/.test(segment)) {
    return `${path}[${segment}]`;
  }
  return `${path}.${segment}`;
}
