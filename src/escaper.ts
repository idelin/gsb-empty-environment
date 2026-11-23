/**
 * 上下文感知转义组件。
 *
 * 支持六类模板上下文：
 *  - html    元素正文
 *  - attr    普通属性值
 *  - js      脚本中的字符串/数据字面量
 *  - url     href/src 等 URL 属性
 *  - comment 注释
 *  - css     样式
 *
 * 设计要点：
 *  - 每个上下文使用独立编码规则，不能跨上下文复用。
 *  - 脚本 (js) 上下文不接受未编码的原始变量，仅接受 JSON 字面量或 SafeString。
 *  - encode 对 html/attr/comment 使用实体感知（entity-aware）规则，
 *    对已经转义过的字符串再次转义不会改变原义；同时 escape 用 SafeString
 *    包裹结果，保证全上下文双重编码幂等。
 */

export type EscapeContext =
  | 'html'
  | 'attr'
  | 'js'
  | 'url'
  | 'comment'
  | 'css';

/** 已被标记为安全（完成转义）的内容，再次转义时原样透传。 */
export class SafeString {
  readonly value: string;

  constructor(value: string) {
    this.value = value;
  }

  toString(): string {
    return this.value;
  }
}

/** 在当前上下文输出原始变量被安全策略拒绝时抛出。 */
export class UnsafeRawOutputError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'UnsafeRawOutputError';
  }
}

/** URL 使用了不被允许的协议（如 javascript:）时抛出。 */
export class UnsafeUrlError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'UnsafeUrlError';
  }
}

/** 匹配已转义的 HTML 实体（命名实体或数值实体），用于幂等转义。 */
const ENTITY_BODY =
  '(?:amp|lt|gt|quot|apos|nbsp|#x[0-9a-fA-F]+|#[0-9]+)';
const ENTITY_RE = new RegExp(`&${ENTITY_BODY};`, 'g');
const ENTITY_CAPTURE_RE = new RegExp(`&(${ENTITY_BODY});`, 'g');

function encodeEntityAware(
  input: string,
  charMap: Record<string, string>,
): string {
  let output = '';
  let lastIndex = 0;
  ENTITY_RE.lastIndex = 0;

  let match: RegExpExecArray | null;
  while ((match = ENTITY_RE.exec(input)) !== null) {
    output += encodePlain(input.slice(lastIndex, match.index), charMap);
    output += match[0];
    lastIndex = match.index + match[0].length;
  }
  output += encodePlain(input.slice(lastIndex), charMap);
  return output;
}

function encodePlain(input: string, charMap: Record<string, string>): string {
  let output = '';
  for (const char of input) {
    output += charMap[char] ?? char;
  }
  return output;
}

const HTML_MAP: Record<string, string> = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
};

/** 元素正文：转义 & < >，实体感知，可安全用于文本节点。 */
export function encodeHtml(input: string): string {
  return encodeEntityAware(input, HTML_MAP);
}

const ATTR_MAP: Record<string, string> = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
  '`': '&#96;',
  '\t': '&#9;',
  '\n': '&#10;',
  '\r': '&#13;',
  ' ': '&#32;',
};

/** 普通属性值：在元素正文基础上额外转义引号与空白。 */
export function encodeAttr(input: string): string {
  return encodeEntityAware(input, ATTR_MAP);
}

/**
 * 脚本上下文：把任意值编码为 JS/JSON 字面量。
 *
 * 普通变量只能以完整 JSON 字面量输出（自带引号），无法通过
 * `{{{ }}}` 输出未编码的原始字符串，避免闭合字符串或脚本标签。
 */
export function encodeJs(value: unknown): string {
  if (value === undefined || value === null) {
    return value === undefined ? 'undefined' : 'null';
  }
  return JSON.stringify(value)
    .replace(/</g, '\\u003c')
    .replace(/>/g, '\\u003e')
    .replace(/\u2028/g, '\\u2028')
    .replace(/\u2029/g, '\\u2029');
}

const COMMENT_MAP: Record<string, string> = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
  '-': '&#45;',
  '`': '&#96;',
  '\t': '&#9;',
  '\n': '&#10;',
  '\r': '&#13;',
};

/** 注释：禁止出现 --、> 等可闭合注释的序列。 */
export function encodeComment(input: string): string {
  return encodeEntityAware(input, COMMENT_MAP);
}

const SAFE_CSS_PUNCTUATION = new Set([
  ' ',
  '\t',
  '-',
  '_',
  '.',
  ',',
  ';',
  '#',
  '%',
]);

function isSafeCssChar(char: string, codePoint: number): boolean {
  if (char >= 'a' && char <= 'z') return true;
  if (char >= 'A' && char <= 'Z') return true;
  if (char >= '0' && char <= '9') return true;
  return SAFE_CSS_PUNCTUATION.has(char) || codePoint >= 0x80;
}

/** 样式：除保守白名单字符外一律输出为 CSS 十六进制转义。 */
export function encodeCss(input: string): string {
  let output = '';
  for (const char of input) {
    const codePoint = char.codePointAt(0) as number;
    if (isSafeCssChar(char, codePoint)) {
      output += char;
    } else {
      output += `\\${codePoint.toString(16)} `;
    }
  }
  return output;
}

const ALLOWED_URL_SCHEMES = new Set(['http', 'https', 'mailto', 'tel', 'ftp']);

const URL_MAP: Record<string, string> = {
  '"': '&quot;',
  "'": '&#39;',
  '`': '&#96;',
  '<': '&lt;',
  '>': '&gt;',
  ' ': '%20',
  '\t': '%09',
  '\n': '%0A',
  '\r': '%0D',
  '\\': '&#92;',
};

/**
 * URL 属性：协议白名单校验后再做属性安全编码。
 *
 * 校验前先去除控制字符并解码实体，阻止 `java\nscript:`、
 * `&#106;avascript:` 等混淆绕过。
 */
export function encodeUrl(input: string): string {
  const probe = decodeEntities(stripControlChars(input)).trim();

  if (probe.length > 0) {
    const schemeMatch = /^([a-zA-Z][a-zA-Z0-9+.-]*):/.exec(probe);
    if (schemeMatch) {
      const scheme = schemeMatch[1].toLowerCase();
      if (!ALLOWED_URL_SCHEMES.has(scheme)) {
        throw new UnsafeUrlError(
          `URL scheme "${scheme}" is not allowed; allowed schemes: ` +
            `${[...ALLOWED_URL_SCHEMES].join(', ')} or relative URLs`,
        );
      }
    }
  }

  let output = '';
  for (const char of input) {
    output += URL_MAP[char] ?? char;
  }
  return output;
}

function stripControlChars(input: string): string {
  return input.replace(/[\u0000-\u001F\u007F-\u009F]/g, '');
}

const NAMED_ENTITY_VALUES: Record<string, string> = {
  amp: '&',
  lt: '<',
  gt: '>',
  quot: '"',
  apos: "'",
  nbsp: '\u00A0',
};

/** 解码命名/数值实体，供 URL 协议校验使用（不用于渲染输出）。 */
export function decodeEntities(input: string): string {
  return input.replace(ENTITY_CAPTURE_RE, (entity, body: string) => {
      if (body[0] === '#') {
        const isHex = body[1] === 'x' || body[1] === 'X';
        const codePoint = isHex
          ? parseInt(body.slice(2), 16)
          : parseInt(body.slice(1), 10);
        return Number.isFinite(codePoint)
          ? String.fromCodePoint(codePoint)
          : entity;
      }
      return NAMED_ENTITY_VALUES[body] ?? entity;
    });
}

const ENCODERS: Record<EscapeContext, (input: string) => string> = {
  html: encodeHtml,
  attr: encodeAttr,
  js: (input) => encodeJs(input),
  url: encodeUrl,
  comment: encodeComment,
  css: encodeCss,
};

/**
 * 按上下文转义。SafeString 原样透传；普通值返回新的 SafeString，
 * 因此对结果再次调用 escape 不会二次编码。
 */
export function escape(
  value: unknown,
  context: EscapeContext,
): SafeString {
  if (value instanceof SafeString) {
    return value;
  }
  const stringValue =
    value === undefined || value === null ? '' : String(value);
  return new SafeString(ENCODERS[context](stringValue));
}

/** 标记已经安全的内容（等价于模板引擎中的 raw / trusted 输出）。 */
export function safe(value: string | SafeString): SafeString {
  return value instanceof SafeString ? value : new SafeString(value);
}
