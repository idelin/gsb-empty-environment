import type { NormalizedKid } from './types.ts';

const HEX_RE = /^[0-9a-f]{32}$/;

/**
 * 把任意形式的 KID 标识规范化为 32 位小写无连字符十六进制串。
 *
 * 接受：
 * - 裸十六进制（32 hex，可含连字符/空格/URN 前缀）
 * - 标准 UUID：`01234567-89ab-cdef-0123-456789abcdef`
 * - URN：`urn:uuid:01234567-...`
 *
 * 输入非法（去除分隔后不是恰好 16 字节）时返回 null，由调用方产出
 * `DEFAULT_KID_MALFORMED` 问题。
 */
export function normalizeKid(raw: string): NormalizedKid | null {
  if (typeof raw !== 'string') return null;
  const stripped = raw
    .trim()
    .toLowerCase()
    .replace(/^urn:uuid:/, '')
    .replace(/[-\s{}]/g, '');
  if (!HEX_RE.test(stripped)) return null;
  return stripped;
}

/** 解析可能包含多个空格分隔 KID 的 `cenc:default_KID` 值。 */
export function normalizeKidList(raw: string): NormalizedKid[] {
  if (typeof raw !== 'string') return [];
  const out: NormalizedKid[] = [];
  for (const token of raw.split(/\s+/)) {
    if (!token) continue;
    const kid = normalizeKid(token);
    if (kid) out.push(kid);
  }
  return out;
}

/** 16 字节数组转规范化 KID。 */
export function bytesToKid(bytes: Uint8Array): NormalizedKid | null {
  if (!bytes || bytes.length !== 16) return null;
  let hex = '';
  for (const b of bytes) hex += b.toString(16).padStart(2, '0');
  return hex;
}

/** 规范化 KID 转回 16 字节数组（用于比对或构造测试数据）。 */
export function kidToBytes(kid: string): Uint8Array | null {
  const normalized = normalizeKid(kid);
  if (!normalized) return null;
  const out = new Uint8Array(16);
  for (let i = 0; i < 16; i++) {
    out[i] = parseInt(normalized.slice(i * 2, i * 2 + 2), 16);
  }
  return out;
}

/** 大小写/连字符不敏感的 KID 相等比较。 */
export function kidEquals(a: string, b: string): boolean {
  const na = normalizeKid(a);
  const nb = normalizeKid(b);
  return na !== null && na === nb;
}
