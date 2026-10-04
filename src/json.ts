/**
 * Minimal JSON parser that records line/column locations for every value
 * and reports duplicate object keys (which JSON.parse silently drops).
 */
import type { MergeIssue, SourceRef } from './types.ts';

export interface JsonLocation {
  line: number;
  column: number;
}

export interface ParsedJson {
  value: unknown;
  /** Dotted path -> location of the key/value in the source text. */
  locations: Map<string, JsonLocation>;
  issues: MergeIssue[];
}

class Parser {
  private pos = 0;
  private line = 1;
  private lineStart = 0;
  private readonly text: string;
  private readonly source: SourceRef;
  readonly locations = new Map<string, JsonLocation>();
  readonly issues: MergeIssue[] = [];

  constructor(text: string, source: SourceRef) {
    this.text = text;
    this.source = source;
  }

  private error(message: string): Error {
    return new Error(`${this.source.name}:${this.line}:${this.column()} ${message}`);
  }

  private column(): number {
    return this.pos - this.lineStart + 1;
  }

  private peek(): string {
    return this.text[this.pos] ?? '';
  }

  private advance(): string {
    const ch = this.text[this.pos] ?? '';
    this.pos += 1;
    if (ch === '\n') {
      this.line += 1;
      this.lineStart = this.pos;
    }
    return ch;
  }

  private skipWhitespace(): void {
    while (this.pos < this.text.length) {
      const ch = this.peek();
      if (ch === ' ' || ch === '\t' || ch === '\n' || ch === '\r') {
        this.advance();
      } else {
        break;
      }
    }
  }

  private expect(ch: string): void {
    if (this.peek() !== ch) {
      throw this.error(`expected '${ch}', found '${this.peek() || 'EOF'}'`);
    }
    this.advance();
  }

  parse(): unknown {
    this.skipWhitespace();
    const value = this.parseValue('');
    this.skipWhitespace();
    if (this.pos < this.text.length) {
      throw this.error(`unexpected trailing content '${this.peek()}'`);
    }
    return value;
  }

  private parseValue(path: string): unknown {
    this.skipWhitespace();
    const ch = this.peek();
    if (ch === '{') return this.parseObject(path);
    if (ch === '[') return this.parseArray(path);
    if (ch === '"') return this.parseString();
    if (ch === 't') return this.parseLiteral('true', true);
    if (ch === 'f') return this.parseLiteral('false', false);
    if (ch === 'n') return this.parseLiteral('null', null);
    return this.parseNumber();
  }

  private parseLiteral(word: string, value: unknown): unknown {
    for (const ch of word) {
      if (this.peek() !== ch) throw this.error(`invalid literal, expected '${word}'`);
      this.advance();
    }
    return value;
  }

  private parseNumber(): number {
    const start = this.pos;
    if (this.peek() === '-') this.advance();
    while (/[0-9]/.test(this.peek())) this.advance();
    if (this.peek() === '.') {
      this.advance();
      while (/[0-9]/.test(this.peek())) this.advance();
    }
    if (this.peek() === 'e' || this.peek() === 'E') {
      this.advance();
      if (this.peek() === '+' || this.peek() === '-') this.advance();
      while (/[0-9]/.test(this.peek())) this.advance();
    }
    const raw = this.text.slice(start, this.pos);
    const num = Number(raw);
    if (raw === '' || Number.isNaN(num)) {
      throw this.error(`invalid number '${raw}'`);
    }
    return num;
  }

  private parseString(): string {
    this.expect('"');
    let out = '';
    for (;;) {
      const ch = this.peek();
      if (ch === '') throw this.error('unterminated string');
      if (ch === '"') {
        this.advance();
        return out;
      }
      if (ch === '\\') {
        this.advance();
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
            const hex = this.text.slice(this.pos, this.pos + 4);
            if (!/^[0-9a-fA-F]{4}$/.test(hex)) throw this.error('invalid \\u escape');
            out += String.fromCharCode(parseInt(hex, 16));
            this.pos += 4;
            break;
          }
          default:
            throw this.error(`invalid escape '\\${esc}'`);
        }
      } else {
        out += this.advance();
      }
    }
  }

  private parseObject(path: string): Record<string, unknown> {
    this.expect('{');
    const obj: Record<string, unknown> = {};
    const seen = new Map<string, JsonLocation>();
    this.skipWhitespace();
    if (this.peek() === '}') {
      this.advance();
      return obj;
    }
    for (;;) {
      this.skipWhitespace();
      const keyLine = this.line;
      const keyColumn = this.column();
      if (this.peek() !== '"') throw this.error(`expected object key, found '${this.peek()}'`);
      const key = this.parseString();
      const childPath = path === '' ? key : `${path}.${key}`;
      const prior = seen.get(key);
      if (prior !== undefined) {
        this.issues.push({
          severity: 'error',
          code: 'duplicate-key',
          path: childPath,
          message:
            `duplicate key '${key}': first defined at ${this.source.name}:${prior.line}:${prior.column}, ` +
            `redefined at ${this.source.name}:${keyLine}:${keyColumn} (later value wins)`,
          source: {
            ...this.source,
            location: `${this.source.name}:${keyLine}:${keyColumn}`,
          },
        });
      } else {
        seen.set(key, { line: keyLine, column: keyColumn });
      }
      this.locations.set(childPath, { line: keyLine, column: keyColumn });
      this.skipWhitespace();
      this.expect(':');
      obj[key] = this.parseValue(childPath);
      this.skipWhitespace();
      const ch = this.peek();
      if (ch === ',') {
        this.advance();
        continue;
      }
      if (ch === '}') {
        this.advance();
        return obj;
      }
      throw this.error(`expected ',' or '}', found '${ch || 'EOF'}'`);
    }
  }

  private parseArray(path: string): unknown[] {
    this.expect('[');
    const arr: unknown[] = [];
    this.skipWhitespace();
    if (this.peek() === ']') {
      this.advance();
      return arr;
    }
    let index = 0;
    for (;;) {
      this.skipWhitespace();
      const itemPath = `${path}[${index}]`;
      this.locations.set(itemPath, { line: this.line, column: this.column() });
      arr.push(this.parseValue(itemPath));
      index += 1;
      this.skipWhitespace();
      const ch = this.peek();
      if (ch === ',') {
        this.advance();
        continue;
      }
      if (ch === ']') {
        this.advance();
        return arr;
      }
      throw this.error(`expected ',' or ']', found '${ch || 'EOF'}'`);
    }
  }
}

export function parseJson(text: string, source: SourceRef): ParsedJson {
  const parser = new Parser(text, source);
  const value = parser.parse();
  return { value, locations: parser.locations, issues: parser.issues };
}
