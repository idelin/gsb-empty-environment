/**
 * 零依赖 GraphQL 词法分析器。
 * 支持名称、标点、整数/浮点、普通字符串与块字符串（Block String），
 * 以及逗号/空白/BOM/逗号忽略与行注释跳过。
 */

import type { Token } from './types.ts';
import { TokenKind } from './types.ts';

export class LexError extends Error {
  readonly line: number;
  readonly column: number;

  constructor(message: string, line: number, column: number) {
    super(`${message} (line ${line}, column ${column})`);
    this.name = 'LexError';
    this.line = line;
    this.column = column;
  }
}

const PUNCTUATORS = new Set([
  '!',
  '$',
  '(',
  ')',
  '...',
  ':',
  '=',
  '@',
  '[',
  ']',
  '{',
  '}',
  '|',
  '&',
]);

const NAME_CONTINUE = /[A-Za-z0-9_]/;

function isNameStart(code: number): boolean {
  return (
    (code >= 0x41 && code <= 0x5a) ||
    (code >= 0x61 && code <= 0x7a) ||
    code === 0x5f
  );
}

function isNameContinue(code: number): boolean {
  return isNameStart(code) || (code >= 0x30 && code <= 0x39);
}

export class Lexer {
  private pos = 0;
  private line = 1;
  private lineStart = 0;

  private readonly source: string;

  constructor(source: string) {
    this.source = source;
  }

  tokenize(): Token[] {
    const tokens: Token[] = [];
    for (;;) {
      const token = this.nextToken();
      tokens.push(token);
      if (token.kind === TokenKind.EOF) {
        return tokens;
      }
    }
  }

  private columnAt(offset: number): number {
    return offset - this.lineStart + 1;
  }

  private error(message: string): never {
    throw new LexError(message, this.line, this.columnAt(this.pos));
  }

  private peek(offset = 0): string {
    return this.source[this.pos + offset] ?? '';
  }

  private advance(): string {
    const char = this.source[this.pos];
    this.pos += 1;
    if (char === '\n') {
      this.line += 1;
      this.lineStart = this.pos;
    }
    return char;
  }

  private skipIgnored(): void {
    for (;;) {
      const char = this.peek();
      if (char === ' ' || char === '\t' || char === '\r' || char === '\n' || char === ',') {
        this.advance();
      } else if (char === '\uFEFF' && this.pos === 0) {
        this.advance();
      } else if (char === '#') {
        while (this.peek() !== '' && this.peek() !== '\n' && this.peek() !== '\r') {
          this.advance();
        }
      } else {
        return;
      }
    }
  }

  private nextToken(): Token {
    this.skipIgnored();
    const start = this.pos;
    const line = this.line;
    const column = this.columnAt(start);
    const char = this.peek();

    if (char === '') {
      return { kind: TokenKind.EOF, value: '', start: this.pos, end: this.pos, line, column };
    }

    if (char === '.') {
      if (this.peek(1) === '.' && this.peek(2) === '.') {
        this.pos += 3;
        return this.token(TokenKind.PUNCTUATOR, '...', start, line, column);
      }
      this.error('Unexpected "."; did you mean spread "..."');
    }

    if (PUNCTUATORS.has(char)) {
      this.advance();
      return this.token(TokenKind.PUNCTUATOR, char, start, line, column);
    }

    if (isNameStart(char.charCodeAt(0))) {
      return this.readName(start, line, column);
    }

    if (char === '"') {
      return this.readString(start, line, column);
    }

    if (char === '-' || (char >= '0' && char <= '9')) {
      return this.readNumber(start, line, column);
    }

    this.error(`Unexpected character ${JSON.stringify(char)}`);
  }

  private token(
    kind: TokenKind,
    value: string,
    start: number,
    line: number,
    column: number,
  ): Token {
    return { kind, value, start, end: this.pos, line, column };
  }

  private readName(start: number, line: number, column: number): Token {
    this.advance();
    while (this.peek() !== '' && isNameContinue(this.peek().charCodeAt(0))) {
      this.advance();
    }
    return this.token(TokenKind.NAME, this.source.slice(start, this.pos), start, line, column);
  }

  private readNumber(start: number, line: number, column: number): Token {
    let isFloat = false;
    if (this.peek() === '-') {
      this.advance();
    }
    if (this.peek() === '0') {
      this.advance();
    } else if (this.peek() >= '1' && this.peek() <= '9') {
      while (this.peek() >= '0' && this.peek() <= '9') {
        this.advance();
      }
    } else {
      this.error('Invalid number literal');
    }

    if (this.peek() === '.') {
      isFloat = true;
      this.advance();
      if (!(this.peek() >= '0' && this.peek() <= '9')) {
        this.error('Fractional part expected after decimal point');
      }
      while (this.peek() >= '0' && this.peek() <= '9') {
        this.advance();
      }
    }

    if (this.peek() === 'e' || this.peek() === 'E') {
      isFloat = true;
      this.advance();
      if (this.peek() === '+' || this.peek() === '-') {
        this.advance();
      }
      if (!(this.peek() >= '0' && this.peek() <= '9')) {
        this.error('Exponent digits expected');
      }
      while (this.peek() >= '0' && this.peek() <= '9') {
        this.advance();
      }
    }

    if (this.peek() !== '' && NAME_CONTINUE.test(this.peek())) {
      this.error('Invalid number suffix');
    }

    return this.token(
      isFloat ? TokenKind.FLOAT : TokenKind.INT,
      this.source.slice(start, this.pos),
      start,
      line,
      column,
    );
  }

  private readString(start: number, line: number, column: number): Token {
    if (this.peek(1) === '"' && this.peek(2) === '"') {
      return this.readBlockString(start, line, column);
    }
    this.advance(); // opening quote
    let value = '';
    for (;;) {
      const char = this.peek();
      if (char === '') {
        this.error('Unterminated string literal');
      }
      if (char === '\n' || char === '\r') {
        this.error('Unterminated string literal');
      }
      if (char === '"') {
        this.advance();
        return this.token(TokenKind.STRING, value, start, line, column);
      }
      if (char === '\\') {
        value += this.readEscapeSequence();
      } else {
        value += this.advance();
      }
    }
  }

  private readBlockString(start: number, line: number, column: number): Token {
    this.advance();
    this.advance();
    this.advance(); // """
    let raw = '';
    for (;;) {
      if (this.peek() === '') {
        this.error('Unterminated block string literal');
      }
      if (this.peek() === '"' && this.peek(1) === '"' && this.peek(2) === '"') {
        this.advance();
        this.advance();
        this.advance();
        const value = dedentBlockString(raw);
        return this.token(TokenKind.BLOCK_STRING, value, start, line, column);
      }
      if (this.peek() === '\\' && this.peek(1) === '"' && this.peek(2) === '"' && this.peek(3) === '"') {
        raw += '"""';
        this.advance();
        this.advance();
        this.advance();
        this.advance();
      } else {
        raw += this.advance();
      }
    }
  }

  private readEscapeSequence(): string {
    this.advance(); // backslash
    const char = this.advance();
    switch (char) {
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
      case 'u': {
        let hex = '';
        for (let index = 0; index < 4; index += 1) {
          const digit = this.advance();
          if (!/[0-9a-fA-F]/.test(digit)) {
            this.error('Invalid unicode escape sequence');
          }
          hex += digit;
        }
        return String.fromCharCode(parseInt(hex, 16));
      }
      default:
        this.error(`Invalid escape sequence "\\${char}"`);
    }
  }
}

/** 按 GraphQL 规范对块字符串进行公共缩进去除。 */
export function dedentBlockString(raw: string): string {
  const lines = raw.split(/\r\n|[\n\r]/);
  let commonIndent: number | null = null;
  for (let index = 1; index < lines.length; index += 1) {
    const line = lines[index];
    const indent = line.length - line.trimStart().length;
    if (indent < line.length && (commonIndent === null || indent < commonIndent)) {
      commonIndent = indent;
      if (indent === 0) {
        break;
      }
    }
  }
  const dedented =
    commonIndent === null
      ? lines
      : lines.map((line, index) => (index === 0 ? line : line.slice(commonIndent)));
  while (dedented.length > 0 && dedented[0].trim() === '') {
    dedented.shift();
  }
  while (dedented.length > 0 && dedented[dedented.length - 1].trim() === '') {
    dedented.pop();
  }
  return dedented.join('\n');
}
