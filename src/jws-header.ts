/**
 * JWS 头准入组件（RFC 7515 + RFC 7797 子集）。
 *
 * 解决两类攻击面：
 * 1. `crit` 头塞入验证方不认识的关键扩展，迫使验证方忽略本应理解的语义；
 * 2. `b64=false`（未编码载荷）让载荷中出现句点，破坏紧凑序列化的分段约定。
 *
 * 组件零第三方依赖，可同时在 Node 与浏览器 ESM 环境中导入。
 */

// ---------------------------------------------------------------------------
// 错误模型
// ---------------------------------------------------------------------------

export type JwsHeaderErrorCode =
  | 'HEADER_NOT_BASE64URL'
  | 'HEADER_NOT_JSON'
  | 'HEADER_NOT_OBJECT'
  | 'DUPLICATE_HEADER_MEMBER'
  | 'CRIT_NOT_ARRAY'
  | 'CRIT_EMPTY'
  | 'CRIT_ENTRY_NOT_STRING'
  | 'CRIT_REGISTERED_NAME'
  | 'CRIT_MEMBER_ABSENT'
  | 'CRIT_NOT_ALLOWED'
  | 'B64_INVALID_TYPE'
  | 'B64_WITHOUT_CRIT'
  | 'PAYLOAD_CONTAINS_PERIOD'
  | 'COMPACT_MALFORMED';

export class JwsHeaderError extends Error {
  readonly code: JwsHeaderErrorCode;

  constructor(code: JwsHeaderErrorCode, message: string) {
    super(message);
    this.name = 'JwsHeaderError';
    this.code = code;
  }
}

// ---------------------------------------------------------------------------
// RFC 7515 已注册头参数名（§4.1）
// ---------------------------------------------------------------------------

const REGISTERED_HEADER_NAMES: ReadonlySet<string> = new Set([
  'alg',
  'jku',
  'jwk',
  'kid',
  'x5u',
  'x5c',
  'x5t',
  'x5t#S256',
  'typ',
  'cty',
  'crit',
]);

/** RFC 7797 的 b64 是最常见的调用方认可扩展，作为默认集合提供。 */
export const DEFAULT_ALLOWED_CRIT: ReadonlySet<string> = new Set(['b64']);

export interface JwsHeader {
  readonly [name: string]: unknown;
}

export interface AdmitHeaderOptions {
  /** 调用方明确认可的关键扩展头名集合；crit 中的每个名字都必须在该集合内。 */
  readonly allowedCrit: ReadonlySet<string> | readonly string[];
}

export interface AdmittedHeader {
  readonly header: JwsHeader;
  /** b64=false（未编码载荷）模式；其余取值与缺省均为标准的 base64url 载荷。 */
  readonly detachedPayloadMode: boolean;
}

/**
 * 解析并准入 JWS Protected Header（紧凑序列化的第一段原文）。
 * 任何规则不满足都抛出 {@link JwsHeaderError}。
 */
export function admitProtectedHeader(
  protectedHeaderSegment: string,
  options: AdmitHeaderOptions,
): AdmittedHeader {
  if (typeof protectedHeaderSegment !== 'string' || protectedHeaderSegment.length === 0) {
    throw new JwsHeaderError('HEADER_NOT_BASE64URL', '保护头段为空或不是字符串');
  }

  const headerBytes = base64urlToBytes(protectedHeaderSegment);
  let headerText: string;
  try {
    headerText = new TextDecoder('utf-8', { fatal: true }).decode(headerBytes);
  } catch {
    throw new JwsHeaderError('HEADER_NOT_JSON', '保护头不是合法的 UTF-8 JSON');
  }

  const parsed = new StrictJsonParser(headerText).parse();
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    throw new JwsHeaderError('HEADER_NOT_OBJECT', 'JWS 保护头必须是 JSON 对象');
  }

  const header = parsed as JwsHeader;
  const allowedCrit = new Set(options.allowedCrit);
  validateCrit(header, allowedCrit);
  const detachedPayloadMode = validateB64(header);

  return { header, detachedPayloadMode };
}

function validateCrit(header: JwsHeader, allowedCrit: Set<string>): void {
  if (!Object.prototype.hasOwnProperty.call(header, 'crit')) {
    return;
  }
  const critical = header.crit;
  if (!Array.isArray(critical)) {
    throw new JwsHeaderError('CRIT_NOT_ARRAY', 'crit 必须是数组');
  }
  if (critical.length === 0) {
    throw new JwsHeaderError('CRIT_EMPTY', 'crit 必须是非空数组');
  }
  for (const name of critical) {
    if (typeof name !== 'string') {
      throw new JwsHeaderError('CRIT_ENTRY_NOT_STRING', 'crit 中的条目必须是字符串');
    }
    if (REGISTERED_HEADER_NAMES.has(name)) {
      throw new JwsHeaderError(
        'CRIT_REGISTERED_NAME',
        `crit 不得包含 RFC 7515 已注册头名: ${name}`,
      );
    }
    if (!Object.prototype.hasOwnProperty.call(header, name)) {
      throw new JwsHeaderError(
        'CRIT_MEMBER_ABSENT',
        `crit 列出的头成员未出现在头中: ${name}`,
      );
    }
    if (!allowedCrit.has(name)) {
      throw new JwsHeaderError('CRIT_NOT_ALLOWED', `调用方不认可该关键扩展: ${name}`);
    }
  }
}

function validateB64(header: JwsHeader): boolean {
  if (!Object.prototype.hasOwnProperty.call(header, 'b64')) {
    return false;
  }
  if (typeof header.b64 !== 'boolean') {
    throw new JwsHeaderError('B64_INVALID_TYPE', 'b64 必须是布尔值');
  }
  if (header.b64) {
    return false;
  }
  // RFC 7797：b64=false 时必须同时在 crit 中列出 b64，否则签名输入语义存在歧义。
  const critical = header.crit;
  if (!Array.isArray(critical) || !critical.includes('b64')) {
    throw new JwsHeaderError(
      'B64_WITHOUT_CRIT',
      'b64=false 仅在 crit 包含 "b64" 时有效',
    );
  }
  return true;
}

// ---------------------------------------------------------------------------
// 紧凑序列化准入与签名输入构造
// ---------------------------------------------------------------------------

export interface AdmittedCompactJws extends AdmittedHeader {
  /** 验证时送入 JWS 算法的签名输入（RFC 7515 §5.1 / RFC 7797 §3）。 */
  readonly signingInput: Uint8Array;
  /** 载荷字节：未编码模式为原字节；标准模式为 base64url 解码后的字节。 */
  readonly payloadBytes: Uint8Array;
  /** 签名段原文（base64url），交由调用方做实际密码学验证。 */
  readonly signatureSegment: string;
}

const DOT = 0x2e;

/**
 * 准入完整的紧凑序列化 JWS，并构造签名输入。
 *
 * 标准模式下序列化为 `BASE64URL(header).BASE64URL(payload).BASE64URL(signature)`；
 * 未编码载荷模式（b64=false）下中段是载荷原字节，且不得再含句点，
 * 以免额外的 `.` 改变三段划分。
 */
export function admitCompactJws(
  compact: string,
  options: AdmitHeaderOptions,
): AdmittedCompactJws {
  if (typeof compact !== 'string' || compact.length === 0) {
    throw new JwsHeaderError('COMPACT_MALFORMED', '紧凑序列化不能为空');
  }

  const compactBytes = new TextEncoder().encode(compact);
  const dotIndexes: number[] = [];
  for (let i = 0; i < compactBytes.length; i += 1) {
    if (compactBytes[i] === DOT) {
      dotIndexes.push(i);
    }
  }

  const headerEnd = compact.indexOf('.');
  if (headerEnd <= 0) {
    throw new JwsHeaderError('COMPACT_MALFORMED', '缺少头段或头段后的句点');
  }
  const headerSegment = compact.slice(0, headerEnd);
  const admitted = admitProtectedHeader(headerSegment, options);

  if (dotIndexes.length !== 2) {
    if (admitted.detachedPayloadMode && dotIndexes.length > 2) {
      throw new JwsHeaderError(
        'PAYLOAD_CONTAINS_PERIOD',
        'b64=false 时紧凑序列化的载荷不得包含句点',
      );
    }
    throw new JwsHeaderError('COMPACT_MALFORMED', '紧凑序列化必须恰好包含两个句点');
  }

  const [firstDot, secondDot] = dotIndexes;
  const payloadBytes = compactBytes.subarray(firstDot + 1, secondDot);
  const signatureSegment = compact.slice(secondDot + 1);
  if (signatureSegment.length === 0) {
    throw new JwsHeaderError('COMPACT_MALFORMED', '签名段不能为空');
  }

  let decodedPayload: Uint8Array;
  if (admitted.detachedPayloadMode) {
    // 未编码载荷：签名输入就是「头原文 + . + 载荷原字节」，载荷不做任何解码。
    decodedPayload = payloadBytes.slice();
  } else {
    const payloadSegment = compact.slice(firstDot + 1, secondDot);
    if (payloadSegment.length === 0) {
      throw new JwsHeaderError('COMPACT_MALFORMED', '载荷段不能为空');
    }
    try {
      decodedPayload = base64urlToBytes(payloadSegment);
    } catch (error) {
      throw new JwsHeaderError(
        'COMPACT_MALFORMED',
        `载荷段不是合法的 base64url: ${(error as Error).message}`,
      );
    }
  }

  // RFC 7515 §5.1：签名输入为 ASCII(BASE64URL(header) || '.' || payload)。
  // 标准模式下 payload 是 base64url 段；b64=false 时是载荷原字节（RFC 7797）。
  const signingInput = compactBytes.subarray(0, secondDot);

  return {
    header: admitted.header,
    detachedPayloadMode: admitted.detachedPayloadMode,
    signingInput: signingInput.slice(),
    payloadBytes: decodedPayload,
    signatureSegment,
  };
}

// ---------------------------------------------------------------------------
// base64url 编解码（仅依赖全局 atob/btoa 与 TextDecoder，无第三方依赖）
// ---------------------------------------------------------------------------

const BASE64URL_CHARS = /^[A-Za-z0-9_-]*$/;

export function base64urlToBytes(segment: string): Uint8Array {
  if (!BASE64URL_CHARS.test(segment)) {
    throw new JwsHeaderError('HEADER_NOT_BASE64URL', '段不是合法的 base64url 编码');
  }
  const base64 = segment.replace(/-/g, '+').replace(/_/g, '/');
  const padded = base64 + '='.repeat((4 - (base64.length % 4)) % 4);
  const binary = atob(padded);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

export function bytesToBase64url(bytes: Uint8Array): string {
  let binary = '';
  const chunkSize = 0x8000;
  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + chunkSize));
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

// ---------------------------------------------------------------------------
// 严格 JSON 解析：同名成员重复即拒绝（JSON.parse 会静默保留最后一个）
// ---------------------------------------------------------------------------

class StrictJsonParser {
  private readonly text: string;
  private position = 0;

  constructor(text: string) {
    this.text = text;
  }

  parse(): unknown {
    this.skipWhitespace();
    const value = this.parseValue();
    this.skipWhitespace();
    if (this.position !== this.text.length) {
      throw new JwsHeaderError('HEADER_NOT_JSON', '头 JSON 含有多余字符');
    }
    return value;
  }

  private parseValue(): unknown {
    const char = this.text[this.position];
    if (char === '{') return this.parseObject();
    if (char === '[') return this.parseArray();
    if (char === '"') return this.parseString();
    if (char === 't') return this.parseLiteral('true', true);
    if (char === 'f') return this.parseLiteral('false', false);
    if (char === 'n') return this.parseLiteral('null', null);
    if (char === '-' || (char >= '0' && char <= '9')) return this.parseNumber();
    throw new JwsHeaderError('HEADER_NOT_JSON', `意外的字符: ${String(char)}`);
  }

  private parseObject(): Record<string, unknown> {
    this.position += 1;
    const object: Record<string, unknown> = {};
    this.skipWhitespace();
    if (this.text[this.position] === '}') {
      this.position += 1;
      return object;
    }
    for (;;) {
      if (this.text[this.position] !== '"') {
        throw new JwsHeaderError('HEADER_NOT_JSON', '对象成员名必须是字符串');
      }
      const key = this.parseString();
      if (Object.prototype.hasOwnProperty.call(object, key)) {
        throw new JwsHeaderError(
          'DUPLICATE_HEADER_MEMBER',
          `头中存在重复成员: ${key}`,
        );
      }
      this.skipWhitespace();
      if (this.text[this.position] !== ':') {
        throw new JwsHeaderError('HEADER_NOT_JSON', '对象成员名后缺少冒号');
      }
      this.position += 1;
      this.skipWhitespace();
      object[key] = this.parseValue();
      this.skipWhitespace();
      const separator = this.text[this.position];
      if (separator === ',') {
        this.position += 1;
        this.skipWhitespace();
      } else if (separator === '}') {
        this.position += 1;
        return object;
      } else {
        throw new JwsHeaderError('HEADER_NOT_JSON', '对象成员缺少逗号或闭合花括号');
      }
    }
  }

  private parseArray(): unknown[] {
    this.position += 1;
    const array: unknown[] = [];
    this.skipWhitespace();
    if (this.text[this.position] === ']') {
      this.position += 1;
      return array;
    }
    for (;;) {
      array.push(this.parseValue());
      this.skipWhitespace();
      const separator = this.text[this.position];
      if (separator === ',') {
        this.position += 1;
        this.skipWhitespace();
      } else if (separator === ']') {
        this.position += 1;
        return array;
      } else {
        throw new JwsHeaderError('HEADER_NOT_JSON', '数组缺少逗号或闭合方括号');
      }
    }
  }

  private parseString(): string {
    this.position += 1;
    let result = '';
    while (this.position < this.text.length) {
      const char = this.text[this.position];
      if (char === '"') {
        this.position += 1;
        return result;
      }
      if (char < ' ') {
        throw new JwsHeaderError('HEADER_NOT_JSON', '字符串中存在未转义的控制字符');
      }
      if (char !== '\\') {
        result += char;
        this.position += 1;
        continue;
      }
      this.position += 1;
      const escaped = this.text[this.position];
      switch (escaped) {
        case '"':
        case '\\':
        case '/':
          result += escaped;
          break;
        case 'b':
          result += '\b';
          break;
        case 'f':
          result += '\f';
          break;
        case 'n':
          result += '\n';
          break;
        case 'r':
          result += '\r';
          break;
        case 't':
          result += '\t';
          break;
        case 'u': {
          const code = this.parseUnicodeEscape();
          if (code >= 0xd800 && code <= 0xdbff) {
            if (this.text[this.position] !== '\\' || this.text[this.position + 1] !== 'u') {
              throw new JwsHeaderError('HEADER_NOT_JSON', '高代理项缺少配对的低代理项');
            }
            this.position += 2;
            const low = this.parseUnicodeEscape();
            if (low < 0xdc00 || low > 0xdfff) {
              throw new JwsHeaderError('HEADER_NOT_JSON', '高代理项后的低代理项非法');
            }
            result += String.fromCharCode(code, low);
          } else if (code >= 0xdc00 && code <= 0xdfff) {
            throw new JwsHeaderError('HEADER_NOT_JSON', '出现孤立的低代理项');
          } else {
            result += String.fromCharCode(code);
          }
          break;
        }
        default:
          throw new JwsHeaderError('HEADER_NOT_JSON', `非法转义: \\${String(escaped)}`);
      }
      this.position += 1;
    }
    throw new JwsHeaderError('HEADER_NOT_JSON', '字符串未闭合');
  }

  private parseUnicodeEscape(): number {
    const hex = this.text.slice(this.position + 1, this.position + 5);
    if (!/^[0-9a-fA-F]{4}$/.test(hex)) {
      throw new JwsHeaderError('HEADER_NOT_JSON', '\\u 转义后需要 4 位十六进制数');
    }
    this.position += 4;
    return parseInt(hex, 16);
  }

  private parseNumber(): number {
    const match = /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/.exec(
      this.text.slice(this.position),
    );
    if (!match) {
      throw new JwsHeaderError('HEADER_NOT_JSON', '非法数字');
    }
    this.position += match[0].length;
    return Number(match[0]);
  }

  private parseLiteral(literal: string, value: boolean | null): boolean | null {
    if (!this.text.startsWith(literal, this.position)) {
      throw new JwsHeaderError('HEADER_NOT_JSON', `期望字面量 ${literal}`);
    }
    this.position += literal.length;
    return value;
  }

  private skipWhitespace(): void {
    while (this.position < this.text.length && ' \t\r\n'.includes(this.text[this.position])) {
      this.position += 1;
    }
  }
}
