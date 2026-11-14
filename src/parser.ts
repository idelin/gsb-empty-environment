import { JsonNormalizationError } from "./errors.ts";
import { canonicalizeNumber } from "./number.ts";

export type JsonInput = string | Uint8Array | ArrayBuffer;

export interface ByteRange {
  /** Inclusive UTF-8 byte offset. */
  start: number;
  /** Exclusive UTF-8 byte offset. */
  end: number;
}

export interface ParsedStringToken {
  value: string;
  raw: string;
  start: number;
  end: number;
}

export interface ParsedObjectEntry {
  key: string;
  path: string;
  rawKey: string;
  keyRange: ByteRange;
  value: ParsedNode;
  index: number;
}

export interface ParsedObjectNode extends ByteRange {
  kind: "object";
  entries: ParsedObjectEntry[];
}

export interface ParsedArrayNode extends ByteRange {
  kind: "array";
  items: ParsedNode[];
}

export interface ParsedStringNode extends ByteRange {
  kind: "string";
  value: string;
  raw: string;
}

export interface ParsedNumberNode extends ByteRange {
  kind: "number";
  raw: string;
  canonical: string;
}

export interface ParsedBooleanNode extends ByteRange {
  kind: "boolean";
  value: boolean;
}

export interface ParsedNullNode extends ByteRange {
  kind: "null";
  value: null;
}

export type ParsedNode =
  | ParsedObjectNode
  | ParsedArrayNode
  | ParsedStringNode
  | ParsedNumberNode
  | ParsedBooleanNode
  | ParsedNullNode;

export interface ParseJsonOptions {
  /** Maximum number of nested objects and arrays (the root container is 1). */
  maxDepth?: number;
  /** Maximum request body size in bytes. */
  maxBytes?: number;
}

export interface ParsedJson {
  root: ParsedNode;
  bytes: Uint8Array;
  stats: {
    bytes: number;
    nodes: number;
    maxDepthSeen: number;
  };
}

export const DEFAULT_MAX_DEPTH = 64;
export const DEFAULT_MAX_BYTES = 8 * 1024 * 1024;
const MAX_NUMBER_TOKEN_BYTES = 100_000;

const SIMPLE_KEY_PATTERN = /^[A-Za-z_$][A-Za-z0-9_$]*$/;

export function appendJsonPath(parent: string, key: string): string {
  if (SIMPLE_KEY_PATTERN.test(key)) {
    return `${parent}.${key}`;
  }
  return `${parent}[${quoteJsonString(key)}]`;
}

export function appendArrayPath(parent: string, index: number): string {
  return `${parent}[${index}]`;
}

export function quoteJsonString(value: string): string {
  let output = '"';
  for (let index = 0; index < value.length; ) {
    const codeUnit = value.charCodeAt(index);
    switch (codeUnit) {
      case 0x22:
        output += '\\"';
        index += 1;
        continue;
      case 0x5c:
        output += "\\\\";
        index += 1;
        continue;
      case 0x08:
        output += "\\b";
        index += 1;
        continue;
      case 0x0c:
        output += "\\f";
        index += 1;
        continue;
      case 0x0a:
        output += "\\n";
        index += 1;
        continue;
      case 0x0d:
        output += "\\r";
        index += 1;
        continue;
      case 0x09:
        output += "\\t";
        index += 1;
        continue;
      default:
        break;
    }

    if (codeUnit < 0x20) {
      output += `\\u${codeUnit.toString(16).padStart(4, "0")}`;
      index += 1;
      continue;
    }

    if (codeUnit >= 0xd800 && codeUnit <= 0xdbff) {
      const low =
        index + 1 < value.length ? value.charCodeAt(index + 1) : -1;
      if (low < 0xdc00 || low > 0xdfff) {
        throw new TypeError("Cannot quote a string containing a lone surrogate");
      }
      output += value.slice(index, index + 2);
      index += 2;
      continue;
    }

    if (codeUnit >= 0xdc00 && codeUnit <= 0xdfff) {
      throw new TypeError("Cannot quote a string containing a lone surrogate");
    }

    output += value[index];
    index += 1;
  }
  return `${output}"`;
}

function toBytes(input: JsonInput): Uint8Array {
  if (typeof input === "string") {
    assertWellFormedSurrogates(input);
    return new TextEncoder().encode(input);
  }
  if (input instanceof Uint8Array) {
    return input;
  }
  if (input instanceof ArrayBuffer) {
    return new Uint8Array(input);
  }
  throw new TypeError("JSON input must be a string, Uint8Array, or ArrayBuffer");
}

function assertWellFormedSurrogates(value: string): void {
  for (let index = 0; index < value.length; index += 1) {
    const codeUnit = value.charCodeAt(index);
    if (codeUnit >= 0xd800 && codeUnit <= 0xdbff) {
      const low =
        index + 1 < value.length ? value.charCodeAt(index + 1) : -1;
      if (low < 0xdc00 || low > 0xdfff) {
        throw new JsonNormalizationError(
          "INVALID_SURROGATE",
          "String input contains an unpaired high surrogate",
          { offset: index, path: "$" },
        );
      }
      index += 1;
    } else if (codeUnit >= 0xdc00 && codeUnit <= 0xdfff) {
      throw new JsonNormalizationError(
        "INVALID_SURROGATE",
        "String input contains an unpaired low surrogate",
        { offset: index, path: "$" },
      );
    }
  }
}

function isWhitespace(byte: number): boolean {
  return byte === 0x20 || byte === 0x09 || byte === 0x0a || byte === 0x0d;
}

function isDigit(byte: number): boolean {
  return byte >= 0x30 && byte <= 0x39;
}

function hexValue(byte: number): number {
  if (byte >= 0x30 && byte <= 0x39) {
    return byte - 0x30;
  }
  if (byte >= 0x41 && byte <= 0x46) {
    return byte - 0x41 + 10;
  }
  if (byte >= 0x61 && byte <= 0x66) {
    return byte - 0x61 + 10;
  }
  return -1;
}

function decodeUtf8Slice(
  bytes: Uint8Array,
  start: number,
  end: number,
): string {
  return new TextDecoder("utf-8", { fatal: true }).decode(
    bytes.subarray(start, end),
  );
}

class JsonParser {
  readonly bytes: Uint8Array;
  readonly maxDepth: number;
  position = 0;
  nodes = 0;
  maxDepthSeen = 0;

  constructor(bytes: Uint8Array, maxDepth: number) {
    this.bytes = bytes;
    this.maxDepth = maxDepth;
  }

  parse(): ParsedNode {
    this.skipWhitespace();
    if (this.position >= this.bytes.length) {
      throw new JsonNormalizationError(
        "EMPTY_INPUT",
        "Request body is empty",
        { offset: this.position, path: "$" },
      );
    }

    const root = this.parseValue("$", 0);
    this.skipWhitespace();
    if (this.position !== this.bytes.length) {
      throw new JsonNormalizationError(
        "TRAILING_DATA",
        "Unexpected data after the JSON value",
        { offset: this.position, path: "$" },
      );
    }
    return root;
  }

  private parseValue(path: string, depth: number): ParsedNode {
    this.skipWhitespace();
    if (this.position >= this.bytes.length) {
      throw new JsonNormalizationError(
        "UNEXPECTED_EOF",
        "Unexpected end of JSON input",
        { offset: this.position, path },
      );
    }

    const byte = this.bytes[this.position];
    if (byte === 0x7b) {
      return this.parseObject(path, depth + 1);
    }
    if (byte === 0x5b) {
      return this.parseArray(path, depth + 1);
    }
    if (byte === 0x22) {
      return this.parseStringNode(path);
    }
    if (byte === 0x74) {
      return this.parseLiteral("true", true, path);
    }
    if (byte === 0x66) {
      return this.parseLiteral("false", false, path);
    }
    if (byte === 0x6e) {
      return this.parseLiteral("null", null, path);
    }
    if (byte === 0x2d || isDigit(byte)) {
      return this.parseNumber(path);
    }

    throw new JsonNormalizationError(
      "UNEXPECTED_TOKEN",
      `Unexpected byte 0x${byte.toString(16).padStart(2, "0")}`,
      { offset: this.position, path },
    );
  }

  private parseObject(path: string, depth: number): ParsedObjectNode {
    const start = this.position;
    this.assertDepth(depth, path, start);
    this.nodes += 1;
    this.position += 1;
    this.skipWhitespace();

    const entries: ParsedObjectEntry[] = [];
    if (this.takeIf(0x7d)) {
      return {
        kind: "object",
        start,
        end: this.position,
        entries,
      };
    }

    while (true) {
      this.skipWhitespace();
      if (this.position >= this.bytes.length) {
        throw new JsonNormalizationError(
          "UNEXPECTED_EOF",
          "Unterminated object",
          { offset: this.position, path },
        );
      }
      if (this.bytes[this.position] !== 0x22) {
        throw new JsonNormalizationError(
          "EXPECTED_KEY",
          "Object keys must be JSON strings",
          { offset: this.position, path },
        );
      }

      const keyToken = this.parseStringToken(path);
      const valuePath = appendJsonPath(path, keyToken.value);
      this.skipWhitespace();
      if (!this.takeIf(0x3a)) {
        throw new JsonNormalizationError(
          "EXPECTED_COLON",
          "Expected ':' after object key",
          { offset: this.position, path: valuePath },
        );
      }

      const value = this.parseValue(valuePath, depth);
      entries.push({
        key: keyToken.value,
        path: valuePath,
        rawKey: keyToken.raw,
        keyRange: { start: keyToken.start, end: keyToken.end },
        value,
        index: entries.length,
      });

      this.skipWhitespace();
      if (this.takeIf(0x2c)) {
        this.skipWhitespace();
        if (this.position < this.bytes.length && this.bytes[this.position] === 0x7d) {
          throw new JsonNormalizationError(
            "TRAILING_COMMA",
            "Trailing commas are not valid JSON",
            { offset: this.position, path },
          );
        }
        continue;
      }
      if (this.takeIf(0x7d)) {
        return {
          kind: "object",
          start,
          end: this.position,
          entries,
        };
      }
      throw new JsonNormalizationError(
        "EXPECTED_COMMA_OR_END",
        "Expected ',' or '}' in object",
        { offset: this.position, path },
      );
    }
  }

  private parseArray(path: string, depth: number): ParsedArrayNode {
    const start = this.position;
    this.assertDepth(depth, path, start);
    this.nodes += 1;
    this.position += 1;
    this.skipWhitespace();

    const items: ParsedNode[] = [];
    if (this.takeIf(0x5d)) {
      return {
        kind: "array",
        start,
        end: this.position,
        items,
      };
    }

    while (true) {
      const itemPath = appendArrayPath(path, items.length);
      items.push(this.parseValue(itemPath, depth));
      this.skipWhitespace();

      if (this.takeIf(0x2c)) {
        this.skipWhitespace();
        if (this.position < this.bytes.length && this.bytes[this.position] === 0x5d) {
          throw new JsonNormalizationError(
            "TRAILING_COMMA",
            "Trailing commas are not valid JSON",
            { offset: this.position, path },
          );
        }
        continue;
      }
      if (this.takeIf(0x5d)) {
        return {
          kind: "array",
          start,
          end: this.position,
          items,
        };
      }
      throw new JsonNormalizationError(
        "EXPECTED_COMMA_OR_END",
        "Expected ',' or ']' in array",
        { offset: this.position, path },
      );
    }
  }

  private parseStringNode(path: string): ParsedStringNode {
    const token = this.parseStringToken(path);
    this.nodes += 1;
    return {
      kind: "string",
      start: token.start,
      end: token.end,
      value: token.value,
      raw: token.raw,
    };
  }

  private parseStringToken(path: string): ParsedStringToken {
    const start = this.position;
    if (!this.takeIf(0x22)) {
      throw new JsonNormalizationError(
        "UNEXPECTED_TOKEN",
        "Expected a JSON string",
        { offset: start, path },
      );
    }

    let value = "";
    while (this.position < this.bytes.length) {
      const byte = this.bytes[this.position];
      if (byte === 0x22) {
        this.position += 1;
        this.validateSurrogates(value, start, path);
        return {
          value,
          raw: decodeUtf8Slice(this.bytes, start, this.position),
          start,
          end: this.position,
        };
      }
      if (byte === 0x5c) {
        value += this.parseEscape(path);
        continue;
      }
      if (byte < 0x20) {
        throw new JsonNormalizationError(
          "INVALID_CHARACTER",
          "Unescaped control character in string",
          { offset: this.position, path },
        );
      }
      value += this.readUtf8CodePoint(path);
    }

    throw new JsonNormalizationError(
      "UNEXPECTED_EOF",
      "Unterminated string",
      { offset: this.position, path },
    );
  }

  private parseEscape(path: string): string {
    const escapeStart = this.position;
    this.position += 1;
    if (this.position >= this.bytes.length) {
      throw new JsonNormalizationError(
        "UNEXPECTED_EOF",
        "Unterminated string escape",
        { offset: this.position, path },
      );
    }
    const escaped = this.bytes[this.position];
    this.position += 1;

    switch (escaped) {
      case 0x22:
        return '"';
      case 0x5c:
        return "\\";
      case 0x2f:
        return "/";
      case 0x62:
        return "\b";
      case 0x66:
        return "\f";
      case 0x6e:
        return "\n";
      case 0x72:
        return "\r";
      case 0x74:
        return "\t";
      case 0x75: {
        let codeUnit = 0;
        for (let index = 0; index < 4; index += 1) {
          if (this.position >= this.bytes.length) {
            throw new JsonNormalizationError(
              "UNEXPECTED_EOF",
              "Incomplete \\u escape",
              { offset: this.position, path },
            );
          }
          const digit = hexValue(this.bytes[this.position]);
          if (digit < 0) {
            throw new JsonNormalizationError(
              "INVALID_ESCAPE",
              "Invalid hexadecimal digit in \\u escape",
              { offset: this.position, path },
            );
          }
          codeUnit = codeUnit * 16 + digit;
          this.position += 1;
        }
        return String.fromCharCode(codeUnit);
      }
      default:
        throw new JsonNormalizationError(
          "INVALID_ESCAPE",
          `Invalid string escape \\${String.fromCharCode(escaped)}`,
          { offset: escapeStart, path },
        );
    }
  }

  private readUtf8CodePoint(path: string): string {
    const start = this.position;
    const first = this.bytes[this.position];
    if (first < 0x80) {
      this.position += 1;
      return String.fromCharCode(first);
    }

    let length: number;
    let codePoint: number;
    if (first >= 0xc2 && first <= 0xdf) {
      length = 2;
      codePoint = first & 0x1f;
    } else if (first >= 0xe0 && first <= 0xef) {
      length = 3;
      codePoint = first & 0x0f;
    } else if (first >= 0xf0 && first <= 0xf4) {
      length = 4;
      codePoint = first & 0x07;
    } else {
      throw new JsonNormalizationError(
        "INVALID_UTF8",
        "Invalid UTF-8 leading byte",
        { offset: start, path },
      );
    }

    if (this.position + length > this.bytes.length) {
      throw new JsonNormalizationError(
        "INVALID_UTF8",
        "Truncated UTF-8 sequence",
        { offset: start, path },
      );
    }

    for (let index = 1; index < length; index += 1) {
      const continuation = this.bytes[this.position + index];
      if ((continuation & 0xc0) !== 0x80) {
        throw new JsonNormalizationError(
          "INVALID_UTF8",
          "Invalid UTF-8 continuation byte",
          { offset: this.position + index, path },
        );
      }
      codePoint = (codePoint << 6) | (continuation & 0x3f);
    }

    if (
      (length === 2 && codePoint < 0x80) ||
      (length === 3 && codePoint < 0x800) ||
      (length === 4 && codePoint < 0x10000)
    ) {
      throw new JsonNormalizationError(
        "INVALID_UTF8",
        "Overlong UTF-8 sequence",
        { offset: start, path },
      );
    }
    if (codePoint >= 0xd800 && codePoint <= 0xdfff) {
      throw new JsonNormalizationError(
        "INVALID_UTF8",
        "UTF-8 cannot encode a surrogate code point",
        { offset: start, path },
      );
    }
    if (codePoint > 0x10ffff) {
      throw new JsonNormalizationError(
        "INVALID_UTF8",
        "UTF-8 code point is above U+10FFFF",
        { offset: start, path },
      );
    }

    this.position += length;
    return String.fromCodePoint(codePoint);
  }

  private validateSurrogates(
    value: string,
    stringStart: number,
    path: string,
  ): void {
    for (let index = 0; index < value.length; index += 1) {
      const codeUnit = value.charCodeAt(index);
      if (codeUnit >= 0xd800 && codeUnit <= 0xdbff) {
        const low =
          index + 1 < value.length ? value.charCodeAt(index + 1) : -1;
        if (low < 0xdc00 || low > 0xdfff) {
          throw new JsonNormalizationError(
            "INVALID_SURROGATE",
            "String contains an unpaired high surrogate",
            { offset: stringStart, path },
          );
        }
        index += 1;
      } else if (codeUnit >= 0xdc00 && codeUnit <= 0xdfff) {
        throw new JsonNormalizationError(
          "INVALID_SURROGATE",
          "String contains an unpaired low surrogate",
          { offset: stringStart, path },
        );
      }
    }
  }

  private parseNumber(path: string): ParsedNumberNode {
    const start = this.position;
    if (this.bytes[this.position] === 0x2d) {
      this.position += 1;
    }
    if (this.position >= this.bytes.length) {
      throw new JsonNormalizationError(
        "INVALID_NUMBER",
        "Incomplete JSON number",
        { offset: this.position, path },
      );
    }

    const first = this.bytes[this.position];
    if (first === 0x30) {
      this.position += 1;
      if (
        this.position < this.bytes.length &&
        isDigit(this.bytes[this.position])
      ) {
        throw new JsonNormalizationError(
          "INVALID_NUMBER",
          "Leading zeroes are not allowed in JSON numbers",
          { offset: this.position, path },
        );
      }
    } else if (first >= 0x31 && first <= 0x39) {
      this.position += 1;
      while (
        this.position < this.bytes.length &&
        isDigit(this.bytes[this.position])
      ) {
        this.position += 1;
      }
    } else {
      throw new JsonNormalizationError(
        "INVALID_NUMBER",
        "Expected a digit in JSON number",
        { offset: this.position, path },
      );
    }

    if (
      this.position < this.bytes.length &&
      this.bytes[this.position] === 0x2e
    ) {
      this.position += 1;
      if (
        this.position >= this.bytes.length ||
        !isDigit(this.bytes[this.position])
      ) {
        throw new JsonNormalizationError(
          "INVALID_NUMBER",
          "A fractional digit is required after '.'",
          { offset: this.position, path },
        );
      }
      while (
        this.position < this.bytes.length &&
        isDigit(this.bytes[this.position])
      ) {
        this.position += 1;
      }
    }

    if (
      this.position < this.bytes.length &&
      (this.bytes[this.position] === 0x65 ||
        this.bytes[this.position] === 0x45)
    ) {
      this.position += 1;
      if (
        this.position < this.bytes.length &&
        (this.bytes[this.position] === 0x2b ||
          this.bytes[this.position] === 0x2d)
      ) {
        this.position += 1;
      }
      if (
        this.position >= this.bytes.length ||
        !isDigit(this.bytes[this.position])
      ) {
        throw new JsonNormalizationError(
          "INVALID_NUMBER",
          "An exponent digit is required",
          { offset: this.position, path },
        );
      }
      while (
        this.position < this.bytes.length &&
        isDigit(this.bytes[this.position])
      ) {
        this.position += 1;
      }
    }

    if (this.position - start > MAX_NUMBER_TOKEN_BYTES) {
      throw new JsonNormalizationError(
        "INVALID_NUMBER",
        `JSON number token exceeds ${MAX_NUMBER_TOKEN_BYTES} bytes`,
        { offset: start, path },
      );
    }
    const raw = decodeUtf8Slice(this.bytes, start, this.position);
    this.nodes += 1;
    return {
      kind: "number",
      start,
      end: this.position,
      raw,
      canonical: canonicalizeNumber(raw),
    };
  }

  private parseLiteral(
    literal: string,
    value: boolean | null,
    path: string,
  ): ParsedBooleanNode | ParsedNullNode {
    const start = this.position;
    for (let index = 0; index < literal.length; index += 1) {
      if (
        this.position + index >= this.bytes.length ||
        this.bytes[this.position + index] !== literal.charCodeAt(index)
      ) {
        throw new JsonNormalizationError(
          "UNEXPECTED_TOKEN",
          `Invalid literal, expected ${literal}`,
          { offset: start, path },
        );
      }
    }
    this.position += literal.length;
    this.nodes += 1;

    if (value === null) {
      return {
        kind: "null",
        start,
        end: this.position,
        value: null,
      };
    }
    return {
      kind: "boolean",
      start,
      end: this.position,
      value,
    };
  }

  private assertDepth(depth: number, path: string, offset: number): void {
    if (depth > this.maxDepth) {
      throw new JsonNormalizationError(
        "DEPTH_EXCEEDED",
        `JSON nesting depth ${depth} exceeds the limit of ${this.maxDepth}`,
        { offset, path },
      );
    }
    if (depth > this.maxDepthSeen) {
      this.maxDepthSeen = depth;
    }
  }

  private skipWhitespace(): void {
    while (
      this.position < this.bytes.length &&
      isWhitespace(this.bytes[this.position])
    ) {
      this.position += 1;
    }
  }

  private takeIf(byte: number): boolean {
    if (this.position < this.bytes.length && this.bytes[this.position] === byte) {
      this.position += 1;
      return true;
    }
    return false;
  }
}

export function parseJsonOnce(
  input: JsonInput,
  options: ParseJsonOptions = {},
): ParsedJson {
  const bytes = toBytes(input);
  const maxBytes = options.maxBytes ?? DEFAULT_MAX_BYTES;
  if (!Number.isInteger(maxBytes) || maxBytes < 1) {
    throw new TypeError("maxBytes must be a positive integer");
  }
  if (bytes.length > maxBytes) {
    throw new JsonNormalizationError(
      "BODY_TOO_LARGE",
      `Request body is ${bytes.length} bytes, limit is ${maxBytes}`,
      { offset: 0, path: "$" },
    );
  }

  try {
    new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    throw new JsonNormalizationError(
      "INVALID_UTF8",
      "Request body is not valid UTF-8",
      { offset: 0, path: "$" },
    );
  }

  const maxDepth = options.maxDepth ?? DEFAULT_MAX_DEPTH;
  if (!Number.isInteger(maxDepth) || maxDepth < 1) {
    throw new TypeError("maxDepth must be a positive integer");
  }

  const parser = new JsonParser(bytes, maxDepth);
  const root = parser.parse();
  return {
    root,
    bytes,
    stats: {
      bytes: bytes.length,
      nodes: parser.nodes,
      maxDepthSeen: parser.maxDepthSeen,
    },
  };
}
