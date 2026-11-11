/**
 * Strict JSON parser used as the single entry point for untrusted
 * configuration text.
 *
 * Security properties:
 *  - object nodes are created with a null prototype, so keys such as
 *    "__proto__" can never touch a prototype during parsing;
 *  - duplicate object keys are rejected outright;
 *  - nesting depth is bounded so deeply nested payloads cannot exhaust the
 *    call stack;
 *  - only grammar-valid JSON is accepted (no eval, no lenient parsing).
 */

export type JsonScalar = string | number | boolean | null;
export type JsonRecord = { [key: string]: JsonValue };
export type JsonArray = JsonValue[];
export type JsonValue = JsonScalar | JsonRecord | JsonArray;

export interface ParseOptions {
  /** Maximum allowed container nesting depth. Defaults to 32. */
  maxDepth?: number;
  /** Label used in errors and provenance. Defaults to "<json-text>". */
  sourceName?: string;
}

export const DEFAULT_MAX_DEPTH = 32;

export class JsonParseError extends Error {
  readonly offset: number;
  readonly line: number;
  readonly column: number;
  readonly sourceName: string;

  constructor(
    message: string,
    offset: number,
    line: number,
    column: number,
    sourceName: string,
  ) {
    const prefix = sourceName ? `${sourceName}: ` : '';
    super(`${prefix}${message} (line ${line}, column ${column})`);
    this.name = 'JsonParseError';
    this.offset = offset;
    this.line = line;
    this.column = column;
    this.sourceName = sourceName;
  }
}

/**
 * Branded wrapper around a tree that was produced by {@link ParsedTree.parse}.
 * The constructor is module-private, so callers cannot forge a plain object
 * and pretend it came from JSON text.
 */
export class ParsedTree {
  readonly root: JsonValue;
  readonly sourceName: string;

  private constructor(root: JsonValue, sourceName: string) {
    this.root = root;
    this.sourceName = sourceName;
  }

  static parse(text: string, options: ParseOptions = {}): ParsedTree {
    if (typeof text !== 'string') {
      throw new TypeError('JSON input must be a string of JSON text');
    }
    const maxDepth = options.maxDepth ?? DEFAULT_MAX_DEPTH;
    if (!Number.isInteger(maxDepth) || maxDepth < 0) {
      throw new TypeError('maxDepth must be a non-negative integer');
    }
    const sourceName = options.sourceName ?? '<json-text>';
    const parser = new Parser(text, maxDepth, sourceName);
    const root = parser.parseValue(0);
    parser.skipWhitespace();
    if (parser.position !== text.length) {
      throw parser.error('Unexpected trailing data after JSON value');
    }
    return new ParsedTree(root, sourceName);
  }
}

/** Convenience functional wrapper around {@link ParsedTree.parse}. */
export function parseJsonTree(text: string, options: ParseOptions = {}): ParsedTree {
  return ParsedTree.parse(text, options);
}

const NUMBER_PATTERN = /-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/y;
const WHITESPACE = new Set([0x20, 0x09, 0x0a, 0x0d]);
const ESCAPES: Record<string, string> = {
  '"': '"',
  '\\': '\\',
  '/': '/',
  b: '\b',
  f: '\f',
  n: '\n',
  r: '\r',
  t: '\t',
};

class Parser {
  position = 0;
  readonly text: string;
  readonly maxDepth: number;
  readonly sourceName: string;

  constructor(
    text: string,
    maxDepth: number,
    sourceName: string,
  ) {
    this.text = text;
    this.maxDepth = maxDepth;
    this.sourceName = sourceName;
  }

  error(message: string, offset: number = this.position): JsonParseError {
    let line = 1;
    let column = 1;
    for (let index = 0; index < offset; index += 1) {
      if (this.text.charCodeAt(index) === 0x0a) {
        line += 1;
        column = 1;
      } else {
        column += 1;
      }
    }
    return new JsonParseError(message, offset, line, column, this.sourceName);
  }

  skipWhitespace(): void {
    while (this.position < this.text.length) {
      const code = this.text.charCodeAt(this.position);
      if (!WHITESPACE.has(code)) {
        break;
      }
      this.position += 1;
    }
  }

  parseValue(depth: number): JsonValue {
    this.skipWhitespace();
    if (this.position >= this.text.length) {
      throw this.error('Unexpected end of input');
    }
    const token = this.text[this.position];
    switch (token) {
      case '{':
        return this.parseObject(depth);
      case '[':
        return this.parseArray(depth);
      case '"':
        return this.parseString();
      case 't':
        return this.parseLiteral('true', true);
      case 'f':
        return this.parseLiteral('false', false);
      case 'n':
        return this.parseLiteral('null', null);
      default:
        if (token === '-' || (token >= '0' && token <= '9')) {
          return this.parseNumber();
        }
        throw this.error(`Unexpected token ${JSON.stringify(token)}`);
    }
  }

  private parseLiteral(literal: string, value: JsonScalar): JsonScalar {
    if (!this.text.startsWith(literal, this.position)) {
      throw this.error(`Invalid literal, expected "${literal}"`);
    }
    this.position += literal.length;
    return value;
  }

  private parseNumber(): number {
    NUMBER_PATTERN.lastIndex = this.position;
    const match = NUMBER_PATTERN.exec(this.text);
    if (match === null) {
      throw this.error('Invalid number');
    }
    this.position = NUMBER_PATTERN.lastIndex;
    return Number(match[0]);
  }

  private parseObject(depth: number): JsonRecord {
    if (depth >= this.maxDepth) {
      throw this.error(`Maximum nesting depth of ${this.maxDepth} exceeded`);
    }
    this.position += 1;
    const record: JsonRecord = Object.create(null) as JsonRecord;
    const seenKeys = new Set<string>();
    this.skipWhitespace();
    if (this.text[this.position] === '}') {
      this.position += 1;
      return record;
    }
    for (;;) {
      this.skipWhitespace();
      if (this.text[this.position] !== '"') {
        throw this.error('Expected string key in object');
      }
      const key = this.parseString();
      if (seenKeys.has(key)) {
        throw this.error(`Duplicate JSON key ${JSON.stringify(key)}`);
      }
      seenKeys.add(key);
      this.skipWhitespace();
      if (this.text[this.position] !== ':') {
        throw this.error('Expected ":" after object key');
      }
      this.position += 1;
      record[key] = this.parseValue(depth + 1);
      this.skipWhitespace();
      const separator = this.text[this.position];
      if (separator === ',') {
        this.position += 1;
        continue;
      }
      if (separator === '}') {
        this.position += 1;
        return record;
      }
      throw this.error('Expected "," or "}" in object');
    }
  }

  private parseArray(depth: number): JsonArray {
    if (depth >= this.maxDepth) {
      throw this.error(`Maximum nesting depth of ${this.maxDepth} exceeded`);
    }
    this.position += 1;
    const array: JsonArray = [];
    this.skipWhitespace();
    if (this.text[this.position] === ']') {
      this.position += 1;
      return array;
    }
    for (;;) {
      array.push(this.parseValue(depth + 1));
      this.skipWhitespace();
      const separator = this.text[this.position];
      if (separator === ',') {
        this.position += 1;
        continue;
      }
      if (separator === ']') {
        this.position += 1;
        return array;
      }
      throw this.error('Expected "," or "]" in array');
    }
  }

  private parseString(): string {
    this.position += 1;
    let result = '';
    while (this.position < this.text.length) {
      const char = this.text[this.position];
      this.position += 1;
      if (char === '"') {
        return result;
      }
      if (char.charCodeAt(0) < 0x20) {
        throw this.error('Unescaped control character in string');
      }
      if (char !== '\\') {
        result += char;
        continue;
      }
      const escape = this.text[this.position];
      this.position += 1;
      if (escape === 'u') {
        const codeUnit = this.parseHexEscape();
        if (codeUnit >= 0xd800 && codeUnit <= 0xdbff) {
          if (this.text[this.position] !== '\\' || this.text[this.position + 1] !== 'u') {
            throw this.error('Unpaired UTF-16 high surrogate in string');
          }
          this.position += 2;
          const lowSurrogate = this.parseHexEscape();
          if (lowSurrogate < 0xdc00 || lowSurrogate > 0xdfff) {
            throw this.error('Invalid UTF-16 low surrogate in string');
          }
          result += String.fromCharCode(codeUnit, lowSurrogate);
        } else if (codeUnit >= 0xdc00 && codeUnit <= 0xdfff) {
          throw this.error('Unpaired UTF-16 low surrogate in string');
        } else {
          result += String.fromCharCode(codeUnit);
        }
        continue;
      }
      const mapped = ESCAPES[escape];
      if (mapped === undefined) {
        throw this.error(`Invalid string escape ${JSON.stringify('\\' + escape)}`);
      }
      result += mapped;
    }
    throw this.error('Unterminated string');
  }

  private parseHexEscape(): number {
    const hex = this.text.slice(this.position, this.position + 4);
    if (!/^[0-9a-fA-F]{4}$/.test(hex)) {
      throw this.error('Invalid Unicode escape sequence');
    }
    this.position += 4;
    return parseInt(hex, 16);
  }
}
