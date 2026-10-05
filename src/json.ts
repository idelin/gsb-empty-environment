import { ConfigError } from './errors.ts';
import type { SourceLocation, SourceRef } from './origin.ts';

export interface ParsedJson {
  value: unknown;
  /** Dot-path -> location of every key/value in the document. */
  locations: Map<string, SourceLocation>;
}

/**
 * Strict JSON parser that tracks line/column for every key and rejects
 * duplicate object keys (JSON.parse silently keeps the last one).
 */
export function parseJson(text: string, sourceName: string): ParsedJson {
  const parser = new Parser(text, sourceName);
  parser.skipWs();
  const value = parser.parseValue([]);
  parser.skipWs();
  if (!parser.eof()) {
    throw parser.error(`Unexpected trailing content ${JSON.stringify(parser.peek())}`);
  }
  return { value, locations: parser.locations };
}

class Parser {
  pos = 0;
  line = 1;
  column = 1;
  locations = new Map<string, SourceLocation>();
  private readonly text: string;
  private readonly sourceName: string;

  constructor(text: string, sourceName: string) {
    this.text = text;
    this.sourceName = sourceName;
  }

  eof(): boolean {
    return this.pos >= this.text.length;
  }

  peek(): string {
    return this.text[this.pos] ?? '';
  }

  private advance(): string {
    const ch = this.text[this.pos++];
    if (ch === '\n') {
      this.line++;
      this.column = 1;
    } else {
      this.column++;
    }
    return ch;
  }

  private loc(): SourceLocation {
    return { line: this.line, column: this.column };
  }

  private source(): SourceRef {
    return { kind: 'file', name: this.sourceName, location: this.loc() };
  }

  error(message: string, path: string[] = []): ConfigError {
    return new ConfigError('PARSE_ERROR', message, {
      path: path.join('.'),
      source: this.source(),
    });
  }

  skipWs(): void {
    while (!this.eof() && /[\s]/.test(this.peek())) this.advance();
  }

  private expect(ch: string, path: string[]): void {
    if (this.peek() !== ch) {
      throw this.error(`Expected ${JSON.stringify(ch)} but found ${JSON.stringify(this.peek())}`, path);
    }
    this.advance();
  }

  parseValue(path: string[]): unknown {
    const ch = this.peek();
    if (ch === '{') return this.parseObject(path);
    if (ch === '[') return this.parseArray(path);
    if (ch === '"') return this.parseString(path);
    if (ch === 't') return this.parseLiteral('true', true, path);
    if (ch === 'f') return this.parseLiteral('false', false, path);
    if (ch === 'n') return this.parseLiteral('null', null, path);
    if (ch === '-' || /[0-9]/.test(ch)) return this.parseNumber(path);
    throw this.error(`Unexpected character ${JSON.stringify(ch)}`, path);
  }

  private parseLiteral(word: string, value: unknown, path: string[]): unknown {
    for (const ch of word) this.expect(ch, path);
    return value;
  }

  private parseNumber(path: string[]): number {
    const start = this.pos;
    if (this.peek() === '-') this.advance();
    while (!this.eof() && /[0-9]/.test(this.peek())) this.advance();
    if (this.peek() === '.') {
      this.advance();
      while (!this.eof() && /[0-9]/.test(this.peek())) this.advance();
    }
    if (this.peek() === 'e' || this.peek() === 'E') {
      this.advance();
      if (this.peek() === '+' || this.peek() === '-') this.advance();
      while (!this.eof() && /[0-9]/.test(this.peek())) this.advance();
    }
    const raw = this.text.slice(start, this.pos);
    const num = Number(raw);
    if (Number.isNaN(num)) throw this.error(`Invalid number ${JSON.stringify(raw)}`, path);
    return num;
  }

  private parseString(path: string[]): string {
    this.expect('"', path);
    let out = '';
    while (true) {
      if (this.eof()) throw this.error('Unterminated string', path);
      const ch = this.advance();
      if (ch === '"') return out;
      if (ch === '\\') {
        const esc = this.advance();
        switch (esc) {
          case '"': out += '"'; break;
          case '\\': out += '\\'; break;
          case '/': out += '/'; break;
          case 'b': out += '\b'; break;
          case 'f': out += '\f'; break;
          case 'n': out += '\n'; break;
          case 'r': out += '\r'; break;
          case 't': out += '\t'; break;
          case 'u': {
            let hex = '';
            for (let i = 0; i < 4; i++) hex += this.advance();
            out += String.fromCharCode(parseInt(hex, 16));
            break;
          }
          default:
            throw this.error(`Invalid escape \\${esc}`, path);
        }
      } else {
        out += ch;
      }
    }
  }

  private parseObject(path: string[]): Record<string, unknown> {
    this.expect('{', path);
    const obj: Record<string, unknown> = {};
    const seen = new Map<string, SourceLocation>();
    this.skipWs();
    if (this.peek() === '}') {
      this.advance();
      return obj;
    }
    while (true) {
      this.skipWs();
      const keyLoc = this.loc();
      const key = this.parseString(path);
      const childPath = [...path, key];
      const dotted = childPath.join('.');
      const first = seen.get(key);
      if (first) {
        throw new ConfigError(
          'DUPLICATE_KEY',
          `Duplicate key ${JSON.stringify(key)} (first defined at line ${first.line}, column ${first.column})`,
          { path: dotted, source: { kind: 'file', name: this.sourceName, location: keyLoc } },
        );
      }
      seen.set(key, keyLoc);
      this.locations.set(dotted, keyLoc);
      this.skipWs();
      this.expect(':', childPath);
      this.skipWs();
      obj[key] = this.parseValue(childPath);
      this.skipWs();
      const ch = this.advance();
      if (ch === '}') return obj;
      if (ch !== ',') throw this.error(`Expected "," or "}" but found ${JSON.stringify(ch)}`, childPath);
    }
  }

  private parseArray(path: string[]): unknown[] {
    this.expect('[', path);
    const arr: unknown[] = [];
    this.skipWs();
    if (this.peek() === ']') {
      this.advance();
      return arr;
    }
    let index = 0;
    while (true) {
      this.skipWs();
      const childPath = [...path, String(index)];
      this.locations.set(childPath.join('.'), this.loc());
      arr.push(this.parseValue(childPath));
      this.skipWs();
      const ch = this.advance();
      if (ch === ']') return arr;
      if (ch !== ',') throw this.error(`Expected "," or "]" but found ${JSON.stringify(ch)}`, path);
      index++;
    }
  }
}
