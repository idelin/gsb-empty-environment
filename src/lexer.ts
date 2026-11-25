import { DecodeError } from './types.ts';

/**
 * 字节级 JSON 分词器。
 *
 * - 不依赖 TextDecoder，自行增量解码 UTF-8，多字节序列可跨任意切块；
 * - 数字保留原始词法（含前导零/指数等非法形态），交由类型层拒绝；
 * - 字符串在分词期即校验代理项配对，孤立代理立即报 LONE_SURROGATE；
 * - 全部状态为可序列化纯数据，检查点可精确恢复。
 */

export type LexerState =
  | 'value'
  | 'string'
  | 'escape'
  | 'escapeU'
  | 'number'
  | 'literal'
  | 'done';

export interface LexerCheckpoint {
  state: LexerState;
  buf: string;
  lit: string;
  litTarget: string | null;
  escapeBuf: string;
  pendingHighSurrogate: number | null;
  utf8Need: number;
  utf8Code: number;
}

export type Token =
  | { type: 'beginObject' }
  | { type: 'endObject' }
  | { type: 'beginArray' }
  | { type: 'endArray' }
  | { type: 'colon' }
  | { type: 'comma' }
  | { type: 'string'; value: string }
  | { type: 'number'; lexeme: string }
  | { type: 'bool'; value: boolean }
  | { type: 'null' };

const HEX = /^[0-9a-fA-F]$/;
const WS = new Set([0x20, 0x09, 0x0a, 0x0d]);

export class JsonLexer {
  state: LexerState = 'value';
  buf = '';
  lit = '';
  litTarget: string | null = null;
  escapeBuf = '';
  pendingHighSurrogate: number | null = null;
  utf8Need = 0;
  utf8Code = 0;

  checkpoint(): LexerCheckpoint {
    return {
      state: this.state,
      buf: this.buf,
      lit: this.lit,
      litTarget: this.litTarget,
      escapeBuf: this.escapeBuf,
      pendingHighSurrogate: this.pendingHighSurrogate,
      utf8Need: this.utf8Need,
      utf8Code: this.utf8Code,
    };
  }

  restore(cp: LexerCheckpoint): void {
    this.state = cp.state;
    this.buf = cp.buf;
    this.lit = cp.lit;
    this.litTarget = cp.litTarget;
    this.escapeBuf = cp.escapeBuf;
    this.pendingHighSurrogate = cp.pendingHighSurrogate;
    this.utf8Need = cp.utf8Need;
    this.utf8Code = cp.utf8Code;
  }

  push(chunk: Uint8Array): Token[] {
    const tokens: Token[] = [];
    for (let index = 0; index < chunk.length; index++) {
      const byte = chunk[index];
      if (this.utf8Need > 0) {
        this.feedContinuation(byte);
        continue;
      }
      switch (this.state) {
        case 'value':
          this.readValueByte(byte, tokens);
          break;
        case 'string':
          this.readStringByte(byte, tokens);
          break;
        case 'escape':
          this.readEscapeByte(byte);
          break;
        case 'escapeU':
          this.readEscapeUByte(byte);
          break;
        case 'number':
          this.readNumberByte(byte, tokens);
          break;
        case 'literal':
          this.readLiteralByte(byte, tokens);
          break;
        case 'done':
          this.readValueByte(byte, tokens);
          break;
      }
    }
    return tokens;
  }

  finish(): Token[] {
    if (this.utf8Need > 0) {
      throw new DecodeError('TRUNCATED_INPUT', 'incomplete UTF-8 sequence');
    }
    if (this.state === 'string' || this.state === 'escape' || this.state === 'escapeU') {
      throw new DecodeError('TRUNCATED_INPUT', 'unterminated string');
    }
    if (this.state === 'number') {
      const lexeme = this.buf;
      this.buf = '';
      this.state = 'value';
      return [{ type: 'number', lexeme }];
    }
    if (this.state === 'literal') {
      throw new DecodeError('TRUNCATED_INPUT', `incomplete literal ${this.lit}`);
    }
    return [];
  }

  private readValueByte(byte: number, tokens: Token[]): void {
    if (this.state === 'done') {
      this.state = 'value';
    }
    if (WS.has(byte)) {
      return;
    }
    switch (byte) {
      case 0x7b:
        tokens.push({ type: 'beginObject' });
        return;
      case 0x7d:
        tokens.push({ type: 'endObject' });
        return;
      case 0x5b:
        tokens.push({ type: 'beginArray' });
        return;
      case 0x5d:
        tokens.push({ type: 'endArray' });
        return;
      case 0x3a:
        tokens.push({ type: 'colon' });
        return;
      case 0x2c:
        tokens.push({ type: 'comma' });
        return;
      case 0x22:
        this.state = 'string';
        this.buf = '';
        this.pendingHighSurrogate = null;
        return;
      case 0x74:
        this.startLiteral('true', byte);
        return;
      case 0x66:
        this.startLiteral('false', byte);
        return;
      case 0x6e:
        this.startLiteral('null', byte);
        return;
      default:
        if (byte === 0x2d || (byte >= 0x30 && byte <= 0x39)) {
          this.state = 'number';
          this.buf = String.fromCharCode(byte);
          return;
        }
        throw new DecodeError('SYNTAX_ERROR', `unexpected byte 0x${byte.toString(16)}`);
    }
  }

  private startLiteral(target: string, firstByte: number): void {
    this.state = 'literal';
    this.litTarget = target;
    this.lit = String.fromCharCode(firstByte);
  }

  private readLiteralByte(byte: number, tokens: Token[]): void {
    const target = this.litTarget as string;
    const expected = target.charCodeAt(this.lit.length);
    if (byte !== expected) {
      throw new DecodeError('SYNTAX_ERROR', `bad literal starting with ${this.lit}`);
    }
    this.lit += String.fromCharCode(byte);
    if (this.lit.length === target.length) {
      if (target === 'true') {
        tokens.push({ type: 'bool', value: true });
      } else if (target === 'false') {
        tokens.push({ type: 'bool', value: false });
      } else {
        tokens.push({ type: 'null' });
      }
      this.lit = '';
      this.litTarget = null;
      this.state = 'done';
    }
  }

  private readNumberByte(byte: number, tokens: Token[]): void {
    const isDigit = byte >= 0x30 && byte <= 0x39;
    const isNumberChar =
      isDigit || byte === 0x2d || byte === 0x2b || byte === 0x2e || byte === 0x65 || byte === 0x45;
    if (isNumberChar) {
      this.buf += String.fromCharCode(byte);
      return;
    }
    if (WS.has(byte) || byte === 0x7d || byte === 0x5d || byte === 0x2c) {
      const lexeme = this.buf;
      this.buf = '';
      this.state = 'done';
      tokens.push({ type: 'number', lexeme });
      this.readValueByte(byte, tokens);
      return;
    }
    throw new DecodeError('SYNTAX_ERROR', `bad number byte 0x${byte.toString(16)}`);
  }

  private readStringByte(byte: number, tokens: Token[]): void {
    if (byte === 0x22) {
      if (this.pendingHighSurrogate !== null) {
        throw new DecodeError('LONE_SURROGATE', 'unpaired high surrogate at string end');
      }
      tokens.push({ type: 'string', value: this.buf });
      this.buf = '';
      this.state = 'done';
      return;
    }
    if (byte === 0x5c) {
      this.state = 'escape';
      return;
    }
    if (byte < 0x20) {
      throw new DecodeError('SYNTAX_ERROR', 'unescaped control character in string');
    }
    if (byte < 0x80) {
      this.buf += String.fromCharCode(byte);
      return;
    }
    this.startUtf8(byte);
  }

  private readEscapeByte(byte: number): void {
    switch (byte) {
      case 0x22:
        this.buf += '"';
        break;
      case 0x5c:
        this.buf += '\\';
        break;
      case 0x2f:
        this.buf += '/';
        break;
      case 0x62:
        this.buf += '\b';
        break;
      case 0x66:
        this.buf += '\f';
        break;
      case 0x6e:
        this.buf += '\n';
        break;
      case 0x72:
        this.buf += '\r';
        break;
      case 0x74:
        this.buf += '\t';
        break;
      case 0x75:
        this.escapeBuf = '';
        this.state = 'escapeU';
        return;
      default:
        throw new DecodeError('SYNTAX_ERROR', `bad escape \\${String.fromCharCode(byte)}`);
    }
    this.state = 'string';
  }

  private readEscapeUByte(byte: number): void {
    const hex = String.fromCharCode(byte);
    if (!HEX.test(hex)) {
      throw new DecodeError('SYNTAX_ERROR', 'bad \\u hex digit');
    }
    this.escapeBuf += hex;
    if (this.escapeBuf.length !== 4) {
      return;
    }
    const codeUnit = parseInt(this.escapeBuf, 16);
    this.escapeBuf = '';
    this.state = 'string';
    if (codeUnit >= 0xd800 && codeUnit <= 0xdbff) {
      this.buf += String.fromCharCode(codeUnit);
      this.pendingHighSurrogate = codeUnit;
    } else if (codeUnit >= 0xdc00 && codeUnit <= 0xdfff) {
      if (this.pendingHighSurrogate === null) {
        throw new DecodeError('LONE_SURROGATE', 'unpaired low surrogate');
      }
      const high = this.pendingHighSurrogate;
      this.pendingHighSurrogate = null;
      const codePoint = 0x10000 + ((high - 0xd800) << 10) + (codeUnit - 0xdc00);
      this.buf = this.buf.slice(0, -1) + String.fromCodePoint(codePoint);
    } else {
      if (this.pendingHighSurrogate !== null) {
        throw new DecodeError('LONE_SURROGATE', 'high surrogate not followed by low surrogate');
      }
      this.buf += String.fromCharCode(codeUnit);
    }
  }

  private startUtf8(first: number): void {
    if (first >= 0xf0 && first <= 0xf7) {
      this.utf8Need = 3;
      this.utf8Code = first & 0x07;
    } else if (first >= 0xe0 && first <= 0xef) {
      this.utf8Need = 2;
      this.utf8Code = first & 0x0f;
    } else if (first >= 0xc0 && first <= 0xdf) {
      this.utf8Need = 1;
      this.utf8Code = first & 0x1f;
    } else {
      throw new DecodeError('SYNTAX_ERROR', 'invalid UTF-8 lead byte');
    }
  }

  private feedContinuation(byte: number): void {
    if ((byte & 0xc0) !== 0x80) {
      throw new DecodeError('SYNTAX_ERROR', 'bad UTF-8 continuation byte');
    }
    this.utf8Code = (this.utf8Code << 6) | (byte & 0x3f);
    this.utf8Need -= 1;
    if (this.utf8Need !== 0) {
      return;
    }
    const codePoint = this.utf8Code;
    this.utf8Code = 0;
    if (this.state !== 'string') {
      throw new DecodeError('SYNTAX_ERROR', 'UTF-8 sequence outside string');
    }
    if (codePoint >= 0xd800 && codePoint <= 0xdfff) {
      throw new DecodeError('LONE_SURROGATE', `surrogate code point U+${codePoint.toString(16)}`);
    }
    if (codePoint > 0x10ffff) {
      throw new DecodeError('SYNTAX_ERROR', 'UTF-8 code point beyond U+10FFFF');
    }
    this.buf += String.fromCodePoint(codePoint);
  }
}
