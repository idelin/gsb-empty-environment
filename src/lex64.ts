/**
 * id64 词法校验。
 *
 * JSON 数字在与字段类型比对之前必须原样保留词法表示（流式分词器把
 * 原始数字码元完整缓冲），因此这里的入参是字符串词法而非 JS number。
 *
 * 接受：
 *  - 规范十进制字符串：可选单个 '-'，随后为 '0' 或非零开头数字串；
 *  - JSON 数字词法：仅当它是精确安全整数（Number.isSafeInteger）、
 *    其 Number.prototype.toString 与原词法一致（即无前导零/无指数/-0）。
 *
 * 拒绝：前导零、科学计数法、'-0'、小数、非数字字符、超有符号 64 位范围。
 * 返回规范化十进制字符串（字符串形式与数字形式的规范结果相同）。
 */

import { DecodeError } from './types.ts';

// 负号后只允许非零开头整数，显式排除 "-0" 歧义。
const CANON_DECIMAL = /^(?:0|-[1-9][0-9]*|[1-9][0-9]*)$/;
const I64_MIN = -9223372036854775808n;
const I64_MAX = 9223372036854775807n;

/** 校验已是规范十进制词法的值（用于字符串承载的 id64）。 */
export function canonicalId64FromText(lexeme: string): string {
  if (!CANON_DECIMAL.test(lexeme)) {
    throw new DecodeError('ID64_LEXEME', `non-canonical id64 text: ${lexeme}`);
  }
  assertI64Range(lexeme);
  return lexeme;
}

/**
 * 校验 JSON 数字词法承载的 id64。
 * 必须是精确安全整数，且词法与规范 toString 完全一致。
 */
export function canonicalId64FromNumberLexeme(lexeme: string): string {
  if (!CANON_DECIMAL.test(lexeme)) {
    // 同时覆盖 1e3、1.0、01、-0 等一切非规范整数词法。
    throw new DecodeError('ID64_LEXEME', `non-canonical id64 number: ${lexeme}`);
  }
  const numeric = Number(lexeme);
  if (!Number.isSafeInteger(numeric)) {
    throw new DecodeError('ID64_LEXEME', `id64 number not a safe integer: ${lexeme}`);
  }
  if (String(numeric) !== lexeme) {
    // 理论上 CANON_DECIMAL 已排除 -0/前导零，保留为防御性双保险。
    throw new DecodeError('ID64_LEXEME', `id64 number lexeme ambiguity: ${lexeme}`);
  }
  assertI64Range(lexeme);
  return lexeme;
}

function assertI64Range(canonical: string): void {
  const value = BigInt(canonical);
  if (value < I64_MIN || value > I64_MAX) {
    throw new DecodeError('RANGE_ERROR', `id64 out of signed 64-bit range: ${canonical}`);
  }
}

/** 二进制入口：把有符号 64 位大整数转成与 JSON 入口一致的规范十进制。 */
export function id64FromBigInt(value: bigint): string {
  if (value < I64_MIN || value > I64_MAX) {
    throw new DecodeError('RANGE_ERROR', `id64 out of signed 64-bit range: ${value}`);
  }
  return value.toString();
}

export function isValidId64Text(lexeme: string): boolean {
  try {
    canonicalId64FromText(lexeme);
    return true;
  } catch {
    return false;
  }
}
