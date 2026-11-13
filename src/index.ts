/**
 * JWE 受保护头准入控制（在任何 PBES2 密钥派生 / 解压之前执行的纯校验逻辑）。
 *
 * 本模块不执行任何密钥派生、解密或解压操作，只做同步的头校验，
 * 因此调用方可以保证所有判定都在昂贵操作之前完成。
 */

// ---------------------------------------------------------------------------
// 常量
// ---------------------------------------------------------------------------

/** 允许的 PBES2 算法。 */
export const PBES2_ALGORITHMS = [
  'PBES2-HS256+A128KW',
  'PBES2-HS384+A192KW',
  'PBES2-HS512+A256KW',
] as const;

/** 明确拒绝的算法。 */
export const FORBIDDEN_ALGORITHMS = ['dir', 'RSA1_5'] as const;

/** p2c 下限（含边界）。 */
export const MIN_P2C = 1000;

/** p2s 解码后的长度限制（含边界）。 */
export const MIN_P2S_BYTES = 8;
export const MAX_P2S_BYTES = 64;

/** 允许的最大压缩比（超过即拒绝，恰好 100 倍允许）。 */
export const MAX_COMPRESSION_RATIO = 100;

/** 允许的 zip 值（缺省表示不压缩）。 */
export const ALLOWED_ZIP = 'DEF';

/** RFC 7516/7518 中注册的标准 JWE 头参数名。 */
export const REGISTERED_HEADER_PARAMETERS = [
  'alg',
  'enc',
  'zip',
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
  'p2s',
  'p2c',
  'iv',
  'tag',
  'apu',
  'apv',
  'epk',
] as const;

export type JweHeaderAdmissionCode =
  | 'INVALID_JSON'
  | 'HEADER_NOT_OBJECT'
  | 'DUPLICATE_HEADER_PARAMETER'
  | 'ALG_MISSING'
  | 'ALG_FORBIDDEN'
  | 'P2C_MISSING'
  | 'P2C_NOT_INTEGER'
  | 'P2C_OUT_OF_RANGE'
  | 'P2S_MISSING'
  | 'P2S_NOT_BASE64URL'
  | 'P2S_LENGTH_OUT_OF_RANGE'
  | 'ZIP_NOT_ALLOWED'
  | 'DECOMPRESSED_BUDGET_ZERO'
  | 'COMPRESSION_RATIO_EXCEEDED'
  | 'INVALID_COMPRESSED_LENGTH'
  | 'BUDGET_INVALID'
  | 'CRIT_NOT_ARRAY'
  | 'CRIT_EMPTY'
  | 'CRIT_DUPLICATE'
  | 'CRIT_NOT_STRING'
  | 'CRIT_REGISTERED_NAME'
  | 'CRIT_NAME_ABSENT'
  | 'CRIT_NAME_NOT_UNDERSTOOD';

export interface AdmissionBudget {
  /** 调用方允许的 p2c 上限（含边界），必须 >= 1000。 */
  maxP2c: number;
  /** 调用方允许的解压后输出字节上限（含边界）。 */
  maxDecompressedBytes: number;
  /**
   * 调用方能够理解的、可出现在 crit 中的扩展头参数名。
   * 缺省时不理解任何扩展参数（RFC 7516 注册参数本身不允许出现在 crit 中）。
   */
  understoodCriticalHeaders?: readonly string[];
}

export interface AdmissionLimits {
  p2c: { min: number; max: number };
  p2sBytes: { min: number; max: number };
  maxCompressionRatio: number;
  maxDecompressedBytes: number;
}

export interface AdmissionResult {
  admitted: boolean;
  /** admitted 为 true 时给出本次生效的预算回显，方便审计。 */
  limits?: AdmissionLimits;
  code?: JweHeaderAdmissionCode;
  message?: string;
  /** 触发拒绝的参数名（如 p2c / p2s / zip / crit / alg），便于日志定位。 */
  parameter?: string;
}

/** 解析后的受保护头：JSON 对象，键唯一。 */
export type JweProtectedHeader = Readonly<Record<string, unknown>>;

export class JweHeaderAdmissionError extends Error {
  readonly code: JweHeaderAdmissionCode;
  readonly parameter?: string;

  constructor(code: JweHeaderAdmissionCode, message: string, parameter?: string) {
    super(message);
    this.name = 'JweHeaderAdmissionError';
    this.code = code;
    this.parameter = parameter;
  }
}

// ---------------------------------------------------------------------------
// base64url（无外部依赖实现）
// ---------------------------------------------------------------------------

const BASE64URL_CHARS = /^[A-Za-z0-9_-]+$/;

function decodeBase64UrlWithCode(
  value: string,
  errorCode: JweHeaderAdmissionCode,
): Uint8Array {
  if (typeof value !== 'string' || !BASE64URL_CHARS.test(value)) {
    throw new JweHeaderAdmissionError(
      errorCode,
      '输入必须是非空、无填充的 base64url 字符串',
    );
  }
  if (value.length % 4 === 1) {
    throw new JweHeaderAdmissionError(errorCode, 'base64url 长度非法');
  }

  const pad = value.length % 4 === 0 ? '' : '='.repeat(4 - (value.length % 4));
  const standard = value.replace(/-/g, '+').replace(/_/g, '/') + pad;
  let binary: string;
  try {
    binary = atob(standard);
  } catch {
    throw new JweHeaderAdmissionError(errorCode, '不是合法的 base64url 编码');
  }

  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return bytes;
}

/**
 * 严格解码 base64url 字符串（无填充，不接受标准 base64 的 '+' '/' '='）。
 * 输入不合法时抛出 JweHeaderAdmissionError。
 */
export function decodeBase64Url(value: string): Uint8Array {
  return decodeBase64UrlWithCode(value, 'P2S_NOT_BASE64URL');
}

// ---------------------------------------------------------------------------
// 受保护头解析（重复键检测）
// ---------------------------------------------------------------------------

/**
 * 解析受保护头 JSON，并在解析阶段拒绝重复的头参数名（JSON.parse 默认会静默
 * 保留最后一个同名键，这里通过 reviver + 容器栈检测同一对象内的重复键）。
 */
export function parseProtectedHeader(json: string): JweProtectedHeader {
  if (typeof json !== 'string') {
    throw new JweHeaderAdmissionError('INVALID_JSON', '受保护头必须是 JSON 字符串');
  }

  try {
    const parsed = parseJsonWithDuplicateCheck(json);
    if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
      throw new JweHeaderAdmissionError('HEADER_NOT_OBJECT', '受保护头必须是 JSON 对象');
    }
    return parsed as JweProtectedHeader;
  } catch (error) {
    if (error instanceof JweHeaderAdmissionError) {
      throw error;
    }
    throw new JweHeaderAdmissionError(
      'INVALID_JSON',
      `受保护头不是合法 JSON: ${(error as Error).message}`,
    );
  }
}

/**
 * 递归下降 JSON 解析器（RFC 8259 子集）。
 *
 * 标准 JSON.parse 会静默保留同名键的最后一个值，无法检测重复头参数名，
 * 因此这里自行做词法/语法分析，在对象闭合前对键名做唯一性检查。
 */
function parseJsonWithDuplicateCheck(text: string): unknown {
  let index = 0;
  const length = text.length;

  function skipWhitespace(): void {
    while (index < length) {
      const code = text.charCodeAt(index);
      if (code === 0x20 || code === 0x09 || code === 0x0a || code === 0x0d) {
        index += 1;
      } else {
        break;
      }
    }
  }

  function parseValue(): unknown {
    skipWhitespace();
    if (index >= length) {
      throw new SyntaxError('意外的输入结束');
    }
    const char = text[index];
    if (char === '{') {
      return parseObject();
    }
    if (char === '[') {
      return parseArray();
    }
    if (char === '"') {
      return parseString();
    }
    if (char === 't') {
      return parseLiteral('true', true);
    }
    if (char === 'f') {
      return parseLiteral('false', false);
    }
    if (char === 'n') {
      return parseLiteral('null', null);
    }
    if (char === '-' || (char >= '0' && char <= '9')) {
      return parseNumber();
    }
    throw new SyntaxError(`位置 ${index} 出现意外字符 ${char}`);
  }

  function parseLiteral(literal: string, value: unknown): unknown {
    if (text.startsWith(literal, index)) {
      index += literal.length;
      return value;
    }
    throw new SyntaxError(`位置 ${index} 的字面量非法`);
  }

  function parseNumber(): number {
    const start = index;
    if (text[index] === '-') {
      index += 1;
    }
    if (text[index] === '0') {
      index += 1;
    } else if (text[index] >= '1' && text[index] <= '9') {
      while (text[index] >= '0' && text[index] <= '9') {
        index += 1;
      }
    } else {
      throw new SyntaxError(`位置 ${index} 的数字非法`);
    }
    if (text[index] === '.') {
      index += 1;
      if (!(text[index] >= '0' && text[index] <= '9')) {
        throw new SyntaxError(`位置 ${index} 缺少小数位`);
      }
      while (text[index] >= '0' && text[index] <= '9') {
        index += 1;
      }
    }
    if (text[index] === 'e' || text[index] === 'E') {
      index += 1;
      if (text[index] === '+' || text[index] === '-') {
        index += 1;
      }
      if (!(text[index] >= '0' && text[index] <= '9')) {
        throw new SyntaxError(`位置 ${index} 缺少指数位`);
      }
      while (text[index] >= '0' && text[index] <= '9') {
        index += 1;
      }
    }
    return Number(text.slice(start, index));
  }

  function parseString(): string {
    const start = index;
    index += 1; // 开引号
    let result = '';
    while (index < length) {
      const char = text[index];
      if (char === '"') {
        index += 1;
        try {
          return JSON.parse(text.slice(start, index)) as string;
        } catch {
          throw new SyntaxError(`位置 ${start} 的字符串非法`);
        }
      }
      if (char.charCodeAt(0) < 0x20) {
        throw new SyntaxError(`位置 ${index} 存在未转义控制字符`);
      }
      if (char === '\\') {
        index += 1;
        if (index >= length) {
          throw new SyntaxError('转义序列不完整');
        }
        const escaped = text[index];
        if (escaped === 'u') {
          const hex = text.slice(index + 1, index + 5);
          if (!/^[0-9a-fA-F]{4}$/.test(hex)) {
            throw new SyntaxError(`位置 ${index} 的 \\u 转义非法`);
          }
          index += 5;
          continue;
        }
        if (escaped === '"' || escaped === '\\' || escaped === '/' ||
          escaped === 'b' || escaped === 'f' || escaped === 'n' ||
          escaped === 'r' || escaped === 't') {
          index += 1;
          continue;
        }
        throw new SyntaxError(`位置 ${index} 的转义非法`);
      }
      index += 1;
    }
    throw new SyntaxError('字符串缺少闭合引号');
  }

  function parseArray(): unknown[] {
    index += 1; // [
    const items: unknown[] = [];
    skipWhitespace();
    if (text[index] === ']') {
      index += 1;
      return items;
    }
    while (true) {
      items.push(parseValue());
      skipWhitespace();
      if (text[index] === ',') {
        index += 1;
        continue;
      }
      if (text[index] === ']') {
        index += 1;
        return items;
      }
      throw new SyntaxError(`位置 ${index} 数组缺少 ',' 或 ']'`);
    }
  }

  function parseObject(): Record<string, unknown> {
    index += 1; // {
    const object: Record<string, unknown> = {};
    const keys = new Set<string>();
    skipWhitespace();
    if (text[index] === '}') {
      index += 1;
      return object;
    }
    while (true) {
      skipWhitespace();
      if (text[index] !== '"') {
        throw new SyntaxError(`位置 ${index} 对象键必须是字符串`);
      }
      const key = parseString();
      if (keys.has(key)) {
        throw new JweHeaderAdmissionError(
          'DUPLICATE_HEADER_PARAMETER',
          `受保护头包含重复的参数名: ${key}`,
          key,
        );
      }
      keys.add(key);
      skipWhitespace();
      if (text[index] !== ':') {
        throw new SyntaxError(`位置 ${index} 对象键后缺少 ':'`);
      }
      index += 1;
      object[key] = parseValue();
      skipWhitespace();
      if (text[index] === ',') {
        index += 1;
        continue;
      }
      if (text[index] === '}') {
        index += 1;
        return object;
      }
      throw new SyntaxError(`位置 ${index} 对象缺少 ',' 或 '}'`);
    }
  }

  const result = parseValue();
  skipWhitespace();
  if (index !== length) {
    throw new SyntaxError(`位置 ${index} 之后存在多余内容`);
  }
  return result;
}

/**
 * 解码 JWE compact serialization 第一段（base64url 编码的受保护头），
 * 随后走与原始 JSON 完全相同的重复键与结构校验。
 */
export function decodeCompactProtectedHeader(encoded: string): JweProtectedHeader {
  try {
    const bytes = decodeBase64UrlWithCode(encoded, 'INVALID_JSON');
    return parseProtectedHeader(new TextDecoder().decode(bytes));
  } catch (error) {
    if (error instanceof JweHeaderAdmissionError && error.code === 'INVALID_JSON') {
      // 不是合法 compact 头时，按原始 JSON 路径再试一次，由 JSON 层给出精确错误。
      return parseProtectedHeader(encoded);
    }
    throw error;
  }
}

// ---------------------------------------------------------------------------
// 准入校验（全部为纯计算，不涉及密钥派生或解压）
// ---------------------------------------------------------------------------

function isSafeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value);
}

function validateBudget(budget: AdmissionBudget): void {
  if (
    budget === null ||
    typeof budget !== 'object' ||
    !isSafeInteger(budget.maxP2c) ||
    budget.maxP2c < MIN_P2C
  ) {
    throw new JweHeaderAdmissionError(
      'BUDGET_INVALID',
      `budget.maxP2c 必须是 >= ${MIN_P2C} 的安全整数`,
      'maxP2c',
    );
  }
  if (!isSafeInteger(budget.maxDecompressedBytes) || budget.maxDecompressedBytes < 0) {
    throw new JweHeaderAdmissionError(
      'BUDGET_INVALID',
      'budget.maxDecompressedBytes 必须是非负安全整数',
      'maxDecompressedBytes',
    );
  }
  if (
    budget.understoodCriticalHeaders !== undefined &&
    (!Array.isArray(budget.understoodCriticalHeaders) ||
      budget.understoodCriticalHeaders.some((name) => typeof name !== 'string'))
  ) {
    throw new JweHeaderAdmissionError(
      'BUDGET_INVALID',
      'budget.understoodCriticalHeaders 必须是字符串数组',
      'understoodCriticalHeaders',
    );
  }
}

function checkPbes2Parameters(header: JweProtectedHeader, budget: AdmissionBudget): void {
  const p2c = header.p2c;
  if (p2c === undefined) {
    throw new JweHeaderAdmissionError('P2C_MISSING', 'PBES2 算法必须提供 p2c', 'p2c');
  }
  if (!isSafeInteger(p2c)) {
    throw new JweHeaderAdmissionError(
      'P2C_NOT_INTEGER',
      'p2c 必须是整数（安全整数）',
      'p2c',
    );
  }
  if (p2c < MIN_P2C || p2c > budget.maxP2c) {
    throw new JweHeaderAdmissionError(
      'P2C_OUT_OF_RANGE',
      `p2c 必须在 ${MIN_P2C} 到 ${budget.maxP2c} 之间（含边界），实际为 ${p2c}`,
      'p2c',
    );
  }

  const p2s = header.p2s;
  if (p2s === undefined) {
    throw new JweHeaderAdmissionError('P2S_MISSING', 'PBES2 算法必须提供 p2s', 'p2s');
  }
  if (typeof p2s !== 'string') {
    throw new JweHeaderAdmissionError(
      'P2S_NOT_BASE64URL',
      'p2s 必须是 base64url 字符串',
      'p2s',
    );
  }
  const salt = decodeBase64Url(p2s);
  if (salt.length < MIN_P2S_BYTES || salt.length > MAX_P2S_BYTES) {
    throw new JweHeaderAdmissionError(
      'P2S_LENGTH_OUT_OF_RANGE',
      `p2s 解码后必须为 ${MIN_P2S_BYTES} 到 ${MAX_P2S_BYTES} 字节，实际为 ${salt.length} 字节`,
      'p2s',
    );
  }
}

function checkCompression(
  header: JweProtectedHeader,
  compressedPayloadLength: number,
  budget: AdmissionBudget,
): void {
  if (!isSafeInteger(compressedPayloadLength) || compressedPayloadLength < 0) {
    throw new JweHeaderAdmissionError(
      'INVALID_COMPRESSED_LENGTH',
      'compressedPayloadLength 必须是非负安全整数',
    );
  }

  if (!('zip' in header)) {
    return;
  }
  if (header.zip !== ALLOWED_ZIP) {
    throw new JweHeaderAdmissionError(
      'ZIP_NOT_ALLOWED',
      `zip 只允许缺省或 "${ALLOWED_ZIP}"，实际为 ${String(header.zip)}`,
      'zip',
    );
  }

  // 调用方给出的解压输出上限为 0，却声明了压缩头：任何非空解压结果都会越界。
  if (budget.maxDecompressedBytes === 0) {
    throw new JweHeaderAdmissionError(
      'DECOMPRESSED_BUDGET_ZERO',
      'maxDecompressedBytes 为 0 时不允许 DEF 压缩头',
      'zip',
    );
  }

  // 空的压缩输入在解压后不可能产生正字节输出，调用方却给出了正预算，属于异常输入。
  if (compressedPayloadLength === 0) {
    throw new JweHeaderAdmissionError(
      'COMPRESSION_RATIO_EXCEEDED',
      '压缩载荷长度为 0，无法通过压缩比校验',
      'zip',
    );
  }

  // 用除法而非乘法，避免大整数乘法丢失精度。恰好 100 倍允许，超过才拒绝。
  const ratio = budget.maxDecompressedBytes / compressedPayloadLength;
  if (ratio > MAX_COMPRESSION_RATIO) {
    throw new JweHeaderAdmissionError(
      'COMPRESSION_RATIO_EXCEEDED',
      `压缩比上限为 ${MAX_COMPRESSION_RATIO} 倍，按预算折算为 ${ratio} 倍（解压预算 ${budget.maxDecompressedBytes} / 压缩长度 ${compressedPayloadLength}）`,
      'zip',
    );
  }
}

function checkCrit(header: JweProtectedHeader, budget: AdmissionBudget): void {
  if (!('crit' in header)) {
    return;
  }
  const crit = header.crit;
  if (!Array.isArray(crit)) {
    throw new JweHeaderAdmissionError(
      'CRIT_NOT_ARRAY',
      'crit 必须是字符串数组',
      'crit',
    );
  }
  if (crit.length === 0) {
    throw new JweHeaderAdmissionError('CRIT_EMPTY', 'crit 不允许为空数组', 'crit');
  }

  const registered = new Set<string>(REGISTERED_HEADER_PARAMETERS);
  const understood = new Set<string>(budget.understoodCriticalHeaders ?? []);
  const seen = new Set<string>();

  for (const name of crit) {
    if (typeof name !== 'string') {
      throw new JweHeaderAdmissionError(
        'CRIT_NOT_STRING',
        'crit 的每个元素都必须是字符串',
        'crit',
      );
    }
    if (seen.has(name)) {
      throw new JweHeaderAdmissionError(
        'CRIT_DUPLICATE',
        `crit 包含重复的参数名: ${name}`,
        'crit',
      );
    }
    seen.add(name);

    if (registered.has(name)) {
      throw new JweHeaderAdmissionError(
        'CRIT_REGISTERED_NAME',
        `RFC 注册参数名不允许出现在 crit 中: ${name}`,
        'crit',
      );
    }
    if (!(name in header)) {
      throw new JweHeaderAdmissionError(
        'CRIT_NAME_ABSENT',
        `crit 列出的参数必须同时出现在头中: ${name}`,
        'crit',
      );
    }
    if (!understood.has(name)) {
      throw new JweHeaderAdmissionError(
        'CRIT_NAME_NOT_UNDERSTOOD',
        `crit 列出的参数名未被调用方理解: ${name}`,
        'crit',
      );
    }
  }
}

function buildLimits(budget: AdmissionBudget): AdmissionLimits {
  return {
    p2c: { min: MIN_P2C, max: budget.maxP2c },
    p2sBytes: { min: MIN_P2S_BYTES, max: MAX_P2S_BYTES },
    maxCompressionRatio: MAX_COMPRESSION_RATIO,
    maxDecompressedBytes: budget.maxDecompressedBytes,
  };
}

function admitParsedHeader(
  header: JweProtectedHeader,
  compressedPayloadLength: number,
  budget: AdmissionBudget,
): AdmissionResult {
  validateBudget(budget);

  if (typeof header.alg !== 'string' || header.alg.length === 0) {
    throw new JweHeaderAdmissionError('ALG_MISSING', '头中必须包含非空字符串 alg', 'alg');
  }
  if ((FORBIDDEN_ALGORITHMS as readonly string[]).includes(header.alg)) {
    throw new JweHeaderAdmissionError(
      'ALG_FORBIDDEN',
      `alg ${header.alg} 被网关策略禁止`,
      'alg',
    );
  }

  if ((PBES2_ALGORITHMS as readonly string[]).includes(header.alg)) {
    checkPbes2Parameters(header, budget);
  }

  checkCompression(header, compressedPayloadLength, budget);
  checkCrit(header, budget);

  return { admitted: true, limits: buildLimits(budget) };
}

/**
 * 准入判定（不抛异常版本）。任何拒绝原因都通过返回值的 code/message 表达，
 * 适合网关直接映射为 400/422 响应；需要异常时使用 admitJweHeaderOrThrow。
 *
 * @param protectedHeader 受保护头：原始 JSON 字符串，或 JWE compact
 *   serialization 的第一段（base64url，自动识别）。
 * @param compressedPayloadLength 压缩后的密文/载荷字节长度（JWE 中为密文段长度）。
 * @param budget 调用方预算：p2c 上限、解压输出上限、可理解的 crit 名称。
 */
export function admitJweHeader(
  protectedHeader: string,
  compressedPayloadLength: number,
  budget: AdmissionBudget,
): AdmissionResult {
  try {
    return admitJweHeaderOrThrow(protectedHeader, compressedPayloadLength, budget);
  } catch (error) {
    if (error instanceof JweHeaderAdmissionError) {
      return {
        admitted: false,
        code: error.code,
        message: error.message,
        parameter: error.parameter,
      };
    }
    throw error;
  }
}

/**
 * 准入判定（抛异常版本）。校验全部为同步纯计算，不触发任何 PBES2 密钥派生，
 * 调用方应在本函数通过之后再执行 PBKDF2 派生与实际解压。
 */
export function admitJweHeaderOrThrow(
  protectedHeader: string,
  compressedPayloadLength: number,
  budget: AdmissionBudget,
): AdmissionResult {
  const trimmed = typeof protectedHeader === 'string' ? protectedHeader.trim() : '';
  const header = trimmed.startsWith('{')
    ? parseProtectedHeader(trimmed)
    : decodeCompactProtectedHeader(trimmed);
  return admitParsedHeader(header, compressedPayloadLength, budget);
}
